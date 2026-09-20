package sandbox

// RuntimeClass overhead is the virt-launcher tax plus guest kernel/agent,
// not guest RAM. Guest RAM is the user's memory request on the Pod (and
// translated onto the VMI). 32Mi was a dummy that made the user Pod look
// cheap while the node still paid for qemu.
const (
	RuntimeClassOverheadCPU    = "100m"
	RuntimeClassOverheadMemory = "256Mi"
)
