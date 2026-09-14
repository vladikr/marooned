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

func TestNeedsUserNamespaceEmptyDirIsInfra(t *testing.T) {
	pod := &corev1.Pod{Spec: corev1.PodSpec{Volumes: []corev1.Volume{{
		Name:         "tmp",
		VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
	}}}}
	if NeedsUserNamespace(pod) {
		t.Fatal("emptyDir is guest tmpfs")
	}
}

func TestNeedsUserNamespaceRespectsPlacementAnnotation(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{util.PlacementAnnotation: util.PlacementUser},
		},
	}
	if !NeedsUserNamespace(pod) {
		t.Fatal("placement annotation must win")
	}
	pod.Annotations[util.PlacementAnnotation] = util.PlacementInfra
	pod.Spec.Volumes = []corev1.Volume{{
		Name:         "data",
		VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "pvc"}},
	}}
	if NeedsUserNamespace(pod) {
		t.Fatal("explicit infra placement must not be re-derived from a stripped or leftover spec")
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
			Name:        "box",
			Namespace:   "app",
			Annotations: map[string]string{util.PlacementAnnotation: util.PlacementInfra},
		},
		Spec: corev1.PodSpec{RuntimeClassName: pointer.String(util.RuntimeClassName)},
	}
	plan = PlanVMI(diskless, util.DefaultInfraNamespace)
	if plan.Namespace != util.DefaultInfraNamespace || !plan.ClaimPool || plan.OwnerPod {
		t.Fatalf("%+v", plan)
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
