package sandbox

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/utils/pointer"

	"maroonedpods.io/maroonedpods/pkg/util"
	mpv1 "maroonedpods.io/maroonedpods/staging/src/maroonedpods.io/api/pkg/apis/core/v1alpha1"
)

const (
	BindingL2Bridge    = "l2bridge"
	BindingMasquerade  = "masquerade"
	AgentListenVsock   = "vsock"
	AgentListenTCP     = "tcp"
	TEEOff             = "off"
	TEESNP             = "snp"
	TEETDX             = "tdx"
	TEEAnnotationValue = "annotation"
	DefaultAgentPort   = 1024
)

// EffectiveSandbox returns sandbox settings with v1 defaults applied.
func EffectiveSandbox(cfg *mpv1.MaroonedPodsConfig) mpv1.SandboxConfig {
	out := mpv1.SandboxConfig{
		InfraNamespace:      util.DefaultInfraNamespace,
		RootfsImage:         util.DefaultSandboxRootfsImage,
		AgentListen:         AgentListenVsock,
		WarmPoolSize:        0,
		PublishGuestIPOnPod: pointer.Bool(true),
		// alpine+agent rootfs has no bootloader; non-TEE guests need kernelBoot.
		KernelBoot: &mpv1.SandboxKernelBoot{
			Image:      util.DefaultSandboxKernelImage,
			KernelPath: util.DefaultSandboxKernelPath,
			InitrdPath: util.DefaultSandboxInitrdPath,
			KernelArgs: util.DefaultSandboxKernelArgs,
		},
		Network:    &mpv1.SandboxNetwork{Binding: BindingMasquerade},
		PoolSizeClasses: []mpv1.SandboxSizeClass{
			{Name: "s", GuestCPU: "1", GuestMemory: "512Mi"},
			{Name: "m", GuestCPU: "2", GuestMemory: "2Gi"},
		},
		ExtraGuestOverhead: &corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("50m"),
			corev1.ResourceMemory: resource.MustParse("64Mi"),
		},
		ConfidentialCompute: &mpv1.SandboxConfidentialCompute{
			Default:            TEEOff,
			RequireCapableNode: pointer.Bool(true),
		},
	}
	if cfg == nil || cfg.Spec.Sandbox == nil {
		return out
	}
	in := cfg.Spec.Sandbox
	if in.InfraNamespace != "" {
		out.InfraNamespace = in.InfraNamespace
	}
	if in.RootfsImage != "" {
		out.RootfsImage = in.RootfsImage
	}
	if in.AgentListen != "" {
		out.AgentListen = in.AgentListen
	}
	if in.WarmPoolSize > 0 {
		out.WarmPoolSize = in.WarmPoolSize
	}
	if in.PublishGuestIPOnPod != nil {
		out.PublishGuestIPOnPod = in.PublishGuestIPOnPod
	}
	if in.DefaultSRIOVNetwork != "" {
		out.DefaultSRIOVNetwork = in.DefaultSRIOVNetwork
	}
	if in.KernelBoot != nil && (in.KernelBoot.Image != "" || in.KernelBoot.KernelPath != "") {
		if out.KernelBoot == nil {
			out.KernelBoot = &mpv1.SandboxKernelBoot{}
		}
		if in.KernelBoot.Image != "" {
			out.KernelBoot.Image = in.KernelBoot.Image
		}
		if in.KernelBoot.KernelPath != "" {
			out.KernelBoot.KernelPath = in.KernelBoot.KernelPath
		}
		if in.KernelBoot.InitrdPath != "" {
			out.KernelBoot.InitrdPath = in.KernelBoot.InitrdPath
		}
		if in.KernelBoot.KernelArgs != "" {
			out.KernelBoot.KernelArgs = in.KernelBoot.KernelArgs
		}
	}
	if in.Network != nil && in.Network.Binding != "" {
		out.Network.Binding = in.Network.Binding
	}
	if len(in.PoolSizeClasses) > 0 {
		out.PoolSizeClasses = in.PoolSizeClasses
	}
	if in.ExtraGuestOverhead != nil {
		out.ExtraGuestOverhead = in.ExtraGuestOverhead
	}
	if in.ConfidentialCompute != nil {
		if in.ConfidentialCompute.Default != "" {
			out.ConfidentialCompute.Default = in.ConfidentialCompute.Default
		}
		if in.ConfidentialCompute.RequireCapableNode != nil {
			out.ConfidentialCompute.RequireCapableNode = in.ConfidentialCompute.RequireCapableNode
		}
		out.ConfidentialCompute.Trustee = in.ConfidentialCompute.Trustee
	}
	return out
}

// DefaultMode is always Sandbox in this repo. Node mode is not implemented here.
func DefaultMode(cfg *mpv1.MaroonedPodsConfig) mpv1.IsolationMode {
	return mpv1.IsolationModeSandbox
}
