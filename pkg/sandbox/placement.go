package sandbox

import (
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	"maroonedpods.io/maroonedpods/pkg/util"
)

// NeedsUserNamespace is always true in v1: every sandbox VMI lives next to
// the Pod. A separate marooned-system pool is a second shape we are not
// shipping yet.
func NeedsUserNamespace(pod *corev1.Pod) bool {
	return pod != nil
}

// PlacementForPod is always user. Infra placement is unused.
func PlacementForPod(pod *corev1.Pod) string {
	return util.PlacementUser
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

// PlanVMI always places the VMI in the Pod namespace as marooned-<pod-uid>.
func PlanVMI(pod *corev1.Pod, infraNamespace string) VMIPlan {
	_ = infraNamespace
	name := ""
	ns := ""
	if pod != nil {
		ns = pod.Namespace
		if pod.UID != "" {
			name = UserNamespaceVMIName(pod.UID)
		}
	}
	return VMIPlan{Namespace: ns, Name: name, ClaimPool: false, OwnerPod: true}
}

// WrongNamespace reports a pool/infra VMI that must be replaced by an in-ns VMI.
func WrongNamespace(vmiNamespace string, plan VMIPlan) bool {
	return plan.OwnerPod && vmiNamespace != "" && vmiNamespace != plan.Namespace
}
