package sandbox

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAppendImageVolumesJSON(t *testing.T) {
	in := []byte(`{
		"spec": {
			"containers": [{"name":"box","image":"busybox"}],
			"volumes": [{"name":"data","persistentVolumeClaim":{"claimName":"pvc"}}]
		}
	}`)
	out, err := AppendImageVolumesJSON(in, "registry:5000/marooned-sandbox:latest", "registry:5000/marooned-kernel:latest")
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, want := range []string{
		`"name":"rootdisk"`,
		`"name":"kernel-boot-volume"`,
		`"reference":"registry:5000/marooned-sandbox:latest"`,
		`"reference":"registry:5000/marooned-kernel:latest"`,
		`"mountPath":"/run/marooned/image-volume/rootdisk"`,
		`"pullPolicy":"IfNotPresent"`,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %s in %s", want, s)
		}
	}
	if !strings.Contains(s, `"claimName":"pvc"`) {
		t.Fatal("dropped existing volume")
	}

	again, err := AppendImageVolumesJSON(out, "registry:5000/marooned-sandbox:latest", "registry:5000/marooned-kernel:latest")
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]interface{}
	if err := json.Unmarshal(again, &obj); err != nil {
		t.Fatal(err)
	}
	spec := obj["spec"].(map[string]interface{})
	vols := spec["volumes"].([]interface{})
	nRoot, nKernel := 0, 0
	for _, v := range vols {
		m := v.(map[string]interface{})
		switch m["name"] {
		case ImageVolumeDiskName:
			nRoot++
		case ImageVolumeKernelName:
			nKernel++
		}
	}
	if nRoot != 1 || nKernel != 1 {
		t.Fatalf("duplicate image volumes root=%d kernel=%d", nRoot, nKernel)
	}
}

func TestKubeletImageVolumePath(t *testing.T) {
	got := KubeletImageVolumePath("uid-1", ImageVolumeDiskName)
	want := "/var/lib/kubelet/pods/uid-1/volumes/kubernetes.io~image/rootdisk"
	if got != want {
		t.Fatalf("path %s", got)
	}
}
