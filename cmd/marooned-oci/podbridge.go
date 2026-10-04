package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	podUplink      = "eth0"
	podUplinkNIC   = "eth0-nic"
	podBridgeName  = "k6t-eth0"
	podTapName     = "tap0"
	podBridgeFake  = "169.254.75.10/32"
	dhcpCacheName  = "vif-cache-eth0.json"
	defaultQemuUID = 107
)

// ipInNS runs `ip` in the pod netns (host `ip`, nsenter --net).
var ipInNS = func(netns string, args ...string) (string, error) {
	cmd := exec.Command("ip", args...)
	if netns != "" {
		cmd = exec.Command("nsenter", append([]string{"--net=" + netns, "--", "ip"}, args...)...)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("ip %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func sysctlInNS(netns string, args ...string) {
	cmd := exec.Command("sysctl", args...)
	if netns != "" {
		cmd = exec.Command("nsenter", append([]string{"--net=" + netns, "--", "sysctl"}, args...)...)
	}
	_ = cmd.Run()
}

func ethtoolTXOff(netns, iface string) {
	cmd := exec.Command("ethtool", "-K", iface, "tx", "off")
	if netns != "" {
		cmd = exec.Command("nsenter", append([]string{"--net=" + netns, "--", "ethtool", "-K", iface, "tx", "off"})...)
	}
	_ = cmd.Run()
}

type podLink struct {
	Name   string
	MAC    string
	MTU    int
	CIDR   string // e.g. 10.244.0.109/24
	IP     net.IP
	Prefix int
	GW     net.IP
}

// setupPodBridge is virt-handler phase 1 of pod-network bridge binding,
// run in the user Pod netns before virt-launcher starts:
//
//	eth0 → eth0-nic (bridge port, no IP)
//	k6t-eth0 (bridge, 169.254.75.10/32 for in-pod DHCP)
//	tap0 on the bridge (qemu)
//	dummy eth0 keeps the original MAC/IP for CNI CHECK
//
// DHCP cache is written where virt-launcher reads it
// (/var/run/kubevirt-private/vif-cache-eth0.json). Returns the original
// MAC so STANDALONE_VMI can stamp interfaces[0].macAddress.
func setupPodBridge(netns, rootfs, logf string) (string, error) {
	if netns == "" {
		return "", fmt.Errorf("no pod netns")
	}
	logBridge(logf, "netns %s", netns)
	if linkExists(netns, podBridgeName) {
		mac, _, _ := readLink(netns, podUplink)
		logBridge(logf, "already bridged mac=%s", mac)
		if link, err := discoverLink(netns); err == nil {
			if err := writeDHCPCache(rootfs, link); err != nil {
				logBridge(logf, "dhcp cache: %v", err)
			}
		}
		return mac, nil
	}
	link, err := discoverLink(netns)
	if err != nil {
		return "", err
	}
	if err := applyPodBridge(netns, rootfs, link); err != nil {
		return "", err
	}
	logBridge(logf, "bridged %s mac=%s cidr=%s gw=%s mtu=%d", link.Name, link.MAC, link.CIDR, link.GW, link.MTU)
	return link.MAC, nil
}

func discoverLink(netns string) (podLink, error) {
	var out podLink
	out.Name = podUplink
	mac, mtu, err := readLink(netns, out.Name)
	if err != nil {
		return out, err
	}
	out.MAC, out.MTU = mac, mtu
	cidr, ip, prefix, err := readIPv4(netns, out.Name)
	if err != nil {
		return out, err
	}
	out.CIDR, out.IP, out.Prefix = cidr, ip, prefix
	out.GW = defaultRouteGW(netns)
	return out, nil
}

func applyPodBridge(netns, rootfs string, link podLink) error {
	uplinkNIC := podUplinkNIC
	uid, gid := qemuIDs(rootfs)
	if _, err := ipInNS(netns, "link", "add", podBridgeName, "type", "bridge"); err != nil {
		return err
	}
	_, _ = ipInNS(netns, "link", "set", podBridgeName, "up")
	if _, err := ipInNS(netns, "addr", "add", podBridgeFake, "dev", podBridgeName); err != nil {
		logBridge("", "bridge fake ip: %v", err)
	}
	_, _ = ipInNS(netns, "link", "set", link.Name, "down")
	_, _ = ipInNS(netns, "addr", "flush", "dev", link.Name)
	if _, err := ipInNS(netns, "link", "set", link.Name, "name", uplinkNIC); err != nil {
		return err
	}
	if _, err := ipInNS(netns, "link", "set", uplinkNIC, "master", podBridgeName); err != nil {
		return err
	}
	_, _ = ipInNS(netns, "link", "set", uplinkNIC, "type", "bridge_slave", "learning", "off")
	_, _ = ipInNS(netns, "link", "set", uplinkNIC, "up")
	if _, err := ipInNS(netns, "link", "add", link.Name, "type", "dummy"); err != nil {
		return err
	}
	if link.MAC != "" {
		_, _ = ipInNS(netns, "link", "set", link.Name, "address", link.MAC)
	}
	if link.MTU > 0 {
		_, _ = ipInNS(netns, "link", "set", link.Name, "mtu", strconv.Itoa(link.MTU))
	}
	if link.CIDR != "" {
		if _, err := ipInNS(netns, "addr", "add", link.CIDR, "dev", link.Name); err != nil {
			return err
		}
	}
	_, _ = ipInNS(netns, "link", "set", link.Name, "up")
	if link.GW != nil {
		_, _ = ipInNS(netns, "route", "replace", "default", "via", link.GW.String(), "dev", link.Name)
	}
	if err := addTap(netns, uid, gid, link.MTU); err != nil {
		return err
	}
	ethtoolTXOff(netns, podBridgeName)
	sysctlInNS(netns, "-w", "net.ipv4.conf.all.arp_ignore=1")
	sysctlInNS(netns, "-w", "net.bridge.bridge-nf-call-iptables=0")
	return writeDHCPCache(rootfs, link)
}

func addTap(netns string, uid, gid, mtu int) error {
	_, err := ipInNS(netns, "tuntap", "add", "dev", podTapName, "mode", "tap", "user", strconv.Itoa(uid), "group", strconv.Itoa(gid))
	if err != nil {
		if _, err2 := ipInNS(netns, "tuntap", "add", "dev", podTapName, "mode", "tap"); err2 != nil {
			return err
		}
	}
	if mtu > 0 {
		_, _ = ipInNS(netns, "link", "set", podTapName, "mtu", strconv.Itoa(mtu))
	}
	if _, err := ipInNS(netns, "link", "set", podTapName, "master", podBridgeName); err != nil {
		return err
	}
	_, err = ipInNS(netns, "link", "set", podTapName, "up")
	return err
}

func linkExists(netns, name string) bool {
	_, err := ipInNS(netns, "link", "show", "dev", name)
	return err == nil
}

func readLink(netns, name string) (mac string, mtu int, err error) {
	out, err := ipInNS(netns, "-o", "link", "show", "dev", name)
	if err != nil {
		return "", 0, err
	}
	return parseLinkMACMTU(out)
}

func readIPv4(netns, name string) (cidr string, ip net.IP, prefix int, err error) {
	out, err := ipInNS(netns, "-o", "-4", "addr", "show", "dev", name)
	if err != nil {
		return "", nil, 0, err
	}
	return parseAddrLine(out)
}

func defaultRouteGW(netns string) net.IP {
	out, err := ipInNS(netns, "-o", "-4", "route", "show", "default")
	if err != nil {
		return nil
	}
	gw, _ := parseDefaultRoute(out)
	return gw
}

func parseLinkMACMTU(out string) (mac string, mtu int, err error) {
	// 2: eth0: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1450 qdisc ... \    link/ether aa:bb:cc:dd:ee:ff brd ff:ff:ff:ff:ff:ff
	fields := strings.Fields(out)
	for i, f := range fields {
		if f == "mtu" && i+1 < len(fields) {
			mtu, _ = strconv.Atoi(fields[i+1])
		}
		if f == "link/ether" && i+1 < len(fields) {
			mac = fields[i+1]
		}
	}
	if mac == "" {
		return "", mtu, fmt.Errorf("no mac in %q", strings.TrimSpace(out))
	}
	return mac, mtu, nil
}

func parseAddrLine(out string) (cidr string, ip net.IP, prefix int, err error) {
	// 2: eth0    inet 10.244.0.109/24 brd 10.244.0.255 scope global eth0
	fields := strings.Fields(out)
	for i, f := range fields {
		if f == "inet" && i+1 < len(fields) {
			cidr = fields[i+1]
			break
		}
	}
	if cidr == "" {
		return "", nil, 0, fmt.Errorf("no inet addr in %q", strings.TrimSpace(out))
	}
	ipn, n, perr := net.ParseCIDR(cidr)
	if perr != nil {
		return "", nil, 0, perr
	}
	ones, _ := n.Mask.Size()
	return cidr, ipn, ones, nil
}

func parseDefaultRoute(out string) (gw net.IP, dev string) {
	// default via 10.244.0.1 dev eth0 ...
	fields := strings.Fields(out)
	for i, f := range fields {
		if f == "via" && i+1 < len(fields) {
			gw = net.ParseIP(fields[i+1])
		}
		if f == "dev" && i+1 < len(fields) {
			dev = fields[i+1]
		}
	}
	return gw, dev
}

func qemuIDs(rootfs string) (int, int) {
	uid, gid := defaultQemuUID, defaultQemuUID
	if rootfs == "" {
		return uid, gid
	}
	b, err := os.ReadFile(filepath.Join(rootfs, "etc/passwd"))
	if err != nil {
		return uid, gid
	}
	for _, line := range strings.Split(string(b), "\n") {
		if !strings.HasPrefix(line, "qemu:") {
			continue
		}
		p := strings.Split(line, ":")
		if len(p) < 4 {
			break
		}
		if n, err := strconv.Atoi(p[2]); err == nil {
			uid = n
		}
		if n, err := strconv.Atoi(p[3]); err == nil {
			gid = n
		}
		break
	}
	return uid, gid
}

// dhcpAddrJSON matches github.com/vishvananda/netlink.Addr JSON
// (embedded *net.IPNet plus Label/Flags/Scope/…).
type dhcpAddrJSON struct {
	IP          net.IP     `json:"IP"`
	Mask        net.IPMask `json:"Mask"`
	Label       string     `json:"Label"`
	Flags       int        `json:"Flags"`
	Scope       int        `json:"Scope"`
	Peer        net.IP     `json:"Peer"`
	Broadcast   net.IP     `json:"Broadcast"`
	PreferedLft int        `json:"PreferedLft"`
	ValidLft    int        `json:"ValidLft"`
}

type dhcpConfigJSON struct {
	Name         string           `json:"Name"`
	IP           dhcpAddrJSON     `json:"IP"`
	MAC          net.HardwareAddr `json:"MAC"`
	Gateway      net.IP           `json:"Gateway"`
	IPAMDisabled bool             `json:"IPAMDisabled"`
}

func dhcpConfigFor(link podLink) dhcpConfigJSON {
	cfg := dhcpConfigJSON{
		Name:         podUplink,
		IPAMDisabled: link.CIDR == "",
	}
	if link.CIDR != "" {
		ip, n, err := net.ParseCIDR(link.CIDR)
		if err == nil {
			n.IP = ip
			cfg.IP = dhcpAddrJSON{IP: n.IP, Mask: n.Mask}
		}
	}
	if link.MAC != "" {
		if mac, err := net.ParseMAC(link.MAC); err == nil {
			cfg.MAC = mac
		}
	}
	cfg.Gateway = link.GW
	return cfg
}

func writeDHCPCache(rootfs string, link podLink) error {
	if rootfs == "" {
		return nil
	}
	dir := filepath.Join(rootfs, "run/kubevirt-private")
	if err := os.MkdirAll(dir, 0750); err != nil {
		return err
	}
	b, err := json.MarshalIndent(dhcpConfigFor(link), "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, dhcpCacheName), b, 0604)
}

func stampStandaloneMAC(spec, mac string) string {
	if spec == "" || mac == "" {
		return spec
	}
	var vmi map[string]interface{}
	if json.Unmarshal([]byte(spec), &vmi) != nil {
		return spec
	}
	specMap, _ := vmi["spec"].(map[string]interface{})
	if specMap == nil {
		return spec
	}
	domain, _ := specMap["domain"].(map[string]interface{})
	if domain == nil {
		return spec
	}
	devices, _ := domain["devices"].(map[string]interface{})
	if devices == nil {
		return spec
	}
	ifaces, _ := devices["interfaces"].([]interface{})
	if len(ifaces) == 0 {
		return spec
	}
	iface, _ := ifaces[0].(map[string]interface{})
	if iface == nil {
		return spec
	}
	iface["macAddress"] = mac
	ifaces[0] = iface
	devices["interfaces"] = ifaces
	b, err := json.Marshal(vmi)
	if err != nil {
		return spec
	}
	return string(b)
}

func stripStandaloneNIC(spec string) string {
	if spec == "" {
		return spec
	}
	var vmi map[string]interface{}
	if json.Unmarshal([]byte(spec), &vmi) != nil {
		return spec
	}
	specMap, _ := vmi["spec"].(map[string]interface{})
	if specMap == nil {
		return spec
	}
	specMap["networks"] = []interface{}{}
	domain, _ := specMap["domain"].(map[string]interface{})
	if domain != nil {
		devices, _ := domain["devices"].(map[string]interface{})
		if devices != nil {
			devices["interfaces"] = []interface{}{}
		}
	}
	b, err := json.Marshal(vmi)
	if err != nil {
		return spec
	}
	return string(b)
}

func logBridge(path, format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	if path == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return
	}
	_, _ = f.WriteString(msg + "\n")
	_ = f.Close()
}
