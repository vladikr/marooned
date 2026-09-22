package main

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"k8s.io/klog/v2"
)

type imageRef struct {
	Registry   string
	Repository string
	Tag        string
}

func parseImageRef(s string) (imageRef, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "docker://")
	if s == "" {
		return imageRef{}, fmt.Errorf("empty image")
	}
	tag := "latest"
	if i := strings.LastIndex(s, ":"); i > 0 && !strings.Contains(s[i:], "/") {
		tag = s[i+1:]
		s = s[:i]
	}
	reg, repo := "docker.io", s
	if slash := strings.Index(s, "/"); slash > 0 {
		head := s[:slash]
		if strings.Contains(head, ".") || strings.Contains(head, ":") || head == "localhost" {
			reg, repo = head, s[slash+1:]
		}
	}
	if reg == "docker.io" && !strings.Contains(repo, "/") {
		repo = "library/" + repo
	}
	return imageRef{Registry: reg, Repository: repo, Tag: tag}, nil
}

func (r imageRef) registryHost() string {
	if r.Registry == "docker.io" {
		return "registry-1.docker.io"
	}
	return r.Registry
}

func (r imageRef) baseURL() string {
	host := r.registryHost()
	if strings.HasPrefix(host, "127.") || strings.HasPrefix(host, "localhost") {
		return "http://" + host
	}
	return "https://" + host
}

func pullImage(dest, image string) error {
	return pullImageWithClient(&http.Client{Timeout: 3 * time.Minute}, dest, image)
}

func pullImageWithClient(hc *http.Client, dest, image string) error {
	ref, err := parseImageRef(image)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dest, 0755); err != nil {
		return err
	}
	token, err := registryToken(hc, ref)
	if err != nil {
		klog.Infof("guest-pull auth %s: %v", image, err)
	}
	man, media, err := fetchManifest(hc, ref, token)
	if err != nil {
		return err
	}
	if strings.Contains(media, "manifest.list") || strings.Contains(media, "image.index") {
		digest := pickLinuxAmd64(man)
		if digest == "" {
			return fmt.Errorf("no linux/amd64 manifest in %s", image)
		}
		man, _, err = fetchManifestDigest(hc, ref, token, digest)
		if err != nil {
			return err
		}
	}
	layers := layerDigests(man)
	if len(layers) == 0 {
		return fmt.Errorf("no layers in %s", image)
	}
	klog.Infof("guest-pull %s: %d layers", image, len(layers))
	for i, d := range layers {
		if err := fetchAndUnpackLayer(hc, ref, token, d, dest); err != nil {
			return fmt.Errorf("layer %d %s: %w", i, d[:12], err)
		}
	}
	return nil
}

func registryToken(hc *http.Client, ref imageRef) (string, error) {
	u := ref.baseURL() + "/v2/"
	req, _ := http.NewRequest(http.MethodGet, u, nil)
	resp, err := hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		return "", nil
	}
	h := resp.Header.Get("WWW-Authenticate")
	realm, service, scope := parseWWWAuth(h)
	if realm == "" {
		return "", fmt.Errorf("no bearer realm")
	}
	if scope == "" {
		scope = "repository:" + ref.Repository + ":pull"
	}
	tu := realm + "?service=" + service + "&scope=" + scope
	tresp, err := hc.Get(tu)
	if err != nil {
		return "", err
	}
	defer tresp.Body.Close()
	var tok struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	_ = json.NewDecoder(tresp.Body).Decode(&tok)
	if tok.Token != "" {
		return tok.Token, nil
	}
	return tok.AccessToken, nil
}

func parseWWWAuth(h string) (realm, service, scope string) {
	h = strings.TrimPrefix(h, "Bearer ")
	h = strings.TrimPrefix(h, "bearer ")
	for _, p := range strings.Split(h, ",") {
		p = strings.TrimSpace(p)
		k, v, ok := strings.Cut(p, "=")
		if !ok {
			continue
		}
		v = strings.Trim(v, `"`)
		switch k {
		case "realm":
			realm = v
		case "service":
			service = v
		case "scope":
			scope = v
		}
	}
	return realm, service, scope
}

func fetchManifest(hc *http.Client, ref imageRef, token string) (map[string]interface{}, string, error) {
	u := ref.baseURL() + "/v2/" + ref.Repository + "/manifests/" + ref.Tag
	return getManifest(hc, u, token)
}

func fetchManifestDigest(hc *http.Client, ref imageRef, token, digest string) (map[string]interface{}, string, error) {
	u := ref.baseURL() + "/v2/" + ref.Repository + "/manifests/" + digest
	return getManifest(hc, u, token)
}

func getManifest(hc *http.Client, u, token string) (map[string]interface{}, string, error) {
	req, _ := http.NewRequest(http.MethodGet, u, nil)
	req.Header.Set("Accept", strings.Join([]string{
		"application/vnd.oci.image.index.v1+json",
		"application/vnd.docker.distribution.manifest.list.v2+json",
		"application/vnd.oci.image.manifest.v1+json",
		"application/vnd.docker.distribution.manifest.v2+json",
	}, ", "))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, "", fmt.Errorf("%s: %s %s", u, resp.Status, b)
	}
	var man map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&man); err != nil {
		return nil, "", err
	}
	return man, resp.Header.Get("Content-Type"), nil
}

func pickLinuxAmd64(man map[string]interface{}) string {
	ms, _ := man["manifests"].([]interface{})
	for _, m := range ms {
		mm, _ := m.(map[string]interface{})
		plat, _ := mm["platform"].(map[string]interface{})
		if plat["os"] == "linux" && (plat["architecture"] == "amd64" || plat["architecture"] == "x86_64") {
			d, _ := mm["digest"].(string)
			return d
		}
	}
	return ""
}

func layerDigests(man map[string]interface{}) []string {
	var out []string
	layers, _ := man["layers"].([]interface{})
	for _, l := range layers {
		lm, _ := l.(map[string]interface{})
		d, _ := lm["digest"].(string)
		if d != "" {
			out = append(out, d)
		}
	}
	return out
}

func fetchAndUnpackLayer(hc *http.Client, ref imageRef, token, digest, dest string) error {
	u := ref.baseURL() + "/v2/" + ref.Repository + "/blobs/" + digest
	req, _ := http.NewRequest(http.MethodGet, u, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("blob %s: %s", digest, resp.Status)
	}
	br := bufio.NewReader(resp.Body)
	magic, _ := br.Peek(2)
	var r io.Reader = br
	if len(magic) >= 2 && magic[0] == 0x1f && magic[1] == 0x8b {
		gz, err := gzip.NewReader(br)
		if err != nil {
			return err
		}
		defer gz.Close()
		r = gz
	}
	return unpackTar(r, dest)
}
