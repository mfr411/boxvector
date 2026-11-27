//go:build linux

package web

import "testing"

func TestParsePacketV4TCP(t *testing.T) {
	f := make([]byte, 14+20+20)
	f[12], f[13] = 0x08, 0x00
	f[14] = 0x45
	f[14+9] = 6
	copy(f[14+12:], []byte{10, 0, 0, 1})
	copy(f[14+16:], []byte{10, 0, 0, 2})
	f[34], f[35] = 0xc3, 0x50 // 50000
	f[36], f[37] = 0x01, 0xbb // 443
	s, d, p, port, ok := parsePacket(f)
	if !ok || s != "10.0.0.1" || d != "10.0.0.2" || p != "tcp" || port != 443 {
		t.Fatalf("got %v %v %v %v %v", s, d, p, port, ok)
	}
}

func TestParsePacketVLANv6UDP(t *testing.T) {
	f := make([]byte, 14+4+40+8)
	f[12], f[13] = 0x81, 0x00
	f[16], f[17] = 0x86, 0xdd
	o := 18
	f[o+6] = 17
	f[o+8+15], f[o+24+15] = 1, 2
	f[o+40], f[o+41] = 0x00, 0x35 // 53
	f[o+42], f[o+43] = 0xd4, 0x31
	s, d, p, port, ok := parsePacket(f)
	if !ok || s != "::1" || d != "::2" || p != "udp" || port != 53 {
		t.Fatalf("got %v %v %v %v %v", s, d, p, port, ok)
	}
}

func TestParsePacketRejectsShort(t *testing.T) {
	if _, _, _, _, ok := parsePacket(make([]byte, 10)); ok {
		t.Fatal("short frame accepted")
	}
	f := make([]byte, 14+20)
	f[12], f[13] = 0x08, 0x06 // ARP is ignored
	if _, _, _, _, ok := parsePacket(f); ok {
		t.Fatal("arp accepted")
	}
}

func TestFlowRate(t *testing.T) {
	f := &tflow{}
	now := int64(1000)
	f.advance(now)
	for s := int64(998); s <= 999; s++ {
		f.advance(s)
		f.ring[s%trafficRing] = 1000
	}
	f.sec = now
	if r := f.rate(2, now); r != 8000 {
		t.Fatalf("rate2 = %d, want 8000", r)
	}
	if r := f.rate(10, now+30); r != 0 {
		t.Fatalf("stale rate = %d, want 0", r)
	}
}
