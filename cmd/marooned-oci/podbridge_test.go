package main

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseLinkMACMTU(t *testing.T) {
	mac, mtu, err := parseLinkMACMTU(`2: eth0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1450 qdisc noqueue state UP \    link/ether 0a:58:0a:f4:00:6d brd ff:ff:ff:ff:ff:ff`)
	if err != nil {
		t.Fatal(err)
	}
	if mac != "0a:58:0a:f4:00:6d" {
		t.Fatalf("mac %s", mac)
	}
	if mtu != 1450 {
		t.Fatalf("mtu %d", mtu)
	}
}

func TestParseAddrLine(t *testing.T) {
	cidr, ip, prefix, err := parseAddrLine("2: eth0    inet 10.244.0.109/24 brd 10.244.0.255 scope global eth0\n")
	if err != nil {
		t.Fatal(err)
	}
	if cidr != "10.244.0.109/24" {
		t.Fatalf("cidr %s", cidr)
	}
	if ip.String() != "10.244.0.109" {
		t.Fatalf("ip %s", ip)
	}
	if prefix != 24 {
		t.Fatalf("prefix %d", prefix)
	}
}

func TestParseDefaultRoute(t *testing.T) {
	gw, dev := parseDefaultRoute("default via 10.244.0.1 dev eth0 proto dhcp src 10.244.0.109 metric 100\n")
	if gw.String() != "10.244.0.1" {
		t.Fatalf("gw %s", gw)
	}
	if dev != "eth0" {
		t.Fatalf("dev %s", dev)
	}
}

func TestDHCPConfigJSONRoundTrip(t *testing.T) {
	link := podLink{
		Name:   "eth0",
		MAC:    "0a:58:0a:f4:00:6d",
		MTU:    1450,
		CIDR:   "10.244.0.109/24",
		IP:     net.ParseIP("10.244.0.109"),
		Prefix: 24,
		GW:     net.ParseIP("10.244.0.1"),
	}
	b, err := json.Marshal(dhcpConfigFor(link))
	if err != nil {
		t.Fatal(err)
	}
	var got dhcpConfigJSON
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.IPAMDisabled {
		t.Fatal("IPAM disabled")
	}
	if got.IP.IP.String() != "10.244.0.109" {
		t.Fatalf("ip %s", got.IP.IP)
	}
	ones, _ := got.IP.Mask.Size()
	if ones != 24 {
		t.Fatalf("mask %d %v", ones, got.IP.Mask)
	}
	if got.MAC.String() != "0a:58:0a:f4:00:6d" {
		t.Fatalf("mac %s", got.MAC)
	}
	if got.Gateway.String() != "10.244.0.1" {
		t.Fatalf("gw %s", got.Gateway)
	}
	if strings.Contains(string(b), `"Peer"`) || strings.Contains(string(b), `"Broadcast"`) {
		t.Fatalf("empty Peer/Broadcast marshal as string: %s", b)
	}
	var kv struct {
		IP struct {
			IP   net.IP     `json:"IP"`
			Mask net.IPMask `json:"Mask"`
			Peer *net.IPNet `json:"Peer"`
		} `json:"IP"`
	}
	if err := json.Unmarshal(b, &kv); err != nil {
		t.Fatal(err)
	}
	if kv.IP.Peer != nil {
		t.Fatal("peer")
	}
}

func TestWriteDHCPCache(t *testing.T) {
	root := t.TempDir()
	link := podLink{
		MAC:  "aa:bb:cc:dd:ee:ff",
		CIDR: "10.244.0.5/24",
		GW:   net.ParseIP("10.244.0.1"),
	}
	if err := writeDHCPCache(root, link); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(root, "run/kubevirt-private", dhcpCacheName)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"IPAMDisabled": false`) {
		t.Fatalf("cache %s", b)
	}
}

func TestQemuIDs(t *testing.T) {
	root := t.TempDir()
	uid, gid := qemuIDs(root)
	if uid != defaultQemuUID || gid != defaultQemuUID {
		t.Fatalf("missing passwd %d %d", uid, gid)
	}
	if err := os.MkdirAll(filepath.Join(root, "etc"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "etc/passwd"), []byte("root:x:0:0:root:/root:/bin/bash\nqemu:x:107:107:qemu:/:/sbin/nologin\n"), 0644); err != nil {
		t.Fatal(err)
	}
	uid, gid = qemuIDs(root)
	if uid != 107 || gid != 107 {
		t.Fatalf("qemu %d:%d", uid, gid)
	}
}

func TestStampStandaloneMAC(t *testing.T) {
	in := `{"spec":{"domain":{"devices":{"interfaces":[{"name":"default","bridge":{}}]}}},"status":{"VSOCKCID":99}}`
	got := stampStandaloneMAC(in, "0a:58:0a:f4:00:6d")
	var v map[string]interface{}
	if err := json.Unmarshal([]byte(got), &v); err != nil {
		t.Fatal(err)
	}
	iface := v["spec"].(map[string]interface{})["domain"].(map[string]interface{})["devices"].(map[string]interface{})["interfaces"].([]interface{})[0].(map[string]interface{})
	if iface["macAddress"] != "0a:58:0a:f4:00:6d" {
		t.Fatalf("mac %v", iface["macAddress"])
	}
	if v["status"].(map[string]interface{})["VSOCKCID"].(float64) != 99 {
		t.Fatal("lost VSOCKCID")
	}
}

func TestStripStandaloneNIC(t *testing.T) {
	in := `{"spec":{"networks":[{"name":"default","pod":{}}],"domain":{"devices":{"interfaces":[{"name":"default","bridge":{}}]}}}}`
	got := stripStandaloneNIC(in)
	var v map[string]interface{}
	if err := json.Unmarshal([]byte(got), &v); err != nil {
		t.Fatal(err)
	}
	spec := v["spec"].(map[string]interface{})
	nets, _ := spec["networks"].([]interface{})
	if len(nets) != 0 {
		t.Fatalf("networks %v", nets)
	}
	ifaces := spec["domain"].(map[string]interface{})["devices"].(map[string]interface{})["interfaces"].([]interface{})
	if len(ifaces) != 0 {
		t.Fatalf("ifaces %v", ifaces)
	}
}

func TestSetupPodBridgeNoNetns(t *testing.T) {
	if _, err := setupPodBridge("", t.TempDir(), ""); err == nil {
		t.Fatal("expected error")
	}
}
