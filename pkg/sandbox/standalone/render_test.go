package standalone

import (
	"encoding/json"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"

	virtv1 "kubevirt.io/api/core/v1"
	"maroonedpods.io/maroonedpods/pkg/sandbox"
	"maroonedpods.io/maroonedpods/pkg/util"
)

func TestRenderStandaloneVMIJSON(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "box",
			Namespace: "app",
			UID:       "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		},
		Spec: corev1.PodSpec{
			RuntimeClassName: pointer.String(util.RuntimeClassName),
			NodeName:         "worker-1",
			Containers: []corev1.Container{{
				Name:  "box",
				Image: "busybox",
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("1"),
						corev1.ResourceMemory: resource.MustParse("512Mi"),
					},
				},
			}},
		},
	}
	res, err := Render(pod, sandbox.EffectiveSandbox(nil))
	if err != nil {
		t.Fatal(err)
	}
	if res.VMI.Name != "marooned-aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee" {
		t.Fatalf("name %s", res.VMI.Name)
	}
	if res.VMI.Namespace != "app" {
		t.Fatalf("ns %s", res.VMI.Namespace)
	}
	if res.VMI.UID != pod.UID {
		t.Fatalf("uid %s want pod uid", res.VMI.UID)
	}
	var vmi virtv1.VirtualMachineInstance
	if err := json.Unmarshal(res.JSON, &vmi); err != nil {
		t.Fatal(err)
	}
	if vmi.Spec.Domain.Memory == nil || vmi.Spec.Domain.Memory.Guest == nil {
		t.Fatal("guest memory missing in STANDALONE_VMI JSON")
	}
	if vmi.UID != pod.UID {
		t.Fatalf("json uid %s", vmi.UID)
	}
	if vmi.Status.VSOCKCID == nil || *vmi.Status.VSOCKCID < 3 {
		t.Fatal("STANDALONE_VMI must stamp VSOCKCID (>=3)")
	}
	if len(vmi.Spec.Networks) != 1 || vmi.Spec.Networks[0].Pod == nil {
		t.Fatalf("pod network %+v", vmi.Spec.Networks)
	}
	if len(vmi.Spec.Domain.Devices.Interfaces) != 1 || vmi.Spec.Domain.Devices.Interfaces[0].Bridge == nil {
		t.Fatalf("bridge iface %+v", vmi.Spec.Domain.Devices.Interfaces)
	}
	if vmi.Spec.Domain.Devices.Interfaces[0].Masquerade != nil {
		t.Fatal("masquerade needs virt-handler")
	}
	if vmi.Spec.Domain.Devices.Interfaces[0].Binding != nil {
		t.Fatal("l2bridge plugin needs OVN-K")
	}
	if vmi.Spec.Domain.Machine == nil || vmi.Spec.Domain.Machine.Type != standaloneMachineType {
		t.Fatalf("machine %v want %s (skip q35 two-pass DomainDefineXML)", vmi.Spec.Domain.Machine, standaloneMachineType)
	}
	if !vmi.Spec.Domain.Devices.DisableHotplug {
		t.Fatal("DisableHotplug")
	}
	if vmi.Spec.Domain.Firmware == nil || vmi.Spec.Domain.Firmware.UUID != pod.UID {
		t.Fatalf("firmware uuid %v want pod uid", vmi.Spec.Domain.Firmware)
	}
	if vmi.Spec.Domain.Firmware.KernelBoot == nil {
		t.Fatal("kernelBoot must stay when stamping firmware UUID")
	}
}

func TestRenderNilPod(t *testing.T) {
	if _, err := Render(nil, sandbox.EffectiveSandbox(nil)); err == nil {
		t.Fatal("expected error")
	}
}

func TestRenderPVCHostDiskCapacity(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "box", Namespace: "app", UID: "uid-1"},
		Spec: corev1.PodSpec{
			RuntimeClassName: pointer.String(util.RuntimeClassName),
			Containers: []corev1.Container{{
				Name:  "box",
				Image: "busybox",
				VolumeMounts: []corev1.VolumeMount{{
					Name: "data", MountPath: "/data",
				}},
			}},
			Volumes: []corev1.Volume{{
				Name: "data",
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "isolated-data"},
				},
			}},
		},
	}
	res, err := RenderWithClaimBytes(pod, sandbox.EffectiveSandbox(nil), map[string]int64{"data": 1 << 30})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, vol := range res.VMI.Spec.Volumes {
		if vol.Name != "vol-data" {
			continue
		}
		found = true
		if vol.PersistentVolumeClaim != nil {
			t.Fatal("standalone VMI must not keep PVC (virt-launcher is not a Pod)")
		}
		if vol.HostDisk == nil || vol.HostDisk.Type != virtv1.HostDiskExistsOrCreate {
			t.Fatalf("hostDisk %+v", vol.HostDisk)
		}
		if vol.HostDisk.Path != "/var/run/kubevirt-private/vmi-disks/vol-data/disk.img" {
			t.Fatalf("path %s", vol.HostDisk.Path)
		}
		if vol.HostDisk.Capacity.Value() != 1<<30 {
			t.Fatalf("capacity %s", vol.HostDisk.Capacity.String())
		}
	}
	if !found {
		t.Fatal("missing vol-data")
	}
}
