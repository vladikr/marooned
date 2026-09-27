package main

import (
	"testing"
)

func TestParseImageRef(t *testing.T) {
	cases := []struct {
		in, reg, repo, tag string
	}{
		{"busybox", "docker.io", "library/busybox", "latest"},
		{"nginx:alpine", "docker.io", "library/nginx", "alpine"},
		{"public.ecr.aws/docker/library/nginx:alpine", "public.ecr.aws", "docker/library/nginx", "alpine"},
		{"quay.io/prometheus/busybox:latest", "quay.io", "prometheus/busybox", "latest"},
		{"localhost:5000/marooned-sandbox:latest", "localhost:5000", "marooned-sandbox", "latest"},
	}
	for _, c := range cases {
		got, err := parseImageRef(c.in)
		if err != nil {
			t.Fatalf("%s: %v", c.in, err)
		}
		if got.Registry != c.reg || got.Repository != c.repo || got.Tag != c.tag {
			t.Fatalf("%s: %+v", c.in, got)
		}
	}
}

func TestPickLinuxAmd64(t *testing.T) {
	man := map[string]interface{}{
		"manifests": []interface{}{
			map[string]interface{}{
				"digest":   "sha256:arm",
				"platform": map[string]interface{}{"os": "linux", "architecture": "arm64"},
			},
			map[string]interface{}{
				"digest":   "sha256:amd",
				"platform": map[string]interface{}{"os": "linux", "architecture": "amd64"},
			},
		},
	}
	if pickLinuxAmd64(man) != "sha256:amd" {
		t.Fatal(pickLinuxAmd64(man))
	}
}
