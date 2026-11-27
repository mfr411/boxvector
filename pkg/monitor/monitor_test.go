package monitor

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newStore(t *testing.T) *Store {
	st, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func enroll(t *testing.T, st *Store, name string) (string, string) {
	tok, _ := st.AddEnrollToken("t")
	id, sec, err := st.Enroll(tok, name, "192.0.2.1")
	if err != nil {
		t.Fatal(err)
	}
	return id, sec
}

func TestEnrollAndHeartbeat(t *testing.T) {
	st := newStore(t)
	if _, _, err := st.Enroll("nope", "x", "1.1.1.1"); err == nil {
		t.Fatal("bad token accepted")
	}
	id, sec := enroll(t, st, "r1")
	if _, err := st.Heartbeat("wrong", "1.1.1.1", Info{}); err == nil {
		t.Fatal("bad secret accepted")
	}
	if _, err := st.Heartbeat(sec, "198.51.100.7", Info{Hostname: "r1", Version: "1.0", Arch: "amd64", Established: 2}); err != nil {
		t.Fatal(err)
	}
	d, _, _ := st.Device(id)
	if !d.Online(time.Now()) || d.RemoteIP != "198.51.100.7" || d.Info.Established != 2 {
		t.Fatalf("%+v", d)
	}
	if d.Online(time.Now().Add(OfflineAfter + time.Minute)) {
		t.Fatal("stale device shown online")
	}
	if strings.Contains(st.s.Devices[id].TokenHash, sec) {
		t.Fatal("secret stored in clear")
	}
}

func TestRevokedTokenAndPersistence(t *testing.T) {
	dir := t.TempDir()
	st, _ := OpenStore(dir)
	tok, _ := st.AddEnrollToken("a")
	_ = st.RevokeEnrollToken(st.Tokens()[0].ID)
	if _, _, err := st.Enroll(tok, "x", "1.1.1.1"); err == nil {
		t.Fatal("revoked token accepted")
	}
	tok2, _ := st.AddEnrollToken("b")
	id, sec, _ := st.Enroll(tok2, "x", "1.1.1.1")
	st2, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := st2.Authenticate(sec); !ok || got != id {
		t.Fatal("device lost after reload")
	}
	if fi, _ := os.Stat(filepath.Join(dir, "state.json")); fi.Mode().Perm() != 0o600 {
		t.Fatalf("state mode %v", fi.Mode())
	}
}

func TestRollingPatch(t *testing.T) {
	st := newStore(t)
	a, sa := enroll(t, st, "a")
	b, sb := enroll(t, st, "b")
	for _, s := range []string{sa, sb} {
		_, _ = st.Heartbeat(s, "1.1.1.1", Info{Version: "1.0", Arch: "amd64"})
	}
	_ = st.AddRelease(Release{Version: "1.1", Arch: "amd64", File: "boxvector_1.1_amd64.deb", SHA256: "x", Size: 1})
	if n, _ := st.Approve([]string{a, b}, "1.1"); n != 2 {
		t.Fatalf("approved %d", n)
	}
	if n, _ := st.Approve([]string{a}, "9.9"); n != 0 {
		t.Fatal("unknown release approved")
	}
	oa, _ := st.Heartbeat(sa, "1.1.1.1", Info{Version: "1.0", Arch: "amd64"})
	ob, _ := st.Heartbeat(sb, "1.1.1.1", Info{Version: "1.0", Arch: "amd64"})
	if oa == nil || ob != nil {
		t.Fatalf("rolling rule broken: a=%v b=%v", oa, ob)
	}
	if err := st.PatchResult(sa, "1.1", "ok", "fine"); err != nil {
		t.Fatal(err)
	}
	if ob, _ = st.Heartbeat(sb, "1.1.1.1", Info{Version: "1.0", Arch: "amd64"}); ob == nil {
		t.Fatal("second device not released after first finished")
	}
	// wrong architecture never gets an order
	c, sc := enroll(t, st, "c")
	_, _ = st.Heartbeat(sc, "1.1.1.1", Info{Version: "1.0", Arch: "arm64"})
	if n, _ := st.Approve([]string{c}, "1.1"); n != 0 {
		t.Fatal("arm64 device approved for amd64 release")
	}
}

func TestDeleteReleaseBlockedWhileQueued(t *testing.T) {
	st := newStore(t)
	a, sa := enroll(t, st, "a")
	_, _ = st.Heartbeat(sa, "1.1.1.1", Info{Version: "1.0", Arch: "amd64"})
	_ = st.AddRelease(Release{Version: "1.1", Arch: "amd64", File: "f.deb"})
	_, _ = st.Approve([]string{a}, "1.1")
	if _, err := st.DeleteRelease("1.1", "amd64"); err == nil {
		t.Fatal("deleted a queued release")
	}
	_, _ = st.Cancel([]string{a})
	if _, err := st.DeleteRelease("1.1", "amd64"); err != nil {
		t.Fatal(err)
	}
}

func TestHTTPAuth(t *testing.T) {
	dir := t.TempDir()
	af := filepath.Join(dir, "users")
	_ = SetPassword(af, "admin", "correct-horse-1")
	s, err := NewServer(dir, af)
	if err != nil {
		t.Fatal(err)
	}
	h := s.Handler()
	do := func(m, p, body, cookie string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(m, p, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if cookie != "" {
			r.Header.Set("Cookie", cookie)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := do("GET", "/", "", ""); w.Code != http.StatusSeeOther {
		t.Fatalf("admin page open without login: %d", w.Code)
	}
	if w := do("GET", "/api/heartbeat", "", ""); w.Code != 405 {
		t.Fatalf("heartbeat GET %d", w.Code)
	}
	if w := do("POST", "/api/heartbeat", "{}", ""); w.Code != 401 {
		t.Fatalf("heartbeat without secret %d", w.Code)
	}
	if w := do("GET", "/api/release/x.deb", "", ""); w.Code != 401 {
		t.Fatalf("release download without secret %d", w.Code)
	}
	if w := do("POST", "/login", "user=admin&password=bad", ""); w.Code != 200 || !strings.Contains(w.Body.String(), "Invalid") {
		t.Fatal("bad password accepted")
	}
	w := do("POST", "/login", "user=admin&password=correct-horse-1", "")
	if w.Code != http.StatusSeeOther {
		t.Fatalf("login %d", w.Code)
	}
	ck := w.Result().Cookies()[0]
	if !ck.Secure || !ck.HttpOnly {
		t.Fatal("cookie flags")
	}
	for _, p := range []string{"/", "/releases", "/enroll"} {
		if w := do("GET", p, "", "bvm="+ck.Value); w.Code != 200 {
			t.Fatalf("%s %d", p, w.Code)
		}
	}
	if w := do("GET", "/etc/passwd", "", "bvm="+ck.Value); w.Code != 404 {
		t.Fatalf("unknown path %d", w.Code)
	}
}
