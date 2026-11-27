//go:build linux

package web

import "testing"

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
