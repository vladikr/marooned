package sandbox

import (
	corev1 "k8s.io/api/core/v1"

	"maroonedpods.io/maroonedpods/pkg/util"
)

// WantsDedicatedCPU is true when the Pod asked for exclusive pCPUs:
// annotation maroonedpods.io/dedicated-cpu=true, or Guaranteed QoS with
// an integer CPU request. Annotation false always wins.
func WantsDedicatedCPU(pod *corev1.Pod) bool {
	if pod == nil {
		return false
	}
	if pod.Annotations != nil {
		switch pod.Annotations[util.DedicatedCPUAnnotation] {
		case "true", "True", "1":
			return true
		case "false", "False", "0":
			return false
		}
	}
	return IsGuaranteedQoS(pod) && IntegerCPUCores(pod) >= 1
}

// IsGuaranteedQoS matches kubelet: every container and init container has
// CPU and memory request equal to limit, both set and positive.
func IsGuaranteedQoS(pod *corev1.Pod) bool {
	if pod == nil {
		return false
	}
	cs := append([]corev1.Container{}, pod.Spec.Containers...)
	cs = append(cs, pod.Spec.InitContainers...)
	if len(cs) == 0 {
		return false
	}
	for _, c := range cs {
		if !equalRequestLimit(c.Resources, corev1.ResourceCPU) {
			return false
		}
		if !equalRequestLimit(c.Resources, corev1.ResourceMemory) {
			return false
		}
	}
	return true
}

func equalRequestLimit(rr corev1.ResourceRequirements, name corev1.ResourceName) bool {
	req, rok := rr.Requests[name]
	lim, lok := rr.Limits[name]
	return rok && lok && req.Sign() > 0 && req.Cmp(lim) == 0
}

// IntegerCPUCores is the pod CPU request in whole cores, or 0 if the
// request is missing or not an integer number of cores.
func IntegerCPUCores(pod *corev1.Pod) uint32 {
	if pod == nil {
		return 0
	}
	milli := int64(0)
	for _, c := range append(append([]corev1.Container{}, pod.Spec.Containers...), pod.Spec.InitContainers...) {
		if q, ok := c.Resources.Requests[corev1.ResourceCPU]; ok {
			milli += q.MilliValue()
		}
	}
	if milli < 1000 || milli%1000 != 0 {
		return 0
	}
	return uint32(milli / 1000)
}
