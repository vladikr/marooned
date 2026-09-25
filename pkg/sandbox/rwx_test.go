package sandbox

import (
	"fmt"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestAccessModesRWX(t *testing.T) {
	if AccessModesRWX(nil) || AccessModesRWX([]corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}) {
		t.Fatal("RWO is not RWX")
	}
	if !AccessModesRWX([]corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce, corev1.ReadWriteMany}) {
		t.Fatal("mixed modes with RWX")
	}
}

func TestRejectRWXVolumesEphemeral(t *testing.T) {
	pod := &corev1.Pod{Spec: corev1.PodSpec{
		Volumes: []corev1.Volume{{
			Name: "share",
			VolumeSource: corev1.VolumeSource{
				Ephemeral: &corev1.EphemeralVolumeSource{
					VolumeClaimTemplate: &corev1.PersistentVolumeClaimTemplate{
						Spec: corev1.PersistentVolumeClaimSpec{
							AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteMany},
						},
					},
				},
			},
		}},
	}}
	err := RejectRWXVolumes(pod, nil)
	if !IsRWXUnsupported(err) {
		t.Fatalf("got %v", err)
	}
}

func TestRejectRWXVolumesPVCLookup(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "app"},
		Spec: corev1.PodSpec{
			Volumes: []corev1.Volume{{
				Name: "data",
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "rwx"},
				},
			}},
		},
	}
	lookup := func(ns, claim string) ([]corev1.PersistentVolumeAccessMode, error) {
		if ns != "app" || claim != "rwx" {
			t.Fatalf("lookup %s/%s", ns, claim)
		}
		return []corev1.PersistentVolumeAccessMode{corev1.ReadWriteMany}, nil
	}
	if err := RejectRWXVolumes(pod, lookup); !IsRWXUnsupported(err) {
		t.Fatalf("got %v", err)
	}
	rwo := func(string, string) ([]corev1.PersistentVolumeAccessMode, error) {
		return []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}, nil
	}
	if err := RejectRWXVolumes(pod, rwo); err != nil {
		t.Fatal(err)
	}
}

func TestRejectRWXVolumesLookupError(t *testing.T) {
	pod := &corev1.Pod{Spec: corev1.PodSpec{
		Volumes: []corev1.Volume{{
			Name: "data",
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "missing"},
			},
		}},
	}}
	want := fmt.Errorf("not found")
	err := RejectRWXVolumes(pod, func(string, string) ([]corev1.PersistentVolumeAccessMode, error) {
		return nil, want
	})
	if err != want {
		t.Fatalf("got %v", err)
	}
	if IsRWXUnsupported(err) {
		t.Fatal("lookup miss is not RWX")
	}
}
