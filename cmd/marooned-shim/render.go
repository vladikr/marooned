package main

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"

	"maroonedpods.io/maroonedpods/pkg/cri"
	generatedclient "maroonedpods.io/maroonedpods/pkg/generated/maroonedpods/clientset/versioned"
	"maroonedpods.io/maroonedpods/pkg/sandbox"
	"maroonedpods.io/maroonedpods/pkg/sandbox/standalone"
	mpv1 "maroonedpods.io/maroonedpods/staging/src/maroonedpods.io/api/pkg/apis/core/v1alpha1"
)

func renderStandaloneVMI(kube *kubernetes.Clientset, mp generatedclient.Interface) func(context.Context, *cri.RenderStandaloneVMIRequest) (*cri.RenderStandaloneVMIResponse, error) {
	return func(ctx context.Context, req *cri.RenderStandaloneVMIRequest) (*cri.RenderStandaloneVMIResponse, error) {
		if req == nil || req.PodNamespace == "" || req.PodName == "" {
			return nil, fmt.Errorf("podName and podNamespace required")
		}
		pod, err := kube.CoreV1().Pods(req.PodNamespace).Get(ctx, req.PodName, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		if req.Node != "" && pod.Spec.NodeName == "" {
			pod.Spec.NodeName = req.Node
		}
		cfg := sandbox.EffectiveSandbox(getMaroonedPodsConfig(ctx, mp))
		res, err := standalone.RenderWithClaimBytes(pod, cfg, claimBytes(ctx, kube, pod))
		if err != nil {
			return nil, err
		}
		klog.Infof("render STANDALONE_VMI %s/%s (%d bytes)", res.VMI.Namespace, res.VMI.Name, len(res.JSON))
		return &cri.RenderStandaloneVMIResponse{
			VMI:       res.JSON,
			Name:      res.VMI.Name,
			Namespace: res.VMI.Namespace,
			UID:       string(res.VMI.UID),
		}, nil
	}
}

func claimBytes(ctx context.Context, kube *kubernetes.Clientset, pod *corev1.Pod) map[string]int64 {
	if kube == nil || pod == nil {
		return nil
	}
	src := sandbox.RestoreVolumes(pod)
	out := map[string]int64{}
	for _, vol := range src.Spec.Volumes {
		if vol.PersistentVolumeClaim == nil || vol.PersistentVolumeClaim.ClaimName == "" {
			continue
		}
		pvc, err := kube.CoreV1().PersistentVolumeClaims(pod.Namespace).Get(ctx, vol.PersistentVolumeClaim.ClaimName, metav1.GetOptions{})
		if err != nil {
			klog.Infof("PVC %s/%s: %v", pod.Namespace, vol.PersistentVolumeClaim.ClaimName, err)
			continue
		}
		q, ok := pvc.Spec.Resources.Requests[corev1.ResourceStorage]
		if !ok {
			continue
		}
		out[vol.Name] = q.Value()
	}
	return out
}

func getMaroonedPodsConfig(ctx context.Context, mp generatedclient.Interface) *mpv1.MaroonedPodsConfig {
	if mp == nil {
		return nil
	}
	cfg, err := mp.MaroonedpodsV1alpha1().MaroonedPodsConfigs().Get(ctx, "default", metav1.GetOptions{})
	if err != nil {
		klog.V(2).Infof("MaroonedPodsConfig default: %v", err)
		return nil
	}
	return cfg
}
