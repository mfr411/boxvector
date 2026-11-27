package fleet

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// fake simulates two routers; behaviour is switched per host.
type fake struct {
	mu       sync.Mutex
	ver      map[string]string // host -> bird3 version
	breakOn  map[string]bool   // host -> BIRD dies after upgrade
	upgraded map[string]bool
	calls    []string
}

func (f *fake) Run(_ context.Context, h Host, script string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, h.Name)
	switch {
	case strings.Contains(script, "apt list --upgradable"):
		bird, conf := "active", "ok"
		up, tot := "2", "2"
		if f.upgraded[h.Name] && f.breakOn[h.Name] {
			bird, conf, up = "failed", "bad", "0"
		}
		out := "OS=Debian GNU/Linux 12\nKERNEL=6.1.0-1\nLATESTK=6.1.0-1\nBIRD=" + f.ver[h.Name] + "\nBV=0.9\nBIRDACTIVE=" + bird + "\nBGP=" + up + "/" + tot + "\nCONF=" + conf + "\n"
		if !f.upgraded[h.Name] {
			out += "PKG bird3 3.4.0 3.4.1 bookworm-security\nPKG curl 7.0 7.1 bookworm\n"
		}
		return out, nil
	case strings.Contains(script, "dpkg-query -W -f='VER"):
		return "VER bird3 " + f.ver[h.Name] + "\n", nil
	case strings.Contains(script, "apt-get") && strings.Contains(script, " -s "):
		return "Inst bird3\nRC=0\n", nil
	case strings.Contains(script, "apt-get") && strings.Contains(script, "--only-upgrade"):
		f.ver[h.Name] = "3.4.1"
		f.upgraded[h.Name] = true
		return "REPACK=bird3\nRC=0\n", nil
	case strings.Contains(script, "dpkg -i"):
		f.ver[h.Name] = "3.4.0"
		f.upgraded[h.Name] = false
		return "RC=0\n", nil
	}
	return "", nil
}

func newMgr(t *testing.T, f *fake) *Manager {
	m := New(t.TempDir(), f)
	m.PollEvery, m.PostWait = time.Millisecond, 30*time.Millisecond
	for _, n := range []string{"r1", "r2", "r3"} {
		if err := m.AddHost(Host{Name: n, Addr: "192.0.2.1"}); err != nil {
			t.Fatal(err)
		}
		f.ver[n] = "3.4.0"
	}
	return m
}

func wait(t *testing.T, m *Manager, id string) *Job {
	for i := 0; i < 400; i++ {
		j, err := m.Job(id)
		if err == nil && j.State != "running" {
			return j
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("job timeout")
	return nil
}

func TestRollingStopsOnFailureThenRollback(t *testing.T) {
	f := &fake{ver: map[string]string{}, breakOn: map[string]bool{"r2": true}, upgraded: map[string]bool{}}
	m := newMgr(t, f)
	id, err := m.Start("admin", "patch", []string{"r1", "r2", "r3"}, ModeSecurity, nil, false, "")
	if err != nil {
		t.Fatal(err)
	}
	j := wait(t, m, id)
	got := []string{j.Hosts[0].State, j.Hosts[1].State, j.Hosts[2].State}
	if strings.Join(got, ",") != "ok,failed,skipped" || j.State != "failed" {
		t.Fatalf("states %v job %s", got, j.State)
	}
	if f.upgraded["r3"] {
		t.Fatal("r3 must not be touched after r2 failed")
	}
	if j.Hosts[1].Before["bird3"] != "3.4.0" {
		t.Fatalf("before not recorded: %v", j.Hosts[1].Before)
	}
	// roll back r2 only
	f.breakOn["r2"] = false
	rid, err := m.Start("admin", "rollback", []string{"r2"}, "", nil, false, id)
	if err != nil {
		t.Fatal(err)
	}
	rj := wait(t, m, rid)
	if rj.Hosts[0].State != "rolledback" || f.ver["r2"] != "3.4.0" {
		t.Fatalf("rollback: %+v ver=%s", rj.Hosts[0], f.ver["r2"])
	}
	if f.ver["r1"] != "3.4.1" {
		t.Fatal("r1 should stay patched")
	}
}

func TestDryRunChangesNothing(t *testing.T) {
	f := &fake{ver: map[string]string{}, breakOn: map[string]bool{}, upgraded: map[string]bool{}}
	m := newMgr(t, f)
	id, _ := m.Start("a", "patch", []string{"r1"}, ModeFull, nil, true, "")
	j := wait(t, m, id)
	if j.Hosts[0].State != "ok" || f.ver["r1"] != "3.4.0" {
		t.Fatalf("%+v %s", j.Hosts[0], f.ver["r1"])
	}
}

func TestPackageNameInjectionRejected(t *testing.T) {
	f := &fake{ver: map[string]string{}, breakOn: map[string]bool{}, upgraded: map[string]bool{}}
	m := newMgr(t, f)
	if _, err := m.Start("a", "patch", []string{"r1"}, ModePackages, []string{"bird3; rm -rf /"}, false, ""); err == nil {
		t.Fatal("expected rejection")
	}
	if len(f.calls) != 0 {
		t.Fatal("nothing must be executed")
	}
}

func TestOneJobAtATimeAndHostValidation(t *testing.T) {
	f := &fake{ver: map[string]string{}, breakOn: map[string]bool{}, upgraded: map[string]bool{}}
	m := newMgr(t, f)
	for _, h := range []Host{{Name: "bad name", Addr: "1.1.1.1"}, {Name: "x", Addr: "1.1.1.1;id"}, {Name: "y", Addr: "1.1.1.1", User: "ro ot"}} {
		if err := m.AddHost(h); err == nil {
			t.Fatalf("accepted %+v", h)
		}
	}
	m.mu.Lock()
	m.busy = true
	m.mu.Unlock()
	if _, err := m.Start("a", "check", []string{"r1"}, "", nil, false, ""); err == nil {
		t.Fatal("second job must be refused")
	}
}

func TestParseCheck(t *testing.T) {
	st := ParseCheck("OS=Debian\nKERNEL=6.1.0-1\nLATESTK=6.1.0-2\nBIRD=3.4.0\nBGP=1/2\nCONF=ok\nBIRDACTIVE=active\nPKG a 1 2 bookworm-security\n")
	if !st.Reboot || st.BGPUp != 1 || st.BGPTotal != 2 || st.Security() != 1 || !st.ConfigValid {
		t.Fatalf("%+v", st)
	}
}
