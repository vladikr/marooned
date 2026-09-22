package translate

import (
	"encoding/json"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"
	virtv1 "kubevirt.io/api/core/v1"

	"maroonedpods.io/maroonedpods/pkg/sandbox"
	"maroonedpods.io/maroonedpods/pkg/util"
	mpv1 "maroonedpods.io/maroonedpods/staging/src/maroonedpods.io/api/pkg/apis/core/v1alpha1"
)

func testConfig() mpv1.SandboxConfig {
	return sandbox.EffectiveSandbox(nil)
}

func podWithResources(cpu, mem string) *corev1.Pod {
	p := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "box", Namespace: "app"},
		Spec: corev1.PodSpec{
			RuntimeClassName: pointer.String(util.RuntimeClassName),
			Containers: []corev1.Container{{
				Name:  "box",
				Image: "busybox",
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{},
				},
			}},
		},
	}
	if cpu != "" {
		p.Spec.Containers[0].Resources.Requests[corev1.ResourceCPU] = resource.MustParse(cpu)
	}
	if mem != "" {
		p.Spec.Containers[0].Resources.Requests[corev1.ResourceMemory] = resource.MustParse(mem)
	}
	return p
}

func TestTranslateCPUMemory(t *testing.T) {
	tests := []struct {
		name     string
		cpu      string
		mem      string
		wantCPU  uint32
		wantMem  string
		overhead bool
	}{
		{name: "empty requests floor to 1 cpu", cpu: "", mem: "", wantCPU: 1},
		{name: "500m ceils to 1", cpu: "500m", mem: "256Mi", wantCPU: 1},
		{name: "1500m plus 50m overhead ceils to 2", cpu: "1500m", mem: "1Gi", wantCPU: 2},
		{name: "sums two containers", cpu: "1", mem: "512Mi", wantCPU: 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := podWithResources(tc.cpu, tc.mem)
			if tc.name == "sums two containers" {
				p.Spec.Containers = append(p.Spec.Containers, corev1.Container{
					Name: "sidecar",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("500m")},
					},
				})
			}
			res := Translate(Input{Pod: p, Config: testConfig(), Node: "worker-1"})
			if len(res.Errors) != 0 {
				t.Fatalf("errors: %v", res.Errors)
			}
			if res.GuestCPU != tc.wantCPU {
				t.Errorf("cpu got %d want %d", res.GuestCPU, tc.wantCPU)
			}
			if res.VMI.Spec.Domain.CPU.Cores != tc.wantCPU {
				t.Errorf("vmi cpu cores %d", res.VMI.Spec.Domain.CPU.Cores)
			}
			if res.VMI.Spec.Domain.Memory == nil || res.VMI.Spec.Domain.Memory.Guest == nil {
				t.Fatal("guest memory missing")
			}
			if res.VMI.Namespace != util.DefaultInfraNamespace {
				t.Errorf("namespace %s", res.VMI.Namespace)
			}
			if res.VMI.Spec.NodeSelector["kubernetes.io/hostname"] != "worker-1" {
				t.Errorf("node selector missing")
			}
		})
	}
}

func TestTranslateMemoryFloor(t *testing.T) {
	p := podWithResources("", "")
	res := Translate(Input{Pod: p, Config: testConfig(), Node: "worker-1"})
	got := res.GuestMem.Value()
	min := int64(512 * 1024 * 1024)
	if got < min {
		t.Fatalf("guest memory %d below 512Mi floor (was ExtraGuestOverhead-only 64Mi; guest OOM panics)", got)
	}
}

func TestTranslateSandboxLauncherLabel(t *testing.T) {
	p := podWithResources("1", "512Mi")
	p.UID = "pod-uid-1"
	res := Translate(Input{Pod: p, Config: testConfig(), Node: "worker-1", OwnerPod: true})
	if res.VMI.Labels[util.SandboxVMILabel] != "true" {
		t.Fatal("sandbox VMI must be labeled so virt-launcher is selectable")
	}
	if res.VMI.Labels[util.SandboxIDLabel] != "pod-uid-1" {
		t.Fatalf("sandbox-id %s", res.VMI.Labels[util.SandboxIDLabel])
	}
}

func TestTranslateUserRootfsEmptyDisk(t *testing.T) {
	p := podWithResources("1", "512Mi")
	res := Translate(Input{Pod: p, Config: testConfig(), Node: "worker-1", RootfsBytes: 5 * 1024 * 1024})
	found := false
	for _, vol := range res.VMI.Spec.Volumes {
		if vol.Name == sandbox.UserRootfsVolume && vol.EmptyDisk != nil {
			found = true
			if vol.EmptyDisk.Capacity.Value() != 256*1024*1024 {
				t.Fatalf("capacity %d", vol.EmptyDisk.Capacity.Value())
			}
		}
	}
	if !found {
		t.Fatal("missing user-rootfs emptyDisk")
	}
	serial := false
	for _, d := range res.VMI.Spec.Domain.Devices.Disks {
		if d.Name == sandbox.UserRootfsVolume && d.Serial == sandbox.UserRootfsSerial {
			serial = true
		}
	}
	if !serial {
		t.Fatal("user-rootfs disk serial")
	}
}

func TestTranslateAutoattachVSOCK(t *testing.T) {
	p := podWithResources("1", "512Mi")
	res := Translate(Input{Pod: p, Config: testConfig(), Node: "worker-1"})
	if res.VMI.Spec.Domain.Devices.AutoattachVSOCK == nil || !*res.VMI.Spec.Domain.Devices.AutoattachVSOCK {
		t.Fatal("sandbox VMI must autoattach vsock")
	}
}

func TestTranslateDefaultAgentDisk(t *testing.T) {
	p := podWithResources("1", "512Mi")
	res := Translate(Input{Pod: p, Config: testConfig(), Node: "worker-1"})
	if len(res.Errors) != 0 {
		t.Fatalf("errors: %v", res.Errors)
	}
	found := false
	for _, vol := range res.VMI.Spec.Volumes {
		if vol.ContainerDisk != nil && vol.ContainerDisk.Image == util.DefaultSandboxRootfsImage {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected rootfs %s", util.DefaultSandboxRootfsImage)
	}
	fw := res.VMI.Spec.Domain.Firmware
	if fw == nil || fw.KernelBoot == nil || fw.KernelBoot.Container == nil {
		t.Fatal("non-TEE guest must kernelBoot the agent disk")
	}
	if fw.KernelBoot.Container.Image != util.DefaultSandboxKernelImage {
		t.Fatalf("kernel image %s", fw.KernelBoot.Container.Image)
	}
	if fw.KernelBoot.Container.ImagePullPolicy != corev1.PullIfNotPresent {
		t.Fatalf("kernel pull %s", fw.KernelBoot.Container.ImagePullPolicy)
	}
	for _, vol := range res.VMI.Spec.Volumes {
		if vol.ContainerDisk != nil && vol.ContainerDisk.ImagePullPolicy != corev1.PullIfNotPresent {
			t.Fatalf("rootfs pull %s", vol.ContainerDisk.ImagePullPolicy)
		}
	}
}

func TestTranslateHugepages(t *testing.T) {
	p := podWithResources("1", "1Gi")
	p.Spec.Containers[0].Resources.Requests[corev1.ResourceName("hugepages-2Mi")] = resource.MustParse("2Gi")
	res := Translate(Input{Pod: p, Config: testConfig()})
	if len(res.Errors) != 0 {
		t.Fatalf("errors: %v", res.Errors)
	}
	if res.Hugepage != "2Mi" {
		t.Fatalf("hugepage %s", res.Hugepage)
	}
	if res.VMI.Spec.Domain.Memory.Hugepages == nil || res.VMI.Spec.Domain.Memory.Hugepages.PageSize != "2Mi" {
		t.Fatalf("vmi hugepages not set")
	}
	got := res.VMI.Spec.Domain.Resources.Requests[corev1.ResourceName("hugepages-2Mi")]
	guest := *res.VMI.Spec.Domain.Memory.Guest
	if got.Cmp(guest) != 0 {
		t.Fatalf("vmi hugepages request %s want guest %s", got.String(), guest.String())
	}
}

func TestTranslateHugepagesLessThanGuestSkipped(t *testing.T) {
	p := podWithResources("1", "512Mi")
	p.Spec.Containers[0].Resources.Requests[corev1.ResourceName("hugepages-2Mi")] = resource.MustParse("64Mi")
	res := Translate(Input{Pod: p, Config: testConfig()})
	if len(res.Errors) != 0 {
		t.Fatalf("errors: %v", res.Errors)
	}
	if res.Hugepage != "" {
		t.Fatalf("hugepage %s, want skipped", res.Hugepage)
	}
	if res.VMI.Spec.Domain.Memory != nil && res.VMI.Spec.Domain.Memory.Hugepages != nil {
		t.Fatal("VMI must not hugepage-back when request < guest RAM")
	}
	if len(res.Warnings) == 0 {
		t.Fatal("expected warning")
	}
}

func TestTranslateHugepagesAndTEEAllowed(t *testing.T) {
	p := podWithResources("1", "1Gi")
	p.Annotations = map[string]string{util.TEEAnnotation: sandbox.TEESNP}
	p.Spec.Containers[0].Resources.Requests[corev1.ResourceName("hugepages-2Mi")] = resource.MustParse("2Gi")
	res := Translate(Input{Pod: p, Config: testConfig()})
	if len(res.Errors) != 0 {
		t.Fatalf("hugepages + TEE should be allowed: %v", res.Errors)
	}
	if res.VMI.Spec.Domain.Firmware == nil || res.VMI.Spec.Domain.Firmware.KernelBoot != nil {
		t.Fatalf("TEE must use UEFI, not kernelBoot")
	}
	if res.VMI.Spec.Domain.Firmware.Bootloader == nil || res.VMI.Spec.Domain.Firmware.Bootloader.EFI == nil {
		t.Fatal("missing EFI")
	}
	if res.VMI.Spec.Domain.Firmware.Bootloader.EFI.SecureBoot == nil || *res.VMI.Spec.Domain.Firmware.Bootloader.EFI.SecureBoot {
		t.Fatal("secureBoot should be false")
	}
}

func TestTranslateSRIOV(t *testing.T) {
	p := podWithResources("1", "1Gi")
	p.Annotations = map[string]string{util.SRIOVNetworkAnnotation: "sriov-nad"}
	p.Spec.Containers[0].Resources.Requests[corev1.ResourceName("intel.com/sriov")] = resource.MustParse("1")
	res := Translate(Input{Pod: p, Config: testConfig()})
	if len(res.Errors) != 0 {
		t.Fatalf("%v", res.Errors)
	}
	found := false
	for _, iface := range res.VMI.Spec.Domain.Devices.Interfaces {
		if iface.SRIOV != nil {
			found = true
		}
	}
	if !found {
		t.Fatal("expected sriov interface")
	}
	if len(res.VMI.Spec.Domain.Devices.Interfaces) < 2 {
		t.Fatal("sriov must be a second interface, not a replacement for l2bridge")
	}
}

func TestTranslateSRIOVMissingNAD(t *testing.T) {
	p := podWithResources("1", "1Gi")
	p.Spec.Containers[0].Resources.Requests[corev1.ResourceName("intel.com/sriov")] = resource.MustParse("1")
	res := Translate(Input{Pod: p, Config: testConfig()})
	if len(res.Errors) == 0 {
		t.Fatal("expected NAD error")
	}
}

func TestTranslateTEESRIOVRejected(t *testing.T) {
	p := podWithResources("1", "1Gi")
	p.Annotations = map[string]string{
		util.TEEAnnotation:          sandbox.TEESNP,
		util.SRIOVNetworkAnnotation: "sriov-nad",
	}
	p.Spec.Containers[0].Resources.Requests[corev1.ResourceName("intel.com/sriov")] = resource.MustParse("1")
	res := Translate(Input{Pod: p, Config: testConfig()})
	if len(res.Errors) == 0 {
		t.Fatal("expected TEE+SR-IOV reject")
	}
}

func TestTranslateDRARejected(t *testing.T) {
	p := podWithResources("1", "1Gi")
	p.Spec.ResourceClaims = []corev1.PodResourceClaim{{Name: "gpu"}}
	res := Translate(Input{Pod: p, Config: testConfig()})
	if len(res.Errors) == 0 || !strings.Contains(res.Errors[0].Error(), "DRA") {
		t.Fatalf("expected DRA error, got %v", res.Errors)
	}
}

func TestTranslatePVCAsVirtioBlk(t *testing.T) {
	p := podWithResources("1", "1Gi")
	p.Spec.Volumes = []corev1.Volume{{
		Name: "data",
		VolumeSource: corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "mypvc"},
		},
	}}
	p.Spec.Containers[0].VolumeMounts = []corev1.VolumeMount{{Name: "data", MountPath: "/data"}}
	res := Translate(Input{Pod: p, Config: testConfig()})
	if len(res.Errors) != 0 {
		t.Fatalf("%v", res.Errors)
	}
	foundDisk := false
	for _, d := range res.VMI.Spec.Domain.Devices.Disks {
		if d.Name == "vol-data" {
			foundDisk = true
		}
	}
	if !foundDisk {
		t.Fatal("expected virtio-blk disk for PVC")
	}
	var pvc *virtv1.PersistentVolumeClaimVolumeSource
	for i := range res.VMI.Spec.Volumes {
		if res.VMI.Spec.Volumes[i].PersistentVolumeClaim != nil && res.VMI.Spec.Volumes[i].Name == "vol-data" {
			pvc = res.VMI.Spec.Volumes[i].PersistentVolumeClaim
		}
	}
	if pvc == nil || pvc.ClaimName != "mypvc" {
		t.Fatalf("claimName not preserved: %+v", pvc)
	}
	for _, d := range res.VMI.Spec.Domain.Devices.Disks {
		if d.Name == "vol-data" {
			if d.Disk == nil || d.Disk.Bus != virtv1.DiskBusVirtio {
				t.Fatalf("expected virtio bus, got %+v", d.Disk)
			}
		}
	}
	if len(res.MountTable) != 1 || res.MountTable[0].Kind != "virtio-blk" || res.MountTable[0].GuestPath != "/data" {
		t.Fatalf("mount table: %+v", res.MountTable)
	}
}

func TestTranslatePVCSameNamespaceNoNamespaceField(t *testing.T) {
	p := podWithResources("1", "1Gi")
	p.UID = "11111111-2222-3333-4444-555555555555"
	p.Spec.Volumes = []corev1.Volume{{
		Name: "data",
		VolumeSource: corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "mypvc"},
		},
	}}
	res := Translate(Input{
		Pod:       p,
		Config:    testConfig(),
		Namespace: p.Namespace,
		Name:      sandbox.UserNamespaceVMIName(p.UID),
		OwnerPod:  true,
	})
	if res.VMI.Namespace != "app" {
		t.Fatalf("namespace %s", res.VMI.Namespace)
	}
	if res.VMI.Name != "marooned-11111111-2222-3333-4444-555555555555" {
		t.Fatalf("name %s", res.VMI.Name)
	}
	if _, ok := res.VMI.Labels[util.WarmPoolStateLabel]; ok {
		t.Fatal("in-ns VMI must not join the infra pool")
	}
	if len(res.VMI.OwnerReferences) != 1 || res.VMI.OwnerReferences[0].Kind != "Pod" {
		t.Fatalf("ownerRef %+v", res.VMI.OwnerReferences)
	}
	raw, err := json.Marshal(res.VMI.Spec.Volumes)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"namespace"`) {
		t.Fatalf("VMI volume must not set namespace: %s", raw)
	}
}

func TestTranslateEmptyDirTmpfs(t *testing.T) {
	p := podWithResources("1", "1Gi")
	p.Spec.Volumes = []corev1.Volume{{
		Name:         "tmp",
		VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
	}}
	p.Spec.Containers[0].VolumeMounts = []corev1.VolumeMount{{Name: "tmp", MountPath: "/scratch"}}
	res := Translate(Input{Pod: p, Config: testConfig()})
	if len(res.MountTable) != 1 || res.MountTable[0].Kind != "tmpfs" {
		t.Fatalf("expected tmpfs mount, got %+v", res.MountTable)
	}
}

func TestTranslateConfigMapFiles(t *testing.T) {
	p := podWithResources("1", "1Gi")
	p.Spec.Volumes = []corev1.Volume{{
		Name:         "cfg",
		VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{LocalObjectReference: corev1.LocalObjectReference{Name: "c"}}},
	}}
	p.Spec.Containers[0].VolumeMounts = []corev1.VolumeMount{{Name: "cfg", MountPath: "/etc/cfg"}}
	res := Translate(Input{Pod: p, Config: testConfig()})
	if len(res.MountTable) != 1 || res.MountTable[0].Kind != "files" {
		t.Fatalf("expected files mount, got %+v", res.MountTable)
	}
}

func TestTranslateNetworkMasqueradeDefault(t *testing.T) {
	p := podWithResources("1", "1Gi")
	res := Translate(Input{Pod: p, Config: testConfig()})
	iface := res.VMI.Spec.Domain.Devices.Interfaces[0]
	if iface.Masquerade == nil {
		t.Fatalf("expected masquerade default, got %+v", iface)
	}
	if len(iface.Ports) < 1 || iface.Ports[0].Port != 1024 {
		t.Fatalf("masquerade must expose agent port 1024, got %+v", iface.Ports)
	}
}

func TestTranslateMasqueradeExposesContainerPorts(t *testing.T) {
	p := podWithResources("1", "1Gi")
	p.Spec.Containers[0].Ports = []corev1.ContainerPort{{Name: "http", ContainerPort: 8080, Protocol: corev1.ProtocolTCP}}
	res := Translate(Input{Pod: p, Config: testConfig()})
	iface := res.VMI.Spec.Domain.Devices.Interfaces[0]
	if len(iface.Ports) != 2 {
		t.Fatalf("ports %+v", iface.Ports)
	}
	if iface.Ports[1].Port != 8080 || iface.Ports[1].Name != "http" {
		t.Fatalf("workload port %+v", iface.Ports[1])
	}
	if res.VMI.Spec.Networks[0].Pod == nil {
		t.Fatal("network source must be pod: {}")
	}
}

func TestTranslateNetworkL2BridgeWhenConfigured(t *testing.T) {
	cfg := testConfig()
	cfg.Network = &mpv1.SandboxNetwork{Binding: sandbox.BindingL2Bridge}
	p := podWithResources("1", "1Gi")
	res := Translate(Input{Pod: p, Config: cfg})
	iface := res.VMI.Spec.Domain.Devices.Interfaces[0]
	if iface.Binding == nil || iface.Binding.Name != sandbox.BindingL2Bridge {
		t.Fatalf("expected l2bridge plugin binding, got %+v", iface)
	}
}

func TestTranslateNetworkMasqueradeFallback(t *testing.T) {
	cfg := testConfig()
	cfg.Network = &mpv1.SandboxNetwork{Binding: sandbox.BindingMasquerade}
	p := podWithResources("1", "1Gi")
	res := Translate(Input{Pod: p, Config: cfg})
	iface := res.VMI.Spec.Domain.Devices.Interfaces[0]
	if iface.Masquerade == nil {
		t.Fatalf("expected masquerade, got %+v", iface)
	}
}

func TestTranslateHiddenVMILabels(t *testing.T) {
	p := podWithResources("1", "512Mi")
	res := Translate(Input{Pod: p, Config: testConfig(), Node: "n1"})
	if res.VMI.Labels[util.SandboxModeLabel] != util.SandboxModeSandbox {
		t.Fatal("mode label")
	}
	if res.VMI.Annotations[util.WarmPoolClaimedByLabel] != "app/box" {
		t.Fatal("claimed-by")
	}
	if *res.VMI.Spec.Domain.Devices.AutoattachGraphicsDevice {
		t.Fatal("graphics should be off")
	}
}

func TestTranslateTDXDisablesSMM(t *testing.T) {
	p := podWithResources("1", "1Gi")
	p.Annotations = map[string]string{util.TEEAnnotation: sandbox.TEETDX}
	res := Translate(Input{Pod: p, Config: testConfig(), Node: "n1"})
	if res.VMI.Spec.Domain.Features == nil || res.VMI.Spec.Domain.Features.SMM == nil ||
		res.VMI.Spec.Domain.Features.SMM.Enabled == nil || *res.VMI.Spec.Domain.Features.SMM.Enabled {
		t.Fatal("TDX requires smm.enabled=false")
	}
	if res.VMI.Spec.NodeSelector["kubevirt.io/tdx"] != "true" {
		t.Fatal("tdx nodeSelector")
	}
	if res.VMI.Spec.Domain.CPU.Model != "host-passthrough" {
		t.Fatal("cpu model")
	}
}

func TestTranslateNilPod(t *testing.T) {
	res := Translate(Input{})
	if len(res.Errors) == 0 {
		t.Fatal("expected error")
	}
}

func TestApplyRWXFilesystem(t *testing.T) {
	p := podWithResources("1", "1Gi")
	p.Spec.Volumes = []corev1.Volume{{
		Name: "share",
		VolumeSource: corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "rwx"},
		},
	}}
	res := Translate(Input{Pod: p, Config: testConfig()})
	if err := ApplyRWXFilesystem(res.VMI, "share"); err != nil {
		t.Fatal(err)
	}
	for _, d := range res.VMI.Spec.Domain.Devices.Disks {
		if d.Name == "vol-share" {
			t.Fatal("disk should be removed for virtiofs")
		}
	}
	if len(res.VMI.Spec.Domain.Devices.Filesystems) != 1 {
		t.Fatal("expected virtiofs filesystem")
	}
}

func TestPickSizeClass(t *testing.T) {
	classes := testConfig().PoolSizeClasses
	got := pickSizeClass(1, resource.MustParse("256Mi"), classes)
	if got != "s" {
		t.Fatalf("got %s", got)
	}
	got = pickSizeClass(2, resource.MustParse("1Gi"), classes)
	if got != "m" {
		t.Fatalf("got %s", got)
	}
}
