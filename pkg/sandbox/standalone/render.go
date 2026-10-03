// Package standalone builds a VMI spec for virt-launcher STANDALONE_VMI.
// KubeVirt pkg/render (VEP-359 / kubevirt#19234) is not released yet;
// this uses marooned translate. Swap the JSON source when PodFromVMI is importable.
package standalone

import (
	"encoding/json"
	"fmt"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
	virtv1 "kubevirt.io/api/core/v1"

	"maroonedpods.io/maroonedpods/pkg/sandbox"
	"maroonedpods.io/maroonedpods/pkg/sandbox/translate"
	"maroonedpods.io/maroonedpods/pkg/sandbox/vsock"
	"maroonedpods.io/maroonedpods/pkg/util"
	mpv1 "maroonedpods.io/maroonedpods/staging/src/maroonedpods.io/api/pkg/apis/core/v1alpha1"
)

// prepareStandalone stamps identity virt-launcher needs and drops the pod
// NIC. Masquerade expects virt-handler to create k6t-eth0; without that
// SyncVMI fails. Agent traffic is vsock. Guest IP / Services come later.
func prepareStandalone(vmi *virtv1.VirtualMachineInstance, podUID string) {
	if vmi == nil {
		return
	}
	if podUID != "" && vmi.UID == "" {
		vmi.UID = k8stypes.UID(podUID)
	}
	cid := vsock.CIDForUID(podUID)
	vmi.Status.VSOCKCID = &cid
	vmi.Spec.Networks = nil
	vmi.Spec.Domain.Devices.Interfaces = nil
}

// Result is the in-memory VMI plus STANDALONE_VMI JSON.
type Result struct {
	VMI  *virtv1.VirtualMachineInstance
	JSON []byte
}

// Render translates a RuntimeClass=marooned Pod into virt-launcher STANDALONE_VMI JSON.
func Render(pod *corev1.Pod, cfg mpv1.SandboxConfig) (Result, error) {
	var out Result
	if pod == nil {
		return out, fmt.Errorf("pod is nil")
	}
	src := sandbox.RestoreVolumes(pod)
	name := sandbox.UserNamespaceVMIName(pod.UID)
	if pod.UID == "" {
		name = ""
	}
	tr := translate.Translate(translate.Input{
		Pod:         src,
		Config:      cfg,
		Node:        pod.Spec.NodeName,
		Namespace:   pod.Namespace,
		Name:        name,
		OwnerPod:    true,
		RootfsBytes: rootfsBytes(pod),
	})
	if len(tr.Errors) > 0 {
		return out, fmt.Errorf("translate: %v", tr.Errors)
	}
	if tr.VMI == nil {
		return out, fmt.Errorf("translate produced no VMI")
	}
	prepareStandalone(tr.VMI, string(pod.UID))
	b, err := json.Marshal(tr.VMI)
	if err != nil {
		return out, err
	}
	out.VMI = tr.VMI
	out.JSON = b
	return out, nil
}

func rootfsBytes(pod *corev1.Pod) int64 {
	if pod.Annotations == nil {
		return 0
	}
	raw := pod.Annotations[util.RootfsBytesAnnotation]
	if raw == "" {
		return 0
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0
	}
	return n
}
