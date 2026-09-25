package sandbox

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"maroonedpods.io/maroonedpods/pkg/util"
)

func TestUserRootfsCapacity(t *testing.T) {
	got := UserRootfsCapacity(5 * 1024 * 1024) // busybox-sized
	if got.Value() != 256*1024*1024 {
		t.Fatalf("small image cap %d want 256Mi", got.Value())
	}
	got = UserRootfsCapacity(400 * 1024 * 1024)
	want := int64((400 + 128) * 1024 * 1024)
	if got.Value() != want {
		t.Fatalf("large image cap %d want %d", got.Value(), want)
	}
	got = UserRootfsCapacityN(5*1024*1024, 2)
	if got.Value() != 2*256*1024*1024 {
		t.Fatalf("two containers cap %d", got.Value())
	}
}

func TestRootfsPVCFromAnnotation(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
			util.RootfsVolumeAnnotation: "root",
		}},
		Spec: corev1.PodSpec{
			Volumes: []corev1.Volume{{
				Name: "root",
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "img"},
				},
			}},
		},
	}
	pvc := RootfsPVC(pod)
	if pvc == nil || pvc.ClaimName != "img" {
		t.Fatalf("%+v", pvc)
	}
	if RootfsPVC(&corev1.Pod{}) != nil {
		t.Fatal("empty")
	}
}
