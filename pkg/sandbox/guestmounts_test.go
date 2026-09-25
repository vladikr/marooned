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

func TestGuestMountsSkipsRootfsVolume(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
			util.RootfsVolumeAnnotation: "root",
		}},
		Spec: corev1.PodSpec{
			Volumes: []corev1.Volume{
				{Name: "root", VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "img"},
				}},
				{Name: "scratch", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
			},
			Containers: []corev1.Container{{
				VolumeMounts: []corev1.VolumeMount{
					{Name: "root", MountPath: "/"},
					{Name: "scratch", MountPath: "/scratch"},
				},
			}},
		},
	}
	got := GuestMounts(pod)
	if len(got) != 1 || got[0].VolumeName != "scratch" {
		t.Fatalf("%+v", got)
	}
}

func TestGuestMountsForInitAndApp(t *testing.T) {
	pod := &corev1.Pod{Spec: corev1.PodSpec{
		Volumes: []corev1.Volume{{
			Name:         "shared",
			VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
		}},
		InitContainers: []corev1.Container{{
			Name:         "init",
			VolumeMounts: []corev1.VolumeMount{{Name: "shared", MountPath: "/shared"}},
		}},
		Containers: []corev1.Container{{
			Name:         "box",
			VolumeMounts: []corev1.VolumeMount{{Name: "shared", MountPath: "/shared"}},
		}},
	}}
	initM := GuestMountsFor(pod, "init")
	if len(initM) != 1 || initM[0].Kind != "tmpfs" || initM[0].GuestPath != "/shared" {
		t.Fatalf("init: %+v", initM)
	}
	app := GuestMountsFor(pod, "box")
	if len(app) != 1 || app[0].Kind != "tmpfs" {
		t.Fatalf("app: %+v", app)
	}
	if n := WorkloadCount(pod); n != 2 {
		t.Fatalf("workload count %d", n)
	}
}

func TestGuestMountsBlockDevice(t *testing.T) {
	pod := &corev1.Pod{Spec: corev1.PodSpec{
		Volumes: []corev1.Volume{{
			Name: "data",
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "blk"},
			},
		}},
		Containers: []corev1.Container{{
			VolumeDevices: []corev1.VolumeDevice{{Name: "data", DevicePath: "/dev/xvda"}},
		}},
	}}
	got := GuestMounts(pod)
	if len(got) != 1 || got[0].Kind != "block" || got[0].GuestPath != "/dev/xvda" || got[0].Serial != "vdata" {
		t.Fatalf("%+v", got)
	}
}

func TestGuestMountsBlockDeviceFromSnapshot(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "p"},
		Spec: corev1.PodSpec{
			Volumes: []corev1.Volume{{
				Name: "data",
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "blk"},
				},
			}},
			Containers: []corev1.Container{{
				Name:          "box",
				VolumeDevices: []corev1.VolumeDevice{{Name: "data", DevicePath: "/dev/xvda"}},
			}},
		},
	}
	raw, err := EncodeVolumeSnapshot(CaptureStrippedVolumes(pod))
	if err != nil {
		t.Fatal(err)
	}
	stripped := pod.DeepCopy()
	stripped.Spec.Volumes = nil
	stripped.Spec.Containers[0].VolumeDevices = nil
	stripped.Annotations = map[string]string{util.VolumesAnnotation: raw}

	got := GuestMounts(stripped)
	if len(got) != 1 || got[0].Kind != "block" || got[0].GuestPath != "/dev/xvda" {
		t.Fatalf("%+v", got)
	}
}
