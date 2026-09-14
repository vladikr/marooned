package sandbox

import (
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	"maroonedpods.io/maroonedpods/pkg/util"
)

// NeedsUserNamespace reports whether the sandbox VMI must live next to the Pod
// so CSI can attach the user's claim. Diskless pods stay in the infra pool.
func NeedsUserNamespace(pod *corev1.Pod) bool {
	if pod == nil {
		return false
	}
	if pod.Annotations != nil {
		switch pod.Annotations[util.PlacementAnnotation] {
		case util.PlacementUser:
			return true
		case util.PlacementInfra:
			return false
		}
	}
	for _, vol := range VolumesOf(pod) {
		if VolumeNeedsUserNamespace(vol) {
			return true
		}
	}
	return false
}

// PlacementForPod is user|infra from the volume list.
func PlacementForPod(pod *corev1.Pod) string {
	if NeedsUserNamespace(pod) {
		return util.PlacementUser
	}
	return util.PlacementInfra
}

// VMIPlan is where the adaptor creates or claims a sandbox VMI.
type VMIPlan struct {
	Namespace string
	Name      string
	ClaimPool bool
	OwnerPod  bool
}

// UserNamespaceVMIName is DNS-1123: marooned-<pod-uid>.
func UserNamespaceVMIName(uid types.UID) string {
	return util.UserNamespaceVMIPrefix + strings.ToLower(string(uid))
}

// PlanVMI decides namespace, name, and whether the infra warm pool may be used.
func PlanVMI(pod *corev1.Pod, infraNamespace string) VMIPlan {
	if infraNamespace == "" {
		infraNamespace = util.DefaultInfraNamespace
	}
	if NeedsUserNamespace(pod) {
		name := ""
		if pod != nil && pod.UID != "" {
			name = UserNamespaceVMIName(pod.UID)
		}
		ns := ""
		if pod != nil {
			ns = pod.Namespace
		}
		return VMIPlan{Namespace: ns, Name: name, ClaimPool: false, OwnerPod: true}
	}
	return VMIPlan{Namespace: infraNamespace, ClaimPool: true, OwnerPod: false}
}

// WrongNamespace reports a pool/infra VMI that must be replaced by an in-ns VMI.
func WrongNamespace(vmiNamespace string, plan VMIPlan) bool {
	return plan.OwnerPod && vmiNamespace != "" && vmiNamespace != plan.Namespace
}
