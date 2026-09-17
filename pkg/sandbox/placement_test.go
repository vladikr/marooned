package sandbox

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"

	"maroonedpods.io/maroonedpods/pkg/util"
)

func TestNeedsUserNamespaceFromPVC(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "app"},
		Spec: corev1.PodSpec{
			Volumes: []corev1.Volume{{
				Name: "data",
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "pvc"},
				},
			}},
		},
	}
	if !NeedsUserNamespace(pod) {
		t.Fatal("PVC pod must use user namespace")
	}
	if PlacementForPod(pod) != util.PlacementUser {
		t.Fatal("placement")
	}
}

func TestNeedsUserNamespaceEphemeral(t *testing.T) {
	pod := &corev1.Pod{Spec: corev1.PodSpec{Volumes: []corev1.Volume{{
		Name:         "eph",
		VolumeSource: corev1.VolumeSource{Ephemeral: &corev1.EphemeralVolumeSource{}},
	}}}}
	if !NeedsUserNamespace(pod) {
		t.Fatal("ephemeral CSI is namespaced")
	}
}

func TestNeedsUserNamespaceEmptyDirStillUserNS(t *testing.T) {
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "app"}}
	if !NeedsUserNamespace(pod) {
		t.Fatal("v1 always places the VMI next to the Pod")
	}
	if PlacementForPod(pod) != util.PlacementUser {
		t.Fatal("placement")
	}
}

func TestPlanVMI(t *testing.T) {
	pvc := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "isolated-pvc",
			Namespace:   "app",
			UID:         "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
			Annotations: map[string]string{util.PlacementAnnotation: util.PlacementUser},
		},
	}
	plan := PlanVMI(pvc, util.DefaultInfraNamespace)
	if plan.Namespace != "app" || plan.ClaimPool || !plan.OwnerPod {
		t.Fatalf("%+v", plan)
	}
	if plan.Name != "marooned-aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee" {
		t.Fatalf("name %s", plan.Name)
	}

	diskless := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "box",
			Namespace: "app",
			UID:  "bbbbbbbb-bbbb-cccc-dddd-eeeeeeeeeeee",
		},
		Spec: corev1.PodSpec{RuntimeClassName: pointer.String(util.RuntimeClassName)},
	}
	plan = PlanVMI(diskless, util.DefaultInfraNamespace)
	if plan.Namespace != "app" || plan.ClaimPool || !plan.OwnerPod {
		t.Fatalf("diskless must also be in-ns: %+v", plan)
	}
}

func TestWrongNamespace(t *testing.T) {
	plan := VMIPlan{Namespace: "app", OwnerPod: true}
	if !WrongNamespace(util.DefaultInfraNamespace, plan) {
		t.Fatal("infra VMI is wrong for a PVC pod")
	}
	if WrongNamespace("app", plan) {
		t.Fatal("same ns is fine")
	}
}
