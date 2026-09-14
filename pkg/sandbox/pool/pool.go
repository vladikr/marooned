package pool

import (
	"fmt"
	"math/rand"

	virtv1 "kubevirt.io/api/core/v1"

	"maroonedpods.io/maroonedpods/pkg/util"
)

// Key identifies a warm-pool bucket. SNP/TDX/off never share a bucket.
type Key struct {
	Node      string
	SizeClass string
	TEE       string
}

func (k Key) String() string {
	return k.Node + "/" + k.SizeClass + "/" + k.TEE
}

// Candidate is an available VMI that may be claimed.
type Candidate struct {
	VMI *virtv1.VirtualMachineInstance
}

// Index groups VMIs by pool key and state.
func Index(vmis []*virtv1.VirtualMachineInstance) map[Key]map[string][]*virtv1.VirtualMachineInstance {
	out := map[Key]map[string][]*virtv1.VirtualMachineInstance{}
	for _, vmi := range vmis {
		if vmi.Labels == nil {
			continue
		}
		if vmi.Labels[util.SandboxModeLabel] != util.SandboxModeSandbox {
			continue
		}
		state := vmi.Labels[util.WarmPoolStateLabel]
		if state == "" {
			continue
		}
		key := Key{
			Node:      vmi.Labels[util.SandboxNodeLabel],
			SizeClass: vmi.Labels[util.SandboxSizeClassLabel],
			TEE:       vmi.Labels[util.SandboxTEELabel],
		}
		if out[key] == nil {
			out[key] = map[string][]*virtv1.VirtualMachineInstance{}
		}
		out[key][state] = append(out[key][state], vmi)
	}
	return out
}

// SelectAvailable returns a running available VMI for the key, or nil.
func SelectAvailable(vmis []*virtv1.VirtualMachineInstance, key Key) *virtv1.VirtualMachineInstance {
	for _, vmi := range vmis {
		if vmi.Labels == nil {
			continue
		}
		if vmi.Labels[util.SandboxModeLabel] != util.SandboxModeSandbox {
			continue
		}
		if vmi.Labels[util.WarmPoolStateLabel] != util.PoolStateAvailable {
			continue
		}
		if vmi.Labels[util.SandboxNodeLabel] != key.Node {
			continue
		}
		if vmi.Labels[util.SandboxSizeClassLabel] != key.SizeClass {
			continue
		}
		if teeOf(vmi) != key.TEE {
			continue
		}
		if vmi.Status.Phase != virtv1.Running {
			continue
		}
		return vmi
	}
	return nil
}

func teeOf(vmi *virtv1.VirtualMachineInstance) string {
	if vmi.Labels == nil {
		return "off"
	}
	if v := vmi.Labels[util.SandboxTEELabel]; v != "" {
		return v
	}
	return "off"
}

// MarkClaimed mutates a copy for an atomic update. Caller must persist with resourceVersion.
func MarkClaimed(vmi *virtv1.VirtualMachineInstance, claimedBy, sandboxID string) *virtv1.VirtualMachineInstance {
	out := vmi.DeepCopy()
	if out.Labels == nil {
		out.Labels = map[string]string{}
	}
	out.Labels[util.WarmPoolStateLabel] = util.PoolStateClaimed
	out.Labels[util.WarmPoolClaimedByLabel] = claimedBy
	if sandboxID != "" {
		out.Labels[util.SandboxIDLabel] = sandboxID
	}
	return out
}

// MarkAvailable returns a VMI to the pool.
func MarkAvailable(vmi *virtv1.VirtualMachineInstance) *virtv1.VirtualMachineInstance {
	out := vmi.DeepCopy()
	if out.Labels == nil {
		out.Labels = map[string]string{}
	}
	out.Labels[util.WarmPoolStateLabel] = util.PoolStateAvailable
	delete(out.Labels, util.WarmPoolClaimedByLabel)
	delete(out.Labels, util.SandboxIDLabel)
	out.OwnerReferences = nil
	return out
}

// Deficit is how many pool VMIs to create for a key.
func Deficit(creating, available, desired int32) int32 {
	have := creating + available
	if have >= desired {
		return 0
	}
	return desired - have
}

// GenerateName returns a unique pool VMI name.
func GenerateName() string {
	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	suffix := make([]byte, 8)
	for i := range suffix {
		suffix[i] = charset[rand.Intn(len(charset))]
	}
	return fmt.Sprintf("%s%s", util.SandboxPoolVMNamePrefix, string(suffix))
}
