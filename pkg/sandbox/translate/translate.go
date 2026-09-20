package translate

import (
	"fmt"
	"math"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	virtv1 "kubevirt.io/api/core/v1"

	"maroonedpods.io/maroonedpods/pkg/sandbox"
	"maroonedpods.io/maroonedpods/pkg/util"
	mpv1 "maroonedpods.io/maroonedpods/staging/src/maroonedpods.io/api/pkg/apis/core/v1alpha1"
)

const (
	rootDiskName    = "rootdisk"
	defaultNetName  = "default"
	sriovNetName    = "sriov"
	teeMemorySlopMi = 256
)

// Result is a VMI spec plus translation diagnostics.
type Result struct {
	VMI        *virtv1.VirtualMachineInstance
	SizeClass  string
	TEE        string
	GuestCPU   uint32
	GuestMem   resource.Quantity
	Hugepage   string
	Errors     []error
	Warnings   []string
	MountTable []Mount
}

// Mount is a guest path the agent must set up from an attached disk or tmpfs.
type Mount struct {
	VolumeName string
	GuestPath  string
	Kind       string // virtio-blk, virtiofs, tmpfs, files
	ReadOnly   bool
}

// Input is the pod plus resolved sandbox config and bound node.
type Input struct {
	Pod       *corev1.Pod
	Config    mpv1.SandboxConfig
	Node        string
	TEE         string
	Namespace   string
	Name        string
	OwnerPod    bool
	RootfsBytes int64
}

// Translate builds a hidden sandbox VMI from a user Pod.
func Translate(in Input) Result {
	res := Result{TEE: resolveTEE(in)}
	if in.Pod == nil {
		res.Errors = append(res.Errors, fmt.Errorf("pod is nil"))
		return res
	}
	if err := checkConflicts(in.Pod, res.TEE); err != nil {
		res.Errors = append(res.Errors, err)
		return res
	}

	cpu, mem := guestCompute(in.Pod, in.Config, res.TEE)
	res.GuestCPU = cpu
	res.GuestMem = mem
	res.SizeClass = pickSizeClass(cpu, mem, in.Config.PoolSizeClasses)

	ns := in.Namespace
	if ns == "" {
		ns = in.Config.InfraNamespace
	}
	name := in.Name
	if name == "" {
		name = sandboxVMIName(in.Pod)
	}
	vmi := virtv1.NewVMIReferenceFromNameWithNS(ns, name)
	vmi.TypeMeta = metav1.TypeMeta{
		APIVersion: virtv1.GroupVersion.String(),
		Kind:       "VirtualMachineInstance",
	}
	vmi.Spec = virtv1.VirtualMachineInstanceSpec{Domain: virtv1.DomainSpec{}}
	falseVal := false
	trueVal := true
	vmi.Spec.Domain.Devices.AutoattachGraphicsDevice = &falseVal
	vmi.Spec.Domain.Devices.AutoattachVSOCK = &trueVal
	vmi.Spec.Domain.CPU = &virtv1.CPU{Cores: cpu, Sockets: 1, Threads: 1}
	guestMem := mem.DeepCopy()
	vmi.Spec.Domain.Memory = &virtv1.Memory{Guest: &guestMem}

	vmi.Labels = map[string]string{
		util.SandboxModeLabel:      util.SandboxModeSandbox,
		util.SandboxVMILabel:       "true",
		util.SandboxTEELabel:       res.TEE,
		util.SandboxSizeClassLabel: res.SizeClass,
	}
	if !in.OwnerPod {
		vmi.Labels[util.WarmPoolStateLabel] = util.PoolStateCreating
	}
	if in.Node != "" {
		vmi.Labels[util.SandboxNodeLabel] = in.Node
		if vmi.Spec.NodeSelector == nil {
			vmi.Spec.NodeSelector = map[string]string{}
		}
		vmi.Spec.NodeSelector["kubernetes.io/hostname"] = in.Node
	}
	if in.Pod != nil {
		if vmi.Annotations == nil {
			vmi.Annotations = map[string]string{}
		}
		vmi.Annotations[util.WarmPoolClaimedByLabel] = claimedBy(in.Pod)
		vmi.Annotations[util.VMIAnnotation] = fmt.Sprintf("%s/%s", in.Pod.Namespace, in.Pod.Name)
		if in.Pod.UID != "" {
			vmi.Labels[util.SandboxIDLabel] = string(in.Pod.UID)
		}
		if in.OwnerPod && in.Pod.UID != "" {
			controller := true
			block := true
			vmi.OwnerReferences = []metav1.OwnerReference{{
				APIVersion:         "v1",
				Kind:               "Pod",
				Name:               in.Pod.Name,
				UID:                in.Pod.UID,
				Controller:         &controller,
				BlockOwnerDeletion: &block,
			}}
		}
	}

	applyBoot(vmi, in.Config, res.TEE)
	applyNetwork(vmi, in.Config)
	applyRootfs(vmi, in.Config, res.TEE)
	applyUserRootfs(vmi, in.RootfsBytes)
	hugepage, hugepageErr := applyHugepages(vmi, in.Pod)
	if hugepageErr != nil {
		res.Errors = append(res.Errors, hugepageErr)
	}
	res.Hugepage = hugepage
	if hugepage == "" && hugepageErr == nil {
		if _, q := podHugepageRequest(in.Pod); q.CmpInt64(0) > 0 {
			res.Warnings = append(res.Warnings, "hugepages request is smaller than guest memory; VMI is not hugepage-backed")
		}
	}

	if err := applySRIOV(vmi, in.Pod, in.Config, res.TEE); err != nil {
		res.Errors = append(res.Errors, err)
	}
	if err := applyDRA(vmi, in.Pod); err != nil {
		res.Errors = append(res.Errors, err)
	}
	mounts, volErrs := applyVolumes(vmi, in.Pod, res.TEE)
	res.MountTable = mounts
	res.Errors = append(res.Errors, volErrs...)

	res.VMI = vmi
	return res
}

func sandboxVMIName(pod *corev1.Pod) string {
	ns := pod.Namespace
	if len(ns) > 20 {
		ns = ns[:20]
	}
	name := pod.Name
	if len(name) > 20 {
		name = name[:20]
	}
	return fmt.Sprintf("sb-%s-%s", ns, name)
}

func claimedBy(pod *corev1.Pod) string {
	return pod.Namespace + "/" + pod.Name
}

func resolveTEE(in Input) string {
	if in.TEE != "" {
		return in.TEE
	}
	if in.Pod != nil && in.Pod.Annotations != nil {
		if v := in.Pod.Annotations[util.TEEAnnotation]; v != "" {
			return v
		}
	}
	if in.Config.ConfidentialCompute != nil && in.Config.ConfidentialCompute.Default != "" &&
		in.Config.ConfidentialCompute.Default != sandbox.TEEAnnotationValue {
		return in.Config.ConfidentialCompute.Default
	}
	return sandbox.TEEOff
}

func checkConflicts(pod *corev1.Pod, tee string) error {
	if tee == sandbox.TEEOff {
		return nil
	}
	if hasSRIOVOrHostDevice(pod) {
		return fmt.Errorf("TEE + SR-IOV/GPU/hostDevices is not supported")
	}
	if hasRWXVirtiofs(pod) {
		return fmt.Errorf("TEE + virtiofs RWX is not supported")
	}
	return nil
}

func hasSRIOVOrHostDevice(pod *corev1.Pod) bool {
	for _, c := range pod.Spec.Containers {
		for name := range c.Resources.Requests {
			s := string(name)
			if strings.HasPrefix(s, "intel.com/") || strings.HasPrefix(s, "nvidia.com/") ||
				strings.Contains(s, "sriov") || strings.HasPrefix(s, "gpu") {
				return true
			}
		}
	}
	return len(pod.Spec.ResourceClaims) > 0
}

func hasRWXVirtiofs(pod *corev1.Pod) bool {
	for _, vol := range pod.Spec.Volumes {
		if vol.PersistentVolumeClaim != nil && vol.PersistentVolumeClaim.ReadOnly == false {
			// Access mode is not on the pod volume; adaptor may still reject RWX+TEE later.
			_ = vol
		}
	}
	return false
}

func guestCompute(pod *corev1.Pod, cfg mpv1.SandboxConfig, tee string) (uint32, resource.Quantity) {
	cpuMillis := int64(0)
	memBytes := int64(0)
	for _, c := range append(append([]corev1.Container{}, pod.Spec.Containers...), pod.Spec.InitContainers...) {
		if q, ok := c.Resources.Requests[corev1.ResourceCPU]; ok {
			cpuMillis += q.MilliValue()
		}
		if q, ok := c.Resources.Requests[corev1.ResourceMemory]; ok {
			memBytes += q.Value()
		}
	}
	if cfg.ExtraGuestOverhead != nil {
		if q, ok := (*cfg.ExtraGuestOverhead)[corev1.ResourceCPU]; ok {
			cpuMillis += q.MilliValue()
		}
		if q, ok := (*cfg.ExtraGuestOverhead)[corev1.ResourceMemory]; ok {
			memBytes += q.Value()
		}
	}
	cores := uint32(math.Ceil(float64(cpuMillis) / 1000.0))
	if cores < 1 {
		cores = 1
	}
	// linux-lts + initramfs unpack OOMs at 64Mi (serial: "deadlocked on memory").
	const minGuestMemory = 512 * 1024 * 1024
	if memBytes < minGuestMemory {
		memBytes = minGuestMemory
	}
	if tee == sandbox.TEESNP || tee == sandbox.TEETDX {
		memBytes += int64(teeMemorySlopMi) * 1024 * 1024
	}
	return cores, *resource.NewQuantity(memBytes, resource.BinarySI)
}

func pickSizeClass(cpu uint32, mem resource.Quantity, classes []mpv1.SandboxSizeClass) string {
	if len(classes) == 0 {
		return "custom"
	}
	best := classes[len(classes)-1].Name
	for _, cl := range classes {
		c, err1 := resource.ParseQuantity(cl.GuestCPU)
		m, err2 := resource.ParseQuantity(cl.GuestMemory)
		if err1 != nil || err2 != nil {
			continue
		}
		classCPU := uint32(math.Ceil(float64(c.MilliValue()) / 1000.0))
		if classCPU == 0 {
			classCPU = 1
		}
		if classCPU >= cpu && m.Cmp(mem) >= 0 {
			return cl.Name
		}
		best = cl.Name
	}
	return best
}

func applyBoot(vmi *virtv1.VirtualMachineInstance, cfg mpv1.SandboxConfig, tee string) {
	if tee == sandbox.TEESNP || tee == sandbox.TEETDX {
		secureBoot := false
		vmi.Spec.Domain.Firmware = &virtv1.Firmware{
			Bootloader: &virtv1.Bootloader{
				EFI: &virtv1.EFI{SecureBoot: &secureBoot},
			},
		}
		vmi.Spec.Domain.CPU.Model = "host-passthrough"
		if vmi.Spec.NodeSelector == nil {
			vmi.Spec.NodeSelector = map[string]string{}
		}
		if tee == sandbox.TEESNP {
			vmi.Spec.NodeSelector["kubevirt.io/sev"] = "true"
		}
		if tee == sandbox.TEETDX {
			vmi.Spec.NodeSelector["kubevirt.io/tdx"] = "true"
			enabled := false
			vmi.Spec.Domain.Features = &virtv1.Features{
				SMM: &virtv1.FeatureState{Enabled: &enabled},
			}
		}
		vmi.Spec.Domain.Devices.Rng = &virtv1.Rng{}
		if vmi.Annotations == nil {
			vmi.Annotations = map[string]string{}
		}
		vmi.Annotations[util.TEEAnnotation] = tee
		return
	}
	kb := cfg.KernelBoot
	if kb == nil {
		return
	}
	vmi.Spec.Domain.Firmware = &virtv1.Firmware{
		KernelBoot: &virtv1.KernelBoot{
			KernelArgs: kb.KernelArgs,
			Container: &virtv1.KernelBootContainer{
				Image:      kb.Image,
				KernelPath: kb.KernelPath,
				InitrdPath: kb.InitrdPath,
			},
		},
	}
}

func applyNetwork(vmi *virtv1.VirtualMachineInstance, cfg mpv1.SandboxConfig) {
	iface := virtv1.Interface{Name: defaultNetName}
	binding := sandbox.BindingL2Bridge
	if cfg.Network != nil && cfg.Network.Binding != "" {
		binding = cfg.Network.Binding
	}
	if binding == sandbox.BindingMasquerade {
		iface.InterfaceBindingMethod = virtv1.InterfaceBindingMethod{Masquerade: &virtv1.InterfaceMasquerade{}}
		iface.Ports = []virtv1.Port{{Name: "agent", Port: int32(sandbox.DefaultAgentPort), Protocol: "TCP"}}
	} else {
		iface.Binding = &virtv1.PluginBinding{Name: binding}
	}
	vmi.Spec.Domain.Devices.Interfaces = append(vmi.Spec.Domain.Devices.Interfaces, iface)
	vmi.Spec.Networks = append(vmi.Spec.Networks, virtv1.Network{
		Name:          defaultNetName,
		NetworkSource: virtv1.NetworkSource{Pod: &virtv1.PodNetwork{}},
	})
}

func applyRootfs(vmi *virtv1.VirtualMachineInstance, cfg mpv1.SandboxConfig, tee string) {
	image := cfg.RootfsImage
	if image == "" {
		image = util.DefaultSandboxRootfsImage
	}
	vmi.Spec.Domain.Devices.Disks = append(vmi.Spec.Domain.Devices.Disks, virtv1.Disk{
		Name: rootDiskName,
		DiskDevice: virtv1.DiskDevice{
			Disk: &virtv1.DiskTarget{Bus: virtv1.DiskBusVirtio},
		},
	})
	vmi.Spec.Volumes = append(vmi.Spec.Volumes, virtv1.Volume{
		Name: rootDiskName,
		VolumeSource: virtv1.VolumeSource{
			ContainerDisk: &virtv1.ContainerDiskSource{Image: image},
		},
	})
	_ = tee
}

func applyUserRootfs(vmi *virtv1.VirtualMachineInstance, imageBytes int64) {
	cap := sandbox.UserRootfsCapacity(imageBytes)
	vmi.Spec.Domain.Devices.Disks = append(vmi.Spec.Domain.Devices.Disks, virtv1.Disk{
		Name:   sandbox.UserRootfsVolume,
		Serial: sandbox.UserRootfsSerial,
		DiskDevice: virtv1.DiskDevice{
			Disk: &virtv1.DiskTarget{Bus: virtv1.DiskBusVirtio},
		},
	})
	vmi.Spec.Volumes = append(vmi.Spec.Volumes, virtv1.Volume{
		Name: sandbox.UserRootfsVolume,
		VolumeSource: virtv1.VolumeSource{
			EmptyDisk: &virtv1.EmptyDiskSource{Capacity: cap},
		},
	})
}

func podHugepageRequest(pod *corev1.Pod) (page string, qty resource.Quantity) {
	for _, c := range pod.Spec.Containers {
		for name, q := range c.Resources.Requests {
			if !strings.HasPrefix(string(name), "hugepages-") {
				continue
			}
			ps := strings.TrimPrefix(string(name), "hugepages-")
			page = ps
			qty.Add(q)
		}
	}
	return page, qty
}

func applyHugepages(vmi *virtv1.VirtualMachineInstance, pod *corev1.Pod) (string, error) {
	page, qty := podHugepageRequest(pod)
	if page == "" {
		return "", nil
	}
	sizes := map[string]struct{}{}
	for _, c := range pod.Spec.Containers {
		for name := range c.Resources.Requests {
			if strings.HasPrefix(string(name), "hugepages-") {
				sizes[strings.TrimPrefix(string(name), "hugepages-")] = struct{}{}
			}
		}
	}
	if len(sizes) > 1 {
		return "", fmt.Errorf("multiple hugepage sizes requested")
	}
	// KubeVirt hugepage-backs all guest RAM; request must equal Memory.Guest
	// or virt-launcher is invalid (64Mi request vs 512Mi guest).
	guest := resource.MustParse("0")
	if vmi.Spec.Domain.Memory != nil && vmi.Spec.Domain.Memory.Guest != nil {
		guest = vmi.Spec.Domain.Memory.Guest.DeepCopy()
	}
	if qty.Cmp(guest) < 0 {
		return "", nil
	}
	if vmi.Spec.Domain.Memory == nil {
		vmi.Spec.Domain.Memory = &virtv1.Memory{}
	}
	vmi.Spec.Domain.Memory.Hugepages = &virtv1.Hugepages{PageSize: page}
	if vmi.Spec.Domain.Resources.Requests == nil {
		vmi.Spec.Domain.Resources.Requests = corev1.ResourceList{}
	}
	if vmi.Spec.Domain.Resources.Limits == nil {
		vmi.Spec.Domain.Resources.Limits = corev1.ResourceList{}
	}
	name := corev1.ResourceName("hugepages-" + page)
	vmi.Spec.Domain.Resources.Requests[name] = guest
	vmi.Spec.Domain.Resources.Limits[name] = guest
	return page, nil
}

func applySRIOV(vmi *virtv1.VirtualMachineInstance, pod *corev1.Pod, cfg mpv1.SandboxConfig, tee string) error {
	count := int64(0)
	for _, c := range pod.Spec.Containers {
		for name, q := range c.Resources.Requests {
			if strings.HasPrefix(string(name), "intel.com/") {
				count += q.Value()
			}
		}
	}
	if count == 0 {
		return nil
	}
	if tee != sandbox.TEEOff {
		return fmt.Errorf("TEE + SR-IOV is not supported")
	}
	nad := ""
	if pod.Annotations != nil {
		nad = pod.Annotations[util.SRIOVNetworkAnnotation]
	}
	if nad == "" {
		nad = cfg.DefaultSRIOVNetwork
	}
	if nad == "" {
		return fmt.Errorf("SR-IOV requested but no NAD: set %s on the pod or sandbox.defaultSRIOVNetwork", util.SRIOVNetworkAnnotation)
	}
	for i := int64(0); i < count; i++ {
		name := fmt.Sprintf("%s-%d", sriovNetName, i)
		vmi.Spec.Domain.Devices.Interfaces = append(vmi.Spec.Domain.Devices.Interfaces, virtv1.Interface{
			Name:                   name,
			InterfaceBindingMethod: virtv1.InterfaceBindingMethod{SRIOV: &virtv1.InterfaceSRIOV{}},
		})
		vmi.Spec.Networks = append(vmi.Spec.Networks, virtv1.Network{
			Name: name,
			NetworkSource: virtv1.NetworkSource{
				Multus: &virtv1.MultusNetwork{NetworkName: nad},
			},
		})
	}
	return nil
}

func applyDRA(vmi *virtv1.VirtualMachineInstance, pod *corev1.Pod) error {
	if len(pod.Spec.ResourceClaims) == 0 {
		return nil
	}
	_ = vmi
	return fmt.Errorf("DRA resourceClaims require a KubeVirt build with GPUsWithDRA; this cluster API does not expose those fields")
}

func applyVolumes(vmi *virtv1.VirtualMachineInstance, pod *corev1.Pod, tee string) ([]Mount, []error) {
	var mounts []Mount
	var errs []error
	mountPaths := map[string][]corev1.VolumeMount{}
	for _, c := range pod.Spec.Containers {
		for _, m := range c.VolumeMounts {
			mountPaths[m.Name] = append(mountPaths[m.Name], m)
		}
		for _, d := range c.VolumeDevices {
			mountPaths[d.Name] = append(mountPaths[d.Name], corev1.VolumeMount{Name: d.Name, MountPath: d.DevicePath})
		}
	}
	for _, vol := range pod.Spec.Volumes {
		switch {
		case vol.PersistentVolumeClaim != nil:
			if tee != sandbox.TEEOff {
				// still attach as virtio-blk; RWX+TEE is rejected in checkConflicts when known
			}
			diskName := "vol-" + vol.Name
			vmi.Spec.Domain.Devices.Disks = append(vmi.Spec.Domain.Devices.Disks, virtv1.Disk{
				Name: diskName,
				DiskDevice: virtv1.DiskDevice{
					Disk: &virtv1.DiskTarget{Bus: virtv1.DiskBusVirtio},
				},
			})
			vmi.Spec.Volumes = append(vmi.Spec.Volumes, virtv1.Volume{
				Name: diskName,
				VolumeSource: virtv1.VolumeSource{
					PersistentVolumeClaim: &virtv1.PersistentVolumeClaimVolumeSource{
						PersistentVolumeClaimVolumeSource: corev1.PersistentVolumeClaimVolumeSource{
							ClaimName: vol.PersistentVolumeClaim.ClaimName,
							ReadOnly:  vol.PersistentVolumeClaim.ReadOnly,
						},
					},
				},
			})
			for _, m := range mountPaths[vol.Name] {
				mounts = append(mounts, Mount{
					VolumeName: vol.Name,
					GuestPath:  m.MountPath,
					Kind:       "virtio-blk",
					ReadOnly:   m.ReadOnly || vol.PersistentVolumeClaim.ReadOnly,
				})
			}
		case vol.EmptyDir != nil:
			for _, m := range mountPaths[vol.Name] {
				mounts = append(mounts, Mount{
					VolumeName: vol.Name,
					GuestPath:  m.MountPath,
					Kind:       "tmpfs",
				})
			}
		case vol.ConfigMap != nil || vol.Secret != nil || vol.Projected != nil:
			for _, m := range mountPaths[vol.Name] {
				mounts = append(mounts, Mount{
					VolumeName: vol.Name,
					GuestPath:  m.MountPath,
					Kind:       "files",
				})
			}
		}
	}
	return mounts, errs
}

// ApplyRWXFilesystem converts a previously attached virtio-blk PVC disk into virtiofs.
// Callers must have confirmed the PVC is RWX.
func ApplyRWXFilesystem(vmi *virtv1.VirtualMachineInstance, volumeName string) error {
	if vmi == nil {
		return fmt.Errorf("vmi is nil")
	}
	diskName := "vol-" + volumeName
	found := false
	disks := vmi.Spec.Domain.Devices.Disks[:0]
	for _, d := range vmi.Spec.Domain.Devices.Disks {
		if d.Name == diskName {
			found = true
			continue
		}
		disks = append(disks, d)
	}
	if !found {
		return fmt.Errorf("volume %s not attached as a disk", volumeName)
	}
	vmi.Spec.Domain.Devices.Disks = disks
	vmi.Spec.Domain.Devices.Filesystems = append(vmi.Spec.Domain.Devices.Filesystems, virtv1.Filesystem{
		Name:     diskName,
		Virtiofs: &virtv1.FilesystemVirtiofs{},
	})
	return nil
}
