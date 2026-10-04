package sandbox

import (
	"encoding/json"
	"path/filepath"

	"maroonedpods.io/maroonedpods/pkg/util"
)

const (
	// ImageVolumeDiskName is the Pod volume that kubelet unpacks as the
	// sandbox containerDisk image. Matches translate's rootdisk VMI volume.
	ImageVolumeDiskName = "rootdisk"
	// ImageVolumeKernelName matches kubevirt containerdisk.KernelBootVolumeName.
	ImageVolumeKernelName = "kernel-boot-volume"

	// ImageVolumeDiskMount is the dummy-container dest CRI-O uses for the
	// sandbox disk Image volume.
	ImageVolumeDiskMount = "/run/marooned/image-volume/rootdisk"
	// ImageVolumeKernelMount is the dummy-container dest for kernel-boot.
	ImageVolumeKernelMount = "/run/marooned/image-volume/kernel-boot"

	kubeletPodsDir        = "/var/lib/kubelet/pods"
	kubeletImagePluginDir = "kubernetes.io~image"

	// VirtImageVolumeDir is virt-launcher --image-volume disk root.
	VirtImageVolumeDir = "/var/run/kubevirt-image-volume"
	// VirtKernelBootVolumeDir is virt-launcher --image-volume kernel root.
	VirtKernelBootVolumeDir = "/var/run/kubevirt-kernel-boot"
)

// KubeletImageVolumePath is where kubelet unpacks a Pod Image volume.
func KubeletImageVolumePath(podUID, volumeName string) string {
	return filepath.Join(kubeletPodsDir, podUID, "volumes", kubeletImagePluginDir, volumeName)
}

// ImageVolumeDiskOverlay is virt-launcher's view of containerDisk index 0.
func ImageVolumeDiskOverlay(rootfs string) string {
	return filepath.Join(rootfs, "var/run/kubevirt-image-volume/disk_0")
}

// ImageVolumeKernelOverlay is virt-launcher's view of kernelBoot.
func ImageVolumeKernelOverlay(rootfs string) string {
	return filepath.Join(rootfs, "var/run/kubevirt-kernel-boot")
}

// AppendImageVolumesJSON adds k8s Image volumes for the sandbox disk and
// kernel images. Vendored k8s.io/api v0.27 has no VolumeSource.Image, so
// this is a JSON-level patch the apiserver (1.31+) understands.
func AppendImageVolumesJSON(podJSON []byte, diskImage, kernelImage string) ([]byte, error) {
	if diskImage == "" {
		diskImage = util.DefaultSandboxRootfsImage
	}
	if kernelImage == "" {
		kernelImage = util.DefaultSandboxKernelImage
	}
	var obj map[string]interface{}
	if err := json.Unmarshal(podJSON, &obj); err != nil {
		return nil, err
	}
	spec, _ := obj["spec"].(map[string]interface{})
	if spec == nil {
		spec = map[string]interface{}{}
		obj["spec"] = spec
	}
	vols, _ := spec["volumes"].([]interface{})
	vols = upsertImageVolume(vols, ImageVolumeDiskName, diskImage)
	vols = upsertImageVolume(vols, ImageVolumeKernelName, kernelImage)
	spec["volumes"] = vols
	for _, key := range []string{"containers", "initContainers"} {
		ctrs, ok := spec[key].([]interface{})
		if !ok {
			continue
		}
		for i, c := range ctrs {
			cm, ok := c.(map[string]interface{})
			if !ok {
				continue
			}
			mounts, _ := cm["volumeMounts"].([]interface{})
			mounts = upsertImageVolumeMount(mounts, ImageVolumeDiskName, ImageVolumeDiskMount)
			mounts = upsertImageVolumeMount(mounts, ImageVolumeKernelName, ImageVolumeKernelMount)
			cm["volumeMounts"] = mounts
			ctrs[i] = cm
		}
		spec[key] = ctrs
	}
	return json.Marshal(obj)
}

func upsertImageVolume(vols []interface{}, name, image string) []interface{} {
	body := map[string]interface{}{
		"name": name,
		"image": map[string]interface{}{
			"reference":  image,
			"pullPolicy": "IfNotPresent",
		},
	}
	for i, v := range vols {
		m, ok := v.(map[string]interface{})
		if !ok || m["name"] != name {
			continue
		}
		m["image"] = body["image"]
		vols[i] = m
		return vols
	}
	return append(vols, body)
}

func upsertImageVolumeMount(mounts []interface{}, name, path string) []interface{} {
	for _, m := range mounts {
		mm, ok := m.(map[string]interface{})
		if ok && mm["name"] == name {
			return mounts
		}
	}
	return append(mounts, map[string]interface{}{
		"name":      name,
		"mountPath": path,
		"readOnly":  true,
	})
}
