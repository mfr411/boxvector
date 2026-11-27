package web

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	sessionCookie = "bv_session"
	sessionIdle   = 60 * time.Minute
	sessionMax    = 12 * time.Hour
	maxFails      = 5
	failWindow    = 10 * time.Minute
)

type session struct {
	user    string
	csrf    string
	created time.Time
	seen    time.Time
}

type auth struct {
	file       string
	trustProxy bool
	system     bool   // verify against the server accounts (/etc/shadow)
	shadow     string // shadow file path

	mu       sync.Mutex
	sessions map[string]*session
	fails    map[string][]time.Time
}

func newAuth(file string, trustProxy bool) *auth {
	return &auth{file: file, trustProxy: trustProxy, sessions: map[string]*session{}, fails: map[string][]time.Time{}}
}

func newAuthOpt(o Options) *auth {
	a := newAuth(o.AuthFile, o.TrustProxy)
	a.system, a.shadow = o.SystemAuth, "/etc/shadow"
	return a
}

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // no entropy: refuse to run rather than issue weak tokens
	}
	return hex.EncodeToString(b)
}

// users reads "user:bcrypt-hash" lines. The file is re-read on every login so
// password changes apply without a restart.
func (a *auth) users() (map[string]string, error) {
	b, err := os.ReadFile(a.file)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		u, h, ok := strings.Cut(line, ":")
		if ok {
			out[u] = h
		}
	}
	return out, nil
}

// SetPassword creates or replaces one user in the auth file (mode 0600).
func SetPassword(file, user, password string) error {
	if user == "" || strings.ContainsAny(user, ":\n\r ") {
		return fmt.Errorf("invalid user name")
	}
	if len(password) < 10 {
		return fmt.Errorf("password must be at least 10 characters")
	}
	h, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return err
	}
	var lines []string
	if b, err := os.ReadFile(file); err == nil {
		for _, l := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
			if u, _, ok := strings.Cut(l, ":"); ok && u == user || strings.TrimSpace(l) == "" {
				continue
			}
			lines = append(lines, l)
		}
	}
	lines = append(lines, user+":"+string(h))
	tmp := file + ".tmp"
	if err := os.WriteFile(tmp, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, file)
}

func (a *auth) clientIP(r *http.Request) string {
	if a.trustProxy {
		if ip := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-For"), ",")[0]); net.ParseIP(ip) != nil {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// blocked reports whether ip had too many failed logins recently.
func (a *auth) blocked(ip string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	var keep []time.Time
	for _, t := range a.fails[ip] {
		if time.Since(t) < failWindow {
			keep = append(keep, t)
		}
	}
	a.fails[ip] = keep
	return len(keep) >= maxFails
}

// dummyHash keeps the timing of unknown users equal to wrong passwords.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("boxvector-dummy"), 12)

func (a *auth) login(ip, user, password string) (*session, string, bool) {
	var ok bool
	if a.system {
		ok = systemLogin(a.shadow, user, password)
	} else {
		users, err := a.users()
		if err != nil {
			return nil, "", false
		}
		h, found := users[user]
		if !found {
			_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
		}
		ok = found && bcrypt.CompareHashAndPassword([]byte(h), []byte(password)) == nil
	}
	if !ok {
		a.mu.Lock()
		a.fails[ip] = append(a.fails[ip], time.Now())
		a.mu.Unlock()
		return nil, "", false
	}
	s := &session{user: user, csrf: randHex(16), created: time.Now(), seen: time.Now()}
	tok := randHex(32)
	a.mu.Lock()
	a.sessions[tok] = s
	delete(a.fails, ip)
	a.mu.Unlock()
	return s, tok, true
}

func (a *auth) get(r *http.Request) (*session, string) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return nil, ""
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	s := a.sessions[c.Value]
	if s == nil {
		return nil, ""
	}
	if time.Since(s.seen) > sessionIdle || time.Since(s.created) > sessionMax {
		delete(a.sessions, c.Value)
		return nil, ""
	}
	s.seen = time.Now()
	return s, c.Value
}

func (a *auth) logout(tok string) {
	a.mu.Lock()
	delete(a.sessions, tok)
	a.mu.Unlock()
}

func csrfOK(s *session, r *http.Request) bool {
	return subtle.ConstantTimeCompare([]byte(r.PostFormValue("csrf")), []byte(s.csrf)) == 1
}
