package sandbox

import (
	"os"
	"path/filepath"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"maroonedpods.io/maroonedpods/pkg/util"
)

func TestDiskMountPath(t *testing.T) {
	if got := DiskMountPath("data"); got != "/run/marooned/disks/data" {
		t.Fatalf("got %s", got)
	}
	if got := DiskMountPath(""); got != DiskMountRoot {
		t.Fatalf("empty name %s", got)
	}
}

func TestVMIDiskVolumeName(t *testing.T) {
	if got := VMIDiskVolumeName("data"); got != "vol-data" {
		t.Fatalf("got %s", got)
	}
	if got := PodVolumeFromVMIDisk("vol-data"); got != "data" {
		t.Fatalf("reverse %s", got)
	}
	if got := PodVolumeFromVMIDisk("user-rootfs"); got != "user-rootfs" {
		t.Fatalf("other %s", got)
	}
}

func TestFindClaimVolumePathCSI(t *testing.T) {
	root := t.TempDir()
	mount := filepath.Join(root, "uid-1", "volumes", "kubernetes.io~csi", "data", "mount")
	if err := os.MkdirAll(mount, 0755); err != nil {
		t.Fatal(err)
	}
	got := FindClaimVolumePath(root, "uid-1", "data")
	if got != mount {
		t.Fatalf("got %s want %s", got, mount)
	}
}

func TestFindClaimVolumePathLocal(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "uid-1", "volumes", "kubernetes.io~local-volume", "data")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	got := FindClaimVolumePath(root, "uid-1", "data")
	if got != dir {
		t.Fatalf("got %s want %s", got, dir)
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

func TestRestoreVolumesDropsDummyMountForBlockDevice(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "p",
		},
		Spec: corev1.PodSpec{
			Volumes: []corev1.Volume{{
				Name: "data",
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "blk"},
				},
			}},
			Containers: []corev1.Container{{
				Name: "box",
				VolumeMounts: []corev1.VolumeMount{{
					Name:      "data",
					MountPath: DiskMountPath("data"),
				}},
			}},
		},
	}
	raw, err := EncodeVolumeSnapshot(VolumeSnapshot{
		Volumes: pod.Spec.Volumes,
		Mounts: []ContainerMounts{{
			Container:     "box",
			VolumeDevices: []corev1.VolumeDevice{{Name: "data", DevicePath: "/dev/xvda"}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	pod.Annotations = map[string]string{util.VolumesAnnotation: raw}
	got := RestoreVolumes(pod)
	if len(got.Spec.Containers[0].VolumeMounts) != 0 {
		t.Fatalf("dummy mount leaked: %+v", got.Spec.Containers[0].VolumeMounts)
	}
	devs := got.Spec.Containers[0].VolumeDevices
	if len(devs) != 1 || devs[0].DevicePath != "/dev/xvda" {
		t.Fatalf("devicePath: %+v", devs)
	}
}
