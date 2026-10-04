package main

import (
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	dhcpCookie0 = 99
	dhcpCookie1 = 130
	dhcpCookie2 = 83
	dhcpCookie3 = 99
	dhcpHdrLen  = 240
	dhcpMinPkt  = 300

	dhcpDiscover = 1
	dhcpOffer    = 2
	dhcpRequest  = 3
	dhcpAck      = 5

	dhcpOptMsgType  = 53
	dhcpOptServerID = 54
	dhcpOptLease    = 51
	dhcpOptMask     = 1
	dhcpOptRouter   = 3
	dhcpOptDNS      = 6
	dhcpOptEnd      = 255
)

func startPodDHCP(netns string, link podLink, dir string) {
	if netns == "" || link.IP == nil || link.CIDR == "" {
		return
	}
	killLeftoverDHCP(netns)
	self, err := os.Executable()
	if err != nil {
		self = os.Args[0]
	}
	gw := ""
	if link.GW != nil {
		gw = link.GW.String()
	}
	args := []string{"pod-dhcp", link.CIDR, gw, strings.TrimSuffix(podBridgeFake, "/32")}
	var cmd *exec.Cmd
	if netns != "" {
		cmd = exec.Command("nsenter", append([]string{"--net=" + netns, "--", self}, args...)...)
	} else {
		cmd = exec.Command(self, args...)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if dir != "" {
		logf, err := os.OpenFile(filepath.Join(dir, "pod-dhcp.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err == nil {
			cmd.Stdout = logf
			cmd.Stderr = logf
		}
	}
	if err := cmd.Start(); err != nil {
		if dir != "" {
			_ = os.WriteFile(filepath.Join(dir, "pod-dhcp.err"), []byte(err.Error()+"\n"), 0644)
		}
		return
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Release()
	_ = os.WriteFile(dhcpPIDFile(netns), []byte(strconv.Itoa(pid)+"\n"), 0644)
	if dir != "" {
		_ = os.WriteFile(filepath.Join(dir, "pod-dhcp.pid"), []byte(strconv.Itoa(pid)+"\n"), 0644)
	}
}

func dhcpPIDFile(netns string) string {
	return "/run/marooned-oci-dhcp-" + filepath.Base(netns) + ".pid"
}

func killLeftoverDHCP(netns string) {
	b, err := os.ReadFile(dhcpPIDFile(netns))
	if err != nil {
		return
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 1 {
		return
	}
	killPID(pid)
	time.Sleep(50 * time.Millisecond)
}

func runPodDHCP(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: marooned-oci pod-dhcp <cidr> [gw] [server]")
		return 1
	}
	cidr := args[0]
	ip, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	yiaddr := ip.To4()
	if yiaddr == nil {
		fmt.Fprintln(os.Stderr, "need ipv4 cidr")
		return 1
	}
	mask := net.IP(ipnet.Mask).To4()
	var gw net.IP
	if len(args) > 1 && args[1] != "" {
		gw = net.ParseIP(args[1]).To4()
	}
	server := net.ParseIP(strings.TrimSuffix(podBridgeFake, "/32")).To4()
	if len(args) > 2 && args[2] != "" {
		if s := net.ParseIP(args[2]).To4(); s != nil {
			server = s
		}
	}
	pc, err := listenDHCP(podBridgeName)
	if err != nil {
		fmt.Fprintln(os.Stderr, "listen", podBridgeName, err)
		pc, err = listenDHCP("")
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "listen:", err)
		return 1
	}
	defer pc.Close()
	buf := make([]byte, 1500)
	for {
		n, _, err := pc.ReadFrom(buf)
		if err != nil {
			continue
		}
		reply := dhcpReply(buf[:n], yiaddr, mask, gw, server)
		if reply == nil {
			continue
		}
		_, _ = pc.WriteTo(reply, &net.UDPAddr{IP: net.IPv4bcast, Port: 68})
	}
}

func listenDHCP(dev string) (net.PacketConn, error) {
	fd, err := syscall.Socket(syscall.AF_INET, syscall.SOCK_DGRAM, syscall.IPPROTO_UDP)
	if err != nil {
		return nil, err
	}
	if err := syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1); err != nil {
		syscall.Close(fd)
		return nil, err
	}
	if err := syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_BROADCAST, 1); err != nil {
		syscall.Close(fd)
		return nil, err
	}
	if dev != "" {
		if err := syscall.SetsockoptString(fd, syscall.SOL_SOCKET, syscall.SO_BINDTODEVICE, dev); err != nil {
			syscall.Close(fd)
			return nil, err
		}
	}
	if err := syscall.Bind(fd, &syscall.SockaddrInet4{Port: 67}); err != nil {
		syscall.Close(fd)
		return nil, err
	}
	file := os.NewFile(uintptr(fd), "dhcp")
	pc, err := net.FilePacketConn(file)
	_ = file.Close()
	return pc, err
}

func dhcpMsgType(pkt []byte) byte {
	if len(pkt) < dhcpHdrLen+4 {
		return 0
	}
	if pkt[dhcpHdrLen] != dhcpCookie0 || pkt[dhcpHdrLen+1] != dhcpCookie1 || pkt[dhcpHdrLen+2] != dhcpCookie2 || pkt[dhcpHdrLen+3] != dhcpCookie3 {
		return 0
	}
	i := dhcpHdrLen + 4
	for i+1 < len(pkt) {
		code := pkt[i]
		if code == dhcpOptEnd {
			return 0
		}
		if code == 0 {
			i++
			continue
		}
		if i+1 >= len(pkt) {
			return 0
		}
		n := int(pkt[i+1])
		if i+2+n > len(pkt) {
			return 0
		}
		if code == dhcpOptMsgType && n >= 1 {
			return pkt[i+2]
		}
		i += 2 + n
	}
	return 0
}

func dhcpReply(req []byte, yiaddr, mask, gw, server net.IP) []byte {
	if len(req) < dhcpHdrLen {
		return nil
	}
	if req[0] != 1 {
		return nil
	}
	mt := dhcpMsgType(req)
	var respType byte
	switch mt {
	case dhcpDiscover:
		respType = dhcpOffer
	case dhcpRequest:
		respType = dhcpAck
	default:
		return nil
	}
	var opts []byte
	opts = appendDHCPOpt(opts, dhcpOptMsgType, []byte{respType})
	if s := server.To4(); s != nil {
		opts = appendDHCPOpt(opts, dhcpOptServerID, s)
	}
	lease := make([]byte, 4)
	binary.BigEndian.PutUint32(lease, 3600)
	opts = appendDHCPOpt(opts, dhcpOptLease, lease)
	if m := mask.To4(); m != nil {
		opts = appendDHCPOpt(opts, dhcpOptMask, m)
	}
	if g := gw.To4(); g != nil {
		opts = appendDHCPOpt(opts, dhcpOptRouter, g)
	}
	opts = append(opts, dhcpOptEnd)
	need := dhcpHdrLen + 4 + len(opts)
	if need < dhcpMinPkt {
		need = dhcpMinPkt
	}
	out := make([]byte, need)
	copy(out, req[:dhcpHdrLen])
	out[0] = 2
	copy(out[16:20], yiaddr.To4())
	if s := server.To4(); s != nil {
		copy(out[20:24], s)
	}
	out[dhcpHdrLen] = dhcpCookie0
	out[dhcpHdrLen+1] = dhcpCookie1
	out[dhcpHdrLen+2] = dhcpCookie2
	out[dhcpHdrLen+3] = dhcpCookie3
	copy(out[dhcpHdrLen+4:], opts)
	return out
}

func appendDHCPOpt(b []byte, code byte, val []byte) []byte {
	b = append(b, code, byte(len(val)))
	return append(b, val...)
}
