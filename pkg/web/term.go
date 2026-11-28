package web

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// Terminal: root shell in the browser. Gate: HTTPS (or loopback) unless --terminal-insecure,
// account must be root or in sudo/wheel/admin, every open/close is written to Logs.

var termOwner sync.Map // terminal id -> user

func canTerminal(user string) bool {
	if user == "root" {
		return true
	}
	f, err := os.Open("/etc/group")
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		p := strings.Split(sc.Text(), ":")
		if len(p) < 4 || (p[0] != "sudo" && p[0] != "wheel" && p[0] != "admin") {
			continue
		}
		for _, m := range strings.Split(p[3], ",") {
			if m == user {
				return true
			}
		}
	}
	return false
}

func (s *server) secureTransport(r *http.Request) bool {
	if s.opt.TermInsecure || r.TLS != nil || (s.opt.TrustProxy && r.Header.Get("X-Forwarded-Proto") == "https") {
		return true
	}
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback() && r.Header.Get("X-Forwarded-For") == ""
}

func (s *server) termClient() *http.Client {
	return &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "unix", s.opt.TermSocket)
	}}, Timeout: 40 * time.Second}
}

// termGate checks transport, group and ownership; returns false after writing the error.
func (s *server) termGate(w http.ResponseWriter, r *http.Request, sess *session, id string) bool {
	if !s.secureTransport(r) {
		http.Error(w, "terminal needs HTTPS (or localhost)", http.StatusForbidden)
		return false
	}
	if !canTerminal(sess.user) {
		http.Error(w, "terminal is limited to root and sudo users", http.StatusForbidden)
		return false
	}
	if id != "" {
		if u, ok := termOwner.Load(id); !ok || u.(string) != sess.user {
			http.Error(w, "no such terminal", http.StatusNotFound)
			return false
		}
	}
	return true
}

func (s *server) termPage(w http.ResponseWriter, r *http.Request, sess *session) {
	msg := ""
	switch {
	case !s.secureTransport(r):
		msg = "The terminal is disabled over plain HTTP. Use HTTPS (--tls-cert/--tls-key or a reverse proxy with --trust-proxy), or start the web service with --terminal-insecure."
	case !canTerminal(sess.user):
		msg = "The terminal is limited to root and members of the sudo group."
	default:
		if c, err := net.DialTimeout("unix", s.opt.TermSocket, 2*time.Second); err != nil {
			msg = "The terminal helper is not running. Start it with: systemctl enable --now boxvector-term"
		} else {
			c.Close()
		}
	}
	s.render(w, "terminal", ctxPage{Title: "Terminal", Nav: "terminal", User: sess.user, CSRF: sess.csrf, Data: map[string]any{"Msg": msg}})
}

func (s *server) termNew(w http.ResponseWriter, r *http.Request, sess *session) {
	if !s.termGate(w, r, sess, "") {
		return
	}
	if r.PostFormValue("password") == "" {
		http.Error(w, "password required", http.StatusForbidden)
		return
	}
	resp, err := s.termClient().PostForm("http://helper/new", url.Values{"cols": {r.PostFormValue("cols")}, "rows": {r.PostFormValue("rows")},
		"user": {termAccount(r, sess.user)}, "password": {r.PostFormValue("password")}})
	if err != nil {
		http.Error(w, "terminal helper unavailable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	var m map[string]string
	_ = json.Unmarshal(b, &m)
	if resp.StatusCode != 200 || m["id"] == "" {
		code := resp.StatusCode
		if code == 200 {
			code = http.StatusServiceUnavailable
		}
		s.store.log(Event{Time: time.Now(), User: sess.user, Action: "terminal", OK: false, Summary: "terminal refused from " + s.auth.clientIP(r)})
		http.Error(w, m["error"], code)
		return
	}
	termOwner.Store(m["id"], sess.user)
	s.store.log(Event{Time: time.Now(), User: sess.user, Action: "terminal", OK: true, Summary: "root terminal opened from " + s.auth.clientIP(r)})
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(b)
}

func (s *server) termOut(w http.ResponseWriter, r *http.Request, sess *session) {
	id := r.URL.Query().Get("id")
	if !s.termGate(w, r, sess, id) {
		return
	}
	resp, err := s.termClient().Get("http://helper/out?id=" + url.QueryEscape(id) + "&off=" + url.QueryEscape(r.URL.Query().Get("off")))
	if err != nil {
		http.Error(w, "terminal helper unavailable", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	for _, h := range []string{"X-Next", "X-Done", "Content-Type"} {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, io.LimitReader(resp.Body, 1<<20))
}

func (s *server) termForward(path string, fields ...string) handler {
	return func(w http.ResponseWriter, r *http.Request, sess *session) {
		id := r.PostFormValue("id")
		if !s.termGate(w, r, sess, id) {
			return
		}
		v := url.Values{}
		for _, f := range fields {
			v.Set(f, r.PostFormValue(f))
		}
		resp, err := s.termClient().PostForm("http://helper/"+path, v)
		if err != nil {
			http.Error(w, "terminal helper unavailable", http.StatusBadGateway)
			return
		}
		resp.Body.Close()
		if path == "close" {
			termOwner.Delete(id)
			s.store.log(Event{Time: time.Now(), User: sess.user, Action: "terminal", OK: true, Summary: "root terminal closed"})
		}
		w.WriteHeader(resp.StatusCode)
	}
}

// termAccount is the system account whose password confirms the terminal. It defaults to the
// signed-in user; a file-auth web user can name a root/sudo account instead.
func termAccount(r *http.Request, def string) string {
	if u := strings.TrimSpace(r.PostFormValue("user")); u != "" && len(u) <= 32 {
		return u
	}
	return def
}
