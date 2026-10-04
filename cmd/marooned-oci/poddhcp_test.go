package main

import (
	"net"
	"testing"
)

func dhcpDiscoverPkt(xid uint32, chaddr net.HardwareAddr) []byte {
	pkt := make([]byte, dhcpMinPkt)
	pkt[0] = 1
	pkt[1] = 1
	pkt[2] = 6
	pkt[4] = byte(xid >> 24)
	pkt[5] = byte(xid >> 16)
	pkt[6] = byte(xid >> 8)
	pkt[7] = byte(xid)
	copy(pkt[28:], chaddr)
	pkt[dhcpHdrLen] = dhcpCookie0
	pkt[dhcpHdrLen+1] = dhcpCookie1
	pkt[dhcpHdrLen+2] = dhcpCookie2
	pkt[dhcpHdrLen+3] = dhcpCookie3
	pkt[dhcpHdrLen+4] = dhcpOptMsgType
	pkt[dhcpHdrLen+5] = 1
	pkt[dhcpHdrLen+6] = dhcpDiscover
	pkt[dhcpHdrLen+7] = dhcpOptEnd
	return pkt
}

func TestDHCPOffer(t *testing.T) {
	mac, err := net.ParseMAC("0a:58:0a:f4:00:6d")
	if err != nil {
		t.Fatal(err)
	}
	req := dhcpDiscoverPkt(0x11223344, mac)
	yi := net.ParseIP("10.244.0.30").To4()
	mask := net.IP(net.CIDRMask(20, 32))
	gw := net.ParseIP("10.244.0.1").To4()
	srv := net.ParseIP("169.254.75.10").To4()
	offer := dhcpReply(req, yi, mask, gw, srv)
	if offer == nil {
		t.Fatal("nil offer")
	}
	if offer[0] != 2 {
		t.Fatalf("op %d", offer[0])
	}
	if net.IP(offer[16:20]).String() != "10.244.0.30" {
		t.Fatalf("yiaddr %v", offer[16:20])
	}
	if dhcpMsgType(offer) != dhcpOffer {
		t.Fatalf("type %d", dhcpMsgType(offer))
	}
	if dhcpMsgType(req) != dhcpDiscover {
		t.Fatalf("discover type %d", dhcpMsgType(req))
	}
}

func TestDHCPAck(t *testing.T) {
	req := dhcpDiscoverPkt(1, []byte{1, 2, 3, 4, 5, 6})
	req[dhcpHdrLen+6] = dhcpRequest
	ack := dhcpReply(req, net.ParseIP("10.244.0.5").To4(), net.IP(net.CIDRMask(24, 32)), net.ParseIP("10.244.0.1").To4(), net.ParseIP("169.254.75.10").To4())
	if dhcpMsgType(ack) != dhcpAck {
		t.Fatalf("type %d", dhcpMsgType(ack))
	}
}

func TestDHCPIgnoresGarbage(t *testing.T) {
	if dhcpReply(nil, nil, nil, nil, nil) != nil {
		t.Fatal("nil req")
	}
	if dhcpReply([]byte{2}, nil, nil, nil, nil) != nil {
		t.Fatal("short")
	}
	req := dhcpDiscoverPkt(1, []byte{1, 2, 3, 4, 5, 6})
	req[dhcpHdrLen+6] = 8
	if dhcpReply(req, net.ParseIP("10.0.0.1").To4(), net.IP(net.CIDRMask(24, 32)), nil, nil) != nil {
		t.Fatal("unknown type")
	}
}

func TestDHCPPIDFile(t *testing.T) {
	got := dhcpPIDFile("/var/run/netns/cni-abc")
	if got != "/run/marooned-oci-dhcp-cni-abc.pid" {
		t.Fatalf("got %s", got)
	}
}
