package sandbox

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func TestDiskMountPath(t *testing.T) {
	if got := DiskMountPath("data"); got != "/run/marooned/disks/data" {
		t.Fatalf("got %s", got)
	}
	if got := DiskMountPath(""); got != DiskMountRoot {
		t.Fatalf("empty name %s", got)
	}
}

func TestDropFromLivePod(t *testing.T) {
	empty := corev1.Volume{Name: "tmp", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}}
	pvc := corev1.Volume{Name: "data", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "pvc"}}}
	if !DropFromLivePod(empty) {
		t.Fatal("emptyDir must drop from the live Pod")
	}
	if DropFromLivePod(pvc) {
		t.Fatal("PVC must stay on the live Pod")
	}
}
