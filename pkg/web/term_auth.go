package web

import (
	"net/http"
	"time"
)

const (
	termFailMax    = 5
	termFailWindow = 10 * time.Minute
)

// authorize runs inside the root helper. The password is checked against /etc/shadow here, not
// in the web process: a compromised web service cannot get a root shell without it.
// Returns 0 when allowed, else an HTTP status and a message.
func (t *termHelper) authorize(user, password string) (int, string) {
	if user == "" || password == "" {
		return http.StatusForbidden, "password required"
	}
	t.mu.Lock()
	now := time.Now()
	keep := t.fails[user][:0:0]
	for _, f := range t.fails[user] {
		if now.Sub(f) < termFailWindow {
			keep = append(keep, f)
		}
	}
	t.fails[user] = keep
	locked := len(keep) >= termFailMax
	t.mu.Unlock()
	if locked {
		return http.StatusTooManyRequests, "too many wrong passwords, try again later"
	}
	if !canTerminal(user) || !systemLogin("/etc/shadow", user, password) {
		t.mu.Lock()
		t.fails[user] = append(t.fails[user], now)
		t.mu.Unlock()
		return http.StatusForbidden, "wrong password, or the account is not root / a sudo user"
	}
	t.mu.Lock()
	delete(t.fails, user)
	t.mu.Unlock()
	return 0, ""
}

// isAdmin says whether a signed-in web user may use the root-level pages (interfaces, routes,
// traffic, VPP, terminal). With server-account login it needs root/sudo. With --file-auth every
// listed user is an administrator: they can already rewrite the BGP config. The terminal still
// asks the root helper to verify a root/sudo account password.
func (s *server) isAdmin(sess *session) bool {
	if s.auth.system {
		return canTerminal(sess.user)
	}
	return true
}

func (s *server) termDefaultAccount(sess *session) string {
	if s.auth.system {
		return sess.user
	}
	return "root"
}
