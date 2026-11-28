package web

import (
	"net/http/httptest"
	"net/netip"
	"os"
	"testing"
)

func TestAllowFrom(t *testing.T) {
	p, err := ParseAllow([]string{"10.0.0.0/8, 192.0.2.5", "2001:db8::/32"})
	if err != nil || len(p) != 3 {
		t.Fatalf("parse: %v %v", p, err)
	}
	if _, err := ParseAllow([]string{"nonsense"}); err == nil {
		t.Fatal("bad entry must fail")
	}
	s := &server{allow: p, auth: &auth{}}
	for ip, want := range map[string]bool{"10.1.2.3:1": true, "192.0.2.5:9": true, "192.0.2.6:9": false, "[2001:db8::1]:5": true, "8.8.8.8:1": false} {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = ip
		if got := s.ipAllowed(r); got != want {
			t.Errorf("%s: got %v want %v", ip, got, want)
		}
	}
	_ = netip.Prefix{}
	if !(&server{auth: &auth{}}).ipAllowed(httptest.NewRequest("GET", "/", nil)) {
		t.Fatal("empty allowlist must allow everyone")
	}
}

func TestUserLockout(t *testing.T) {
	f := t.TempDir() + "/users"
	if err := os.WriteFile(f, []byte("admin:x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := newAuthOpt(Options{AuthFile: f})
	for i := 0; i < maxFails*4; i++ {
		a.login("10.0.0."+string(rune('a'+i%20)), "victim", "wrong")
	}
	if !a.blockedUser("victim") {
		t.Fatal("account must lock after distributed guesses")
	}
	if a.blockedUser("other") {
		t.Fatal("other accounts unaffected")
	}
}
