package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"maroonedpods.io/maroonedpods/pkg/sandbox"
)

func TestVMIPVCNames(t *testing.T) {
	spec := `{
		"spec": {
			"volumes": [
				{"name": "rootdisk", "containerDisk": {"image": "x"}},
				{"name": "vol-data", "persistentVolumeClaim": {"claimName": "isolated-data"}},
				{"name": "scratch", "emptyDisk": {"capacity": "1Gi"}}
			]
		}
	}`
	got := vmiPVCNames(spec)
	if len(got) != 1 || got[0] != "vol-data" {
		t.Fatalf("got %v", got)
	}
	host := `{"spec":{"volumes":[{"name":"vol-data","hostDisk":{"path":"/var/run/kubevirt-private/vmi-disks/vol-data/disk.img","type":"DiskOrCreate","capacity":"1Gi"}}]}}`
	got = vmiPVCNames(host)
	if len(got) != 1 || got[0] != "vol-data" {
		t.Fatalf("hostDisk %v", got)
	}
	if vmiPVCNames("") != nil {
		t.Fatal("empty spec")
	}
}

func TestBundlePVCSources(t *testing.T) {
	dir := t.TempDir()
	cfg := []byte(`{
		"mounts": [
			{"destination": "/run/marooned/image-volume/rootdisk", "source": "/img"},
			{"destination": "/run/marooned/disks/data", "source": "/var/lib/kubelet/pods/uid/volumes/kubernetes.io~csi/data/mount"},
			{"destination": "/run/marooned/disks", "source": "/skip"}
		]
	}`)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), cfg, 0644); err != nil {
		t.Fatal(err)
	}
	got := bundlePVCSources(dir)
	if got["data"] != "/var/lib/kubelet/pods/uid/volumes/kubernetes.io~csi/data/mount" {
		t.Fatalf("got %#v", got)
	}
}

func TestPVCDiskBindsFromBundle(t *testing.T) {
	dir := t.TempDir()
	src := "/host/pvc/data"
	cfg := []byte(`{"mounts":[{"destination":"/run/marooned/disks/data","source":"` + src + `"}]}`)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), cfg, 0644); err != nil {
		t.Fatal(err)
	}
	spec := `{"spec":{"volumes":[{"name":"vol-data","persistentVolumeClaim":{"claimName":"isolated-data"}}]}}`
	binds := pvcDiskBinds("/overlay", "uid-1", dir, spec)
	if len(binds) != 1 {
		t.Fatalf("binds %d", len(binds))
	}
	if binds[0].src != src {
		t.Fatalf("src %s", binds[0].src)
	}
	wantDst := sandbox.VirtPVCDiskOverlay("/overlay", "vol-data")
	if binds[0].dst != wantDst {
		t.Fatalf("dst %s want %s", binds[0].dst, wantDst)
	}
}

func TestPVCDiskBindsSkipWhenNoPVC(t *testing.T) {
	spec := `{"spec":{"volumes":[{"name":"rootdisk","containerDisk":{"image":"x"}}]}}`
	if binds := pvcDiskBinds("/overlay", "uid", "", spec); len(binds) != 0 {
		t.Fatalf("got %v", binds)
	}
}

func TestEnsureDiskImg(t *testing.T) {
	dir := t.TempDir()
	const want int64 = 2 << 20
	if err := ensureDiskImg(dir, want); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, sandbox.DiskImgName)
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if st.Size() != want {
		t.Fatalf("size %d want %d (must not use node-disk statfs)", st.Size(), want)
	}
	if err := ensureDiskImg(dir, want); err != nil {
		t.Fatal(err)
	}
	st2, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if st2.Size() != st.Size() {
		t.Fatalf("rewrote existing disk.img %d -> %d", st.Size(), st2.Size())
	}
}

func TestVirtPVCDiskOverlay(t *testing.T) {
	got := sandbox.VirtPVCDiskOverlay("/overlay", "vol-data")
	if !strings.HasSuffix(got, "/var/run/kubevirt-private/vmi-disks/vol-data") {
		t.Fatalf("got %s", got)
	}
}
