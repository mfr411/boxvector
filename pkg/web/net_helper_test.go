//go:build linux

package web

import (
	"strings"
	"testing"
)

func TestValidateRoute(t *testing.T) {
	bad := []kroute{
		{Dst: "x", Via: "10.0.0.1"}, {Dst: "10.0.0.0/8"}, {Dst: "10.0.0.0/8", Via: "fd00::1"},
		{Dst: "10.0.0.0/8", Via: "10.0.0.1; reboot"}, {Dst: "10.0.0.0/8", Dev: "lo", Via: "fe80::1%lo"},
		{Dst: "10.0.0.0/8", Dev: "../etc"}, {Dst: "::ffff:10.0.0.0/104", Via: "10.0.0.1"},
	}
	for _, k := range bad {
		if _, err := validateRoute(k); err == nil {
			t.Errorf("accepted %+v", k)
		}
	}
	k, err := validateRoute(kroute{Dst: "10.1.2.3/8", Via: " 10.0.0.1 ", Comment: "a\nb"})
	if err != nil || k.Dst != "10.0.0.0/8" || k.Via != "10.0.0.1" || k.Comment != "ab" {
		t.Errorf("got %+v %v", k, err)
	}
}

func TestGuardRoute(t *testing.T) {
	if guardRoute(kroute{Dst: "10.0.0.0/8"}, "10.5.5.5", false) == nil {
		t.Error("route covering the client must be refused")
	}
	if guardRoute(kroute{Dst: "10.0.0.0/8"}, "10.5.5.5", true) != nil || guardRoute(kroute{Dst: "192.0.2.0/24"}, "10.5.5.5", false) != nil {
		t.Error("force or unrelated route must pass")
	}
}

func TestApplyGW(t *testing.T) {
	c := &ifc{Kind: "vlan", Name: "x.10", Parent: "x", VID: 10, Addrs: []string{"10.1.1.2/24"}}
	for _, bad := range []string{"10.2.2.1", "10.1.1.2", "0.0.0.0", "224.0.0.1", "zzz", "2001:db8::1"} {
		if applyGW(&ifc{Addrs: c.Addrs}, bad) == nil {
			t.Fatalf("gateway %q must be rejected", bad)
		}
	}
	if applyGW(&ifc{}, "10.1.1.1") == nil {
		t.Fatal("gateway without address must be rejected")
	}
	if err := applyGW(c, "10.1.1.1"); err != nil || c.GW != "10.1.1.1" {
		t.Fatalf("valid gateway rejected: %v", err)
	}
	if err := applyGW(c, ""); err != nil {
		t.Fatal("empty gateway is optional")
	}
	if !strings.Contains(c.render(), "\tgateway 10.1.1.1\n") {
		t.Fatalf("stanza lacks gateway:\n%s", c.render())
	}
}
