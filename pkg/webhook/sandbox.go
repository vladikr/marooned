package webhook

import (
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"

	"maroonedpods.io/maroonedpods/pkg/sandbox"
	"maroonedpods.io/maroonedpods/pkg/util"
)

const (
	resourceCPU              = corev1.ResourceCPU
	resourceMemory           = corev1.ResourceMemory
	resourceEphemeralStorage = corev1.ResourceEphemeralStorage
	hugepagesPrefix          = "hugepages-"
)

// IsSandboxPod reports whether the pod uses RuntimeClass marooned.
func IsSandboxPod(pod *corev1.Pod) bool {
	if pod == nil || pod.Spec.RuntimeClassName == nil {
		return false
	}
	return *pod.Spec.RuntimeClassName == util.RuntimeClassName
}

// MutateSandboxPod applies the sandbox admission rules in place:
// mode label, placement, persist the guest volume table.
// PVC/ephemeral stay on the Pod so kubelet attaches once; emptyDir is
// guest tmpfs and is dropped. Claim volumeDevices become dummy
// volumeMounts at DiskMountPath so a Filesystem SC can attach.
// CPU, memory, hugepages, extended devices, and resourceClaims stay
// so the scheduler/quota/DRA see them.
func MutateSandboxPod(pod *corev1.Pod) error {
	// one-pod has no hidden VMI. sandbox-cleanup was for the two-pod
	// adaptor; nothing strips it on delete, so kubectl delete stuck.
	if pod.DeletionTimestamp != nil && !pod.DeletionTimestamp.IsZero() {
		pod.Finalizers = stripNamedFinalizer(pod.Finalizers, util.SandboxFinalizer)
		return nil
	}
	if pod.Labels == nil {
		pod.Labels = map[string]string{}
	}
	pod.Labels[util.SandboxModeLabel] = util.SandboxModeSandbox

	if err := persistVolumeSnapshot(pod); err != nil {
		return err
	}

	keptVolumes, droppedVolumeNames := filterVolumes(pod.Spec.Volumes)
	pod.Spec.Volumes = keptVolumes

	for i := range pod.Spec.Containers {
		dropVolumeRefs(&pod.Spec.Containers[i], droppedVolumeNames)
		rewriteClaimMounts(&pod.Spec.Containers[i], keptVolumes)
		rewriteProbesToExec(&pod.Spec.Containers[i])
	}
	for i := range pod.Spec.InitContainers {
		dropVolumeRefs(&pod.Spec.InitContainers[i], droppedVolumeNames)
		rewriteClaimMounts(&pod.Spec.InitContainers[i], keptVolumes)
	}
	return nil
}

// HTTP/TCP probes hit status.podIP (the CRI-O pause). Rewrite them to
// exec wget against 127.0.0.1 so kubelet uses CRI exec in the guest.
func rewriteProbesToExec(c *corev1.Container) {
	rewriteProbeToExec(c.LivenessProbe)
	rewriteProbeToExec(c.ReadinessProbe)
	rewriteProbeToExec(c.StartupProbe)
}

func rewriteProbeToExec(p *corev1.Probe) {
	if p == nil {
		return
	}
	url := probeURL(p)
	if url == "" {
		return
	}
	p.Exec = &corev1.ExecAction{Command: []string{"wget", "-qO-", url}}
	p.HTTPGet = nil
	p.TCPSocket = nil
	p.GRPC = nil
	// CRI exec over vsock is slower than a local HTTP GET. The default
	// timeoutSeconds=1 surfaces as "exit code -1" / unknown readiness.
	if p.TimeoutSeconds <= 1 {
		p.TimeoutSeconds = 10
	}
	if p.InitialDelaySeconds < 5 {
		p.InitialDelaySeconds = 5
	}
}

func probeURL(p *corev1.Probe) string {
	if p.HTTPGet != nil {
		port := p.HTTPGet.Port.IntValue()
		if port <= 0 {
			return ""
		}
		path := p.HTTPGet.Path
		if path == "" {
			path = "/"
		}
		host := p.HTTPGet.Host
		if host == "" {
			host = "127.0.0.1"
		}
		scheme := strings.ToLower(string(p.HTTPGet.Scheme))
		if scheme == "" {
			scheme = "http"
		}
		return scheme + "://" + host + ":" + strconv.Itoa(port) + path
	}
	if p.TCPSocket != nil {
		port := p.TCPSocket.Port.IntValue()
		if port <= 0 {
			return ""
		}
		return "http://127.0.0.1:" + strconv.Itoa(port) + "/"
	}
	return ""
}

func persistVolumeSnapshot(pod *corev1.Pod) error {
	if pod.Annotations == nil {
		pod.Annotations = map[string]string{}
	}
	if _, ok := pod.Annotations[util.VolumesAnnotation]; !ok {
		raw, err := sandbox.EncodeVolumeSnapshot(sandbox.CaptureStrippedVolumes(pod))
		if err != nil {
			return err
		}
		pod.Annotations[util.VolumesAnnotation] = raw
	}
	if pod.Annotations[util.PlacementAnnotation] == "" {
		pod.Annotations[util.PlacementAnnotation] = sandbox.PlacementForPod(pod)
	}
	return nil
}

func hasFinalizer(finalizers []string, name string) bool {
	for _, f := range finalizers {
		if f == name {
			return true
		}
	}
	return false
}

func stripNamedFinalizer(finalizers []string, name string) []string {
	out := finalizers[:0]
	for _, f := range finalizers {
		if f != name {
			out = append(out, f)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func dropVolumeRefs(c *corev1.Container, droppedVolumeNames map[string]struct{}) {
	c.VolumeMounts = filterVolumeMounts(c.VolumeMounts, droppedVolumeNames)
	c.VolumeDevices = filterVolumeDevices(c.VolumeDevices, droppedVolumeNames)
}

func rewriteClaimMounts(c *corev1.Container, volumes []corev1.Volume) {
	claims := map[string]struct{}{}
	for _, vol := range volumes {
		if vol.PersistentVolumeClaim != nil || vol.Ephemeral != nil {
			claims[vol.Name] = struct{}{}
		}
	}
	mounted := map[string]struct{}{}
	for i, m := range c.VolumeMounts {
		if _, ok := claims[m.Name]; ok {
			c.VolumeMounts[i].MountPath = sandbox.DiskMountPath(m.Name)
		}
		mounted[c.VolumeMounts[i].Name] = struct{}{}
	}
	if len(c.VolumeDevices) == 0 {
		return
	}
	// Filesystem PVCs cannot keep live volumeDevices (kubelet rejects
	// volumeMode Filesystem + volumeDevices). Dummy volumeMounts let
	// kubelet NodePublish once at DiskMountPath; the snapshot keeps
	// devicePath for the guest. volumeMode Block CSI needs live
	// volumeDevices and is a follow-up.
	kept := make([]corev1.VolumeDevice, 0, len(c.VolumeDevices))
	for _, d := range c.VolumeDevices {
		if _, ok := claims[d.Name]; !ok {
			kept = append(kept, d)
			continue
		}
		if _, ok := mounted[d.Name]; ok {
			continue
		}
		c.VolumeMounts = append(c.VolumeMounts, corev1.VolumeMount{
			Name:      d.Name,
			MountPath: sandbox.DiskMountPath(d.Name),
		})
		mounted[d.Name] = struct{}{}
	}
	if len(kept) == 0 {
		c.VolumeDevices = nil
		return
	}
	c.VolumeDevices = kept
}

func isWorkloadVolume(vol corev1.Volume) bool {
	return sandbox.DropFromLivePod(vol)
}

func keepProjectedSA(vol corev1.Volume) bool {
	if vol.VolumeSource.Projected == nil {
		return true
	}
	return true
}

func filterVolumes(volumes []corev1.Volume) ([]corev1.Volume, map[string]struct{}) {
	dropped := map[string]struct{}{}
	kept := make([]corev1.Volume, 0, len(volumes))
	for _, vol := range volumes {
		if isWorkloadVolume(vol) {
			dropped[vol.Name] = struct{}{}
			continue
		}
		if !keepProjectedSA(vol) {
			dropped[vol.Name] = struct{}{}
			continue
		}
		kept = append(kept, vol)
	}
	return kept, dropped
}

func filterVolumeMounts(mounts []corev1.VolumeMount, dropped map[string]struct{}) []corev1.VolumeMount {
	if len(mounts) == 0 {
		return mounts
	}
	kept := make([]corev1.VolumeMount, 0, len(mounts))
	for _, m := range mounts {
		if _, drop := dropped[m.Name]; drop {
			continue
		}
		kept = append(kept, m)
	}
	return kept
}

func filterVolumeDevices(devices []corev1.VolumeDevice, dropped map[string]struct{}) []corev1.VolumeDevice {
	if len(devices) == 0 {
		return devices
	}
	kept := make([]corev1.VolumeDevice, 0, len(devices))
	for _, d := range devices {
		if _, drop := dropped[d.Name]; drop {
			continue
		}
		kept = append(kept, d)
	}
	return kept
}

// IsHugepageResource reports hugepages-* resource names.
func IsHugepageResource(name corev1.ResourceName) bool {
	return strings.HasPrefix(string(name), hugepagesPrefix)
}

// IsExtendedResource reports non-core compute resources that belong on the VMI.
func IsExtendedResource(name corev1.ResourceName) bool {
	if name == resourceCPU || name == resourceMemory || name == resourceEphemeralStorage {
		return false
	}
	if IsHugepageResource(name) {
		return true
	}
	return true
}
