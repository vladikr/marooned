package adaptor

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"maroonedpods.io/maroonedpods/pkg/sandbox"
	"maroonedpods.io/maroonedpods/pkg/sandbox/translate"
	"maroonedpods.io/maroonedpods/pkg/util"
)

func TestPVCPodVMINamespaceIsPodNamespace(t *testing.T) {
	pod := pvcSandboxPod()
	cfg := sandbox.EffectiveSandbox(nil)
	plan := sandbox.PlanVMI(pod, cfg.InfraNamespace)
	tr := translate.Translate(translate.Input{
		Pod:       sandbox.RestoreVolumes(pod),
		Config:    cfg,
		Node:      "worker-1",
		Namespace: plan.Namespace,
		Name:      plan.Name,
		OwnerPod:  plan.OwnerPod,
	})
	if len(tr.Errors) != 0 {
		t.Fatal(tr.Errors)
	}
	if tr.VMI.Namespace != pod.Namespace {
		t.Fatalf("VMI.namespace=%s want %s", tr.VMI.Namespace, pod.Namespace)
	}
	if plan.ClaimPool {
		t.Fatal("PVC pod must never claim the infra pool")
	}
}

func TestDisklessVMINamespaceIsPodNamespace(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "box",
			Namespace: "app",
			UID:       "bbbbbbbb-bbbb-cccc-dddd-eeeeeeeeeeee",
		},
		Spec: corev1.PodSpec{
			NodeName: "worker-1",
			Containers: []corev1.Container{{
				Name: "box",
			}},
		},
	}
	cfg := sandbox.EffectiveSandbox(nil)
	plan := sandbox.PlanVMI(pod, cfg.InfraNamespace)
	tr := translate.Translate(translate.Input{
		Pod:       pod,
		Config:    cfg,
		Node:      "worker-1",
		Namespace: plan.Namespace,
		Name:      plan.Name,
		OwnerPod:  plan.OwnerPod,
	})
	if tr.VMI.Namespace != pod.Namespace {
		t.Fatalf("VMI.namespace=%s want %s", tr.VMI.Namespace, pod.Namespace)
	}
	if plan.ClaimPool {
		t.Fatal("v1 must not claim an infra pool")
	}
}

func pvcSandboxPod() *corev1.Pod {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "isolated-pvc",
			Namespace: "app",
			UID:       "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		},
		Spec: corev1.PodSpec{
			NodeName: "worker-1",
			Volumes: []corev1.Volume{{
				Name: "data",
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "data"},
				},
			}},
			Containers: []corev1.Container{{
				Name:         "box",
				VolumeMounts: []corev1.VolumeMount{{Name: "data", MountPath: "/data"}},
			}},
		},
	}
	raw, err := sandbox.EncodeVolumeSnapshot(sandbox.CaptureStrippedVolumes(pod))
	if err != nil {
		panic(err)
	}
	pod.Annotations = map[string]string{
		util.VolumesAnnotation:   raw,
		util.PlacementAnnotation: util.PlacementUser,
	}
	pod.Spec.Volumes = nil
	pod.Spec.Containers[0].VolumeMounts = nil
	return pod
}
