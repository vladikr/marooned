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
	if len(vmi.Spec.Networks) != 0 || len(vmi.Spec.Domain.Devices.Interfaces) != 0 {
		t.Fatal("standalone VMI must drop the pod NIC (no virt-handler masquerade)")
	}
}

func TestRenderNilPod(t *testing.T) {
	if _, err := Render(nil, sandbox.EffectiveSandbox(nil)); err == nil {
		t.Fatal("expected error")
	}
}
