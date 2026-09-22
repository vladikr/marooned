package sandbox

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"maroonedpods.io/maroonedpods/pkg/util"
)

func TestGuestMountsEmptyDirFromSnapshot(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "p"},
		Spec: corev1.PodSpec{
			Volumes: []corev1.Volume{{
				Name:         "scratch",
				VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
			}},
			Containers: []corev1.Container{{
				Name:         "box",
				VolumeMounts: []corev1.VolumeMount{{Name: "scratch", MountPath: "/scratch"}},
			}},
		},
	}
	raw, err := EncodeVolumeSnapshot(CaptureStrippedVolumes(pod))
	if err != nil {
		t.Fatal(err)
	}
	stripped := pod.DeepCopy()
	stripped.Spec.Volumes = nil
	stripped.Spec.Containers[0].VolumeMounts = nil
	stripped.Annotations = map[string]string{util.VolumesAnnotation: raw}

	got := GuestMounts(stripped)
	if len(got) != 1 || got[0].Kind != "tmpfs" || got[0].GuestPath != "/scratch" {
		t.Fatalf("%+v", got)
	}
}

func TestGuestMountsLiveEmptyDir(t *testing.T) {
	pod := &corev1.Pod{Spec: corev1.PodSpec{
		Volumes: []corev1.Volume{{
			Name:         "tmp",
			VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
		}},
		Containers: []corev1.Container{{
			VolumeMounts: []corev1.VolumeMount{{Name: "tmp", MountPath: "/data"}},
		}},
	}}
	got := GuestMounts(pod)
	if len(got) != 1 || got[0].Kind != "tmpfs" || got[0].GuestPath != "/data" {
		t.Fatalf("%+v", got)
	}
}

func TestDiskSerial(t *testing.T) {
	if DiskSerial("data") != "vdata" {
		t.Fatalf("%q", DiskSerial("data"))
	}
	if DiskSerial("data_vol") != "vdatavol" {
		t.Fatalf("%q", DiskSerial("data_vol"))
	}
}

func TestGuestMountsPVCSerial(t *testing.T) {
	pod := &corev1.Pod{Spec: corev1.PodSpec{
		Volumes: []corev1.Volume{{
			Name: "data",
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "mypvc"},
			},
		}},
		Containers: []corev1.Container{{
			VolumeMounts: []corev1.VolumeMount{{Name: "data", MountPath: "/data"}},
		}},
	}}
	got := GuestMounts(pod)
	if len(got) != 1 || got[0].Kind != "virtio-blk" || got[0].Serial != "vdata" {
		t.Fatalf("%+v", got)
	}
}
