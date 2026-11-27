package monitor

import (
	"crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// AppName is the label shown in the UI and the browser title. Keep it neutral so a device owner looking at the
// panel does not read a product name; the binary/package may still be called boxvector.
const AppName = "monitoring"

//go:embed templates/*.html
var tplFS embed.FS

type Server struct {
	st       *Store
	dir      string
	authFile string
	tpl      *template.Template
	mu       sync.Mutex
	sess     map[string]sess
	saved    time.Time
	fails    map[string][]time.Time
	now      func() time.Time
}

func NewServer(dir, authFile string) (*Server, error) {
	st, err := OpenStore(dir)
	if err != nil {
		return nil, err
	}
	fm := template.FuncMap{
		"ago": func(t time.Time) string {
			if t.IsZero() {
				return "never"
			}
			d := time.Since(t).Round(time.Second)
			switch {
			case d < time.Minute:
				return fmt.Sprintf("%ds ago", int(d.Seconds()))
			case d < time.Hour:
				return fmt.Sprintf("%dm ago", int(d.Minutes()))
			case d < 48*time.Hour:
				return fmt.Sprintf("%dh ago", int(d.Hours()))
			}
			return fmt.Sprintf("%dd ago", int(d.Hours()/24))
		},
		"ts": func(t time.Time) string { return t.Local().Format("2006-01-02 15:04:05") },
		"mb": func(n int64) string { return fmt.Sprintf("%.1f MB", float64(n)/1048576) },
		"up": func(s int64) string { return fmt.Sprintf("%dd %dh", s/86400, s%86400/3600) },
		"short": func(s string) string {
			if len(s) > 12 {
				return s[:12]
			}
			return s
		},
	}
	t, err := template.New("").Funcs(fm).ParseFS(tplFS, "templates/*.html")
	if err != nil {
		return nil, err
	}
	srv := &Server{st: st, dir: dir, authFile: authFile, tpl: t, sess: map[string]sess{}, fails: map[string][]time.Time{}, now: time.Now}
	srv.loadSess()
	return srv, nil
}

const sessTTL = 30 * 24 * time.Hour

type sess struct {
	User string    `json:"user"`
	Exp  time.Time `json:"exp"`
}

func (s *Server) sessFile() string { return filepath.Join(s.dir, "sessions.json") }

func (s *Server) loadSess() {
	b, err := os.ReadFile(s.sessFile())
	if err != nil {
		return
	}
	m := map[string]sess{}
	if json.Unmarshal(b, &m) == nil {
		for k, v := range m {
			if v.Exp.After(s.now()) {
				s.sess[k] = v
			}
		}
	}
}

// saveSess writes the (hashed) session table; caller holds s.mu.
func (s *Server) saveSess() {
	b, _ := json.Marshal(s.sess)
	tmp := s.sessFile() + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, s.sessFile())
	}
	s.saved = s.now()
}

func (s *Server) Store() *Store { return s.st }

func clientIP(r *http.Request) string {
	if v := r.Header.Get("X-Real-IP"); v != "" {
		return v
	}
	h, _, _ := net.SplitHostPort(r.RemoteAddr)
	return h
}

func (s *Server) Handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("/api/enroll", s.apiEnroll)
	m.HandleFunc("/api/heartbeat", s.apiHeartbeat)
	m.HandleFunc("/api/patch-result", s.apiPatchResult)
	m.HandleFunc("/api/release/", s.apiRelease)
	m.HandleFunc("/install.sh", s.installScript)
	m.HandleFunc("/login", s.login)
	m.HandleFunc("/logout", s.logout)
	m.HandleFunc("/", s.admin(s.devices))
	m.HandleFunc("/device", s.admin(s.device))
	m.HandleFunc("/releases", s.admin(s.releases))
	m.HandleFunc("/enroll", s.admin(s.enrollPage))
	m.HandleFunc("/act", s.admin(s.act))
	m.HandleFunc("/account", s.admin(s.account))
	return secure(m)
}

func secure(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Robots-Tag", "noindex, nofollow")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; frame-ancestors 'none'")
		h.ServeHTTP(w, r)
	})
}

// ---------- agent API ----------

func bearer(r *http.Request) string {
	return strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
}

func jsonOut(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) throttle(key string, max int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	var keep []time.Time
	for _, t := range s.fails[key] {
		if now.Sub(t) < 10*time.Minute {
			keep = append(keep, t)
		}
	}
	s.fails[key] = keep
	return len(keep) >= max
}

func (s *Server) fail(key string) {
	s.mu.Lock()
	s.fails[key] = append(s.fails[key], s.now())
	s.mu.Unlock()
}

func (s *Server) apiEnroll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	ip := clientIP(r)
	if s.throttle("enroll:"+ip, 10) {
		jsonOut(w, 429, map[string]string{"error": "too many attempts"})
		return
	}
	var in struct{ Token, Hostname string }
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&in); err != nil || in.Hostname == "" {
		jsonOut(w, 400, map[string]string{"error": "bad request"})
		return
	}
	id, secret, err := s.st.Enroll(in.Token, in.Hostname, ip)
	if err != nil {
		s.fail("enroll:" + ip)
		jsonOut(w, 403, map[string]string{"error": "invalid enroll token"})
		return
	}
	jsonOut(w, 200, map[string]any{"id": id, "secret": secret, "interval": HeartbeatInterval})
}

func (s *Server) apiHeartbeat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	var in Info
	if err := json.NewDecoder(io.LimitReader(r.Body, 16384)).Decode(&in); err != nil {
		jsonOut(w, 400, map[string]string{"error": "bad request"})
		return
	}
	order, err := s.st.Heartbeat(bearer(r), clientIP(r), in)
	if err != nil {
		jsonOut(w, 401, map[string]string{"error": "unauthorized"})
		return
	}
	jsonOut(w, 200, map[string]any{"interval": HeartbeatInterval, "patch": order})
}

func (s *Server) apiPatchResult(w http.ResponseWriter, r *http.Request) {
	var in struct{ Version, Status, Msg string }
	if r.Method != http.MethodPost || json.NewDecoder(io.LimitReader(r.Body, 8192)).Decode(&in) != nil {
		http.Error(w, "bad request", 400)
		return
	}
	if err := s.st.PatchResult(bearer(r), in.Version, in.Status, in.Msg); err != nil {
		jsonOut(w, 401, map[string]string{"error": err.Error()})
		return
	}
	jsonOut(w, 200, map[string]string{"ok": "1"})
}

func (s *Server) apiRelease(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.st.Authenticate(bearer(r)); !ok {
		http.Error(w, "unauthorized", 401)
		return
	}
	f := filepath.Base(strings.TrimPrefix(r.URL.Path, "/api/release/"))
	if !s.st.HasFile(f) {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, filepath.Join(s.dir, "releases", f))
}

func (s *Server) installScript(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprint(w, "# boxvector agent enrollment\n# usage: curl -fsSL https://monitor.boxvector.co/install.sh | sudo ENROLL_TOKEN=<token> sh\nset -e\n"+
		"[ -n \"$ENROLL_TOKEN\" ] || { echo 'set ENROLL_TOKEN=<token>' >&2; exit 1; }\n"+
		"command -v boxvector >/dev/null || { echo 'boxvector is not installed' >&2; exit 1; }\n"+
		"boxvector agent enroll --url https://monitor.boxvector.co --token \"$ENROLL_TOKEN\"\n"+
		"systemctl enable --now boxvector-agent\nsystemctl --no-pager status boxvector-agent | head -4\n")
}

// ---------- admin auth ----------

func (s *Server) users() map[string]string {
	m := map[string]string{}
	b, _ := os.ReadFile(s.authFile)
	for _, l := range strings.Split(string(b), "\n") {
		if i := strings.IndexByte(l, ':'); i > 0 {
			m[l[:i]] = strings.TrimSpace(l[i+1:])
		}
	}
	return m
}

func SetPassword(file, user, pw string) error {
	h, err := bcrypt.GenerateFromPassword([]byte(pw), 12)
	if err != nil {
		return err
	}
	var out []string
	b, _ := os.ReadFile(file)
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if l != "" && !strings.HasPrefix(l, user+":") {
			out = append(out, l)
		}
	}
	out = append(out, user+":"+string(h))
	// atomic replace: a crash never leaves a half written users file
	tmp := file + ".tmp"
	if err := os.WriteFile(tmp, []byte(strings.Join(out, "\n")+"\n"), 0o640); err != nil {
		return err
	}
	if err := os.Rename(tmp, file); err != nil {
		return err
	}
	// the service runs as a dedicated user; if this CLI ran as root the file would otherwise
	// become root-owned and the service could no longer read it (it must stay readable + writable by the dir owner)
	if di, err := os.Stat(filepath.Dir(file)); err == nil {
		fi, err := os.Stat(file)
		if err == nil {
			ds, okd := di.Sys().(*syscall.Stat_t)
			fs, okf := fi.Sys().(*syscall.Stat_t)
			if okd && okf && ds.Uid != fs.Uid {
				_ = os.Chown(file, int(ds.Uid), int(ds.Gid))
			}
		}
	}
	return nil
}

func rnd() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *Server) session(r *http.Request) (string, string, bool) {
	c, err := r.Cookie("bvm")
	if err != nil {
		return "", "", false
	}
	h := fmt.Sprintf("%x", sha256.Sum256([]byte(c.Value)))
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.sess[h]
	if !ok || s.now().After(v.Exp) {
		if ok {
			delete(s.sess, h)
			s.saveSess()
		}
		return "", "", false
	}
	v.Exp = s.now().Add(sessTTL)
	s.sess[h] = v
	if s.now().Sub(s.saved) > time.Hour {
		s.saveSess()
	}
	return h, v.User, true
}

func (s *Server) admin(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && r.URL.Path != "/device" && r.URL.Path != "/releases" && r.URL.Path != "/enroll" && r.URL.Path != "/account" && r.URL.Path != "/act" {
			http.NotFound(w, r)
			return
		}
		if _, _, ok := s.session(r); !ok {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		if r.Method == http.MethodPost && r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			http.Error(w, "cross-site request refused", 403)
			return
		}
		h(w, r)
	}
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	data := map[string]any{"Err": ""}
	if r.Method == http.MethodPost {
		ip := clientIP(r)
		if s.throttle("login:"+ip, 8) {
			data["Err"] = "Too many attempts. Try again later."
		} else {
			u, p := r.FormValue("user"), r.FormValue("password")
			h, ok := s.users()[u]
			good := ok && bcrypt.CompareHashAndPassword([]byte(h), []byte(p)) == nil
			if !ok {
				_ = bcrypt.CompareHashAndPassword([]byte("$2a$12$0000000000000000000000000000000000000000000000000000"), []byte(p))
			}
			if good {
				tok := rnd()
				s.mu.Lock()
				s.sess[fmt.Sprintf("%x", sha256.Sum256([]byte(tok)))] = sess{User: u, Exp: s.now().Add(sessTTL)}
				s.saveSess()
				s.mu.Unlock()
				http.SetCookie(w, &http.Cookie{Name: "bvm", Value: tok, Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode, MaxAge: int(sessTTL.Seconds())})
				http.Redirect(w, r, "/", http.StatusSeeOther)
				return
			}
			s.fail("login:" + ip)
			data["Err"] = "Invalid username or password."
		}
	}
	s.render(w, "login.html", data)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if h, _, ok := s.session(r); ok {
		s.mu.Lock()
		delete(s.sess, h)
		s.saveSess()
		s.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: "bvm", Path: "/", MaxAge: -1, Secure: true, HttpOnly: true})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

var navTitles = map[string]string{"devices": "Devices", "releases": "Releases", "enroll": "Enroll", "account": "Account", "login": "Sign in"}

func (s *Server) render(w http.ResponseWriter, name string, data any) {
	if m, ok := data.(map[string]any); ok {
		if t, ok := m["T"]; !ok || t == "" {
			if n, ok := m["Nav"].(string); ok {
				m["T"] = navTitles[n]
			}
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tpl.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, "template error", 500)
	}
}

// ---------- admin pages ----------

type devView struct {
	Device
	On bool
}

func (s *Server) devices(w http.ResponseWriter, r *http.Request) {
	now := s.now()
	var rows []devView
	on, off, ok := 0, 0, 0
	latest := ""
	rel := s.st.Releases()
	if len(rel) > 0 {
		latest = rel[0].Version
	}
	outdated := 0
	for _, d := range s.st.Devices() {
		v := devView{d, d.Online(now)}
		if v.On {
			on++
		} else {
			off++
		}
		if d.Patch.Status == "ok" {
			ok++
		}
		if latest != "" && d.Info.Version != latest {
			outdated++
		}
		rows = append(rows, v)
	}
	s.render(w, "devices.html", map[string]any{"Nav": "devices", "Rows": rows, "On": on, "Off": off, "Out": outdated, "Latest": latest, "Rel": rel, "Events": s.st.Events(12), "Msg": r.URL.Query().Get("m")})
}

func (s *Server) device(w http.ResponseWriter, r *http.Request) {
	d, ev, ok := s.st.Device(r.URL.Query().Get("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	s.render(w, "device.html", map[string]any{"Nav": "devices", "D": d, "On": d.Online(s.now()), "Events": ev, "Rel": s.st.Releases(), "Msg": r.URL.Query().Get("m")})
}

var debRe = regexp.MustCompile(`^boxvector_([0-9][A-Za-z0-9.+~-]*)_(amd64|arm64)\.deb$`)

func (s *Server) releases(w http.ResponseWriter, r *http.Request) {
	msg := r.URL.Query().Get("m")
	if r.Method == http.MethodPost {
		msg = s.upload(r)
		http.Redirect(w, r, "/releases?m="+template.URLQueryEscaper(msg), http.StatusSeeOther)
		return
	}
	s.render(w, "releases.html", map[string]any{"Nav": "releases", "Rel": s.st.Releases(), "Msg": msg})
}

func (s *Server) upload(r *http.Request) string {
	r.Body = http.MaxBytesReader(nil, r.Body, 120<<20)
	f, hd, err := r.FormFile("deb")
	if err != nil {
		return "Upload failed: " + err.Error()
	}
	defer f.Close()
	mm := debRe.FindStringSubmatch(filepath.Base(hd.Filename))
	if mm == nil {
		return "File name must be boxvector_<version>_<amd64|arm64>.deb"
	}
	name := mm[0]
	tmp, err := os.CreateTemp(filepath.Join(s.dir, "releases"), ".up-*")
	if err != nil {
		return "Upload failed: " + err.Error()
	}
	defer os.Remove(tmp.Name())
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), f)
	tmp.Close()
	if err != nil {
		return "Upload failed: " + err.Error()
	}
	head := make([]byte, 8)
	if fh, e := os.Open(tmp.Name()); e == nil {
		_, _ = io.ReadFull(fh, head)
		fh.Close()
	}
	if string(head) != "!<arch>\n" {
		return "Not a valid .deb file"
	}
	rel := Release{Version: mm[1], Arch: mm[2], File: name, SHA256: hex.EncodeToString(h.Sum(nil)), Size: n}
	if err := s.st.AddRelease(rel); err != nil {
		return err.Error()
	}
	if err := os.Rename(tmp.Name(), filepath.Join(s.dir, "releases", name)); err != nil {
		return "Store failed: " + err.Error()
	}
	_ = os.Chmod(filepath.Join(s.dir, "releases", name), 0o640)
	return "Release " + mm[1] + " (" + mm[2] + ") uploaded"
}

func (s *Server) enrollPage(w http.ResponseWriter, r *http.Request) {
	plain := ""
	if r.Method == http.MethodPost {
		switch r.FormValue("do") {
		case "new":
			l := strings.TrimSpace(r.FormValue("label"))
			if l == "" || len(l) > 60 {
				l = "unnamed"
			}
			plain, _ = s.st.AddEnrollToken(l)
		case "revoke":
			_ = s.st.RevokeEnrollToken(r.FormValue("id"))
		}
	}
	s.render(w, "enroll.html", map[string]any{"Nav": "enroll", "Tokens": s.st.Tokens(), "New": plain})
}

func (s *Server) act(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	_ = r.ParseForm()
	back, ids := "/", r.Form["id"]
	if b := r.FormValue("back"); strings.HasPrefix(b, "/device?id=") {
		back = b
	}
	msg := ""
	switch r.FormValue("do") {
	case "approve":
		n, err := s.st.Approve(ids, r.FormValue("version"))
		msg = fmt.Sprintf("%d device(s) queued for %s", n, r.FormValue("version"))
		if err != nil {
			msg = err.Error()
		}
	case "cancel":
		n, _ := s.st.Cancel(ids)
		msg = fmt.Sprintf("%d queued patch(es) cancelled", n)
	case "delete":
		for _, id := range ids {
			_ = s.st.DeleteDevice(id)
		}
		msg, back = "Device removed", "/"
	case "note":
		if len(ids) == 1 {
			_ = s.st.SetNote(ids[0], r.FormValue("note"))
		}
		msg = "Saved"
	case "delrel":
		f, err := s.st.DeleteRelease(r.FormValue("version"), r.FormValue("arch"))
		if err != nil {
			msg = err.Error()
		} else {
			_ = os.Remove(filepath.Join(s.dir, "releases", filepath.Base(f)))
			msg = "Release deleted"
		}
		back = "/releases"
	}
	sep := "?"
	if strings.Contains(back, "?") {
		sep = "&"
	}
	http.Redirect(w, r, back+sep+"m="+template.URLQueryEscaper(msg), http.StatusSeeOther)
}

// account lets the signed-in admin change their own password. Other sessions of that user are signed out.
func (s *Server) account(w http.ResponseWriter, r *http.Request) {
	cur, user, _ := s.session(r)
	data := map[string]any{"Nav": "account", "User": user, "Msg": "", "Err": ""}
	if r.Method == http.MethodPost {
		old, n1, n2 := r.FormValue("old"), r.FormValue("new"), r.FormValue("again")
		h := s.users()[user]
		switch {
		case s.throttle("pw:"+user, 5):
			data["Err"] = "Too many attempts. Try again later."
		case h == "" || bcrypt.CompareHashAndPassword([]byte(h), []byte(old)) != nil:
			s.fail("pw:" + user)
			data["Err"] = "Current password is wrong."
		case len(n1) < 10:
			data["Err"] = "New password must be at least 10 characters."
		case len(n1) > 72:
			data["Err"] = "New password must be at most 72 characters."
		case n1 != n2:
			data["Err"] = "The two new passwords do not match."
		case n1 == old:
			data["Err"] = "New password must differ from the current one."
		default:
			if err := SetPassword(s.authFile, user, n1); err != nil {
				data["Err"] = "Could not save: " + err.Error()
			} else {
				s.mu.Lock()
				for k, v := range s.sess {
					if v.User == user && k != cur {
						delete(s.sess, k)
					}
				}
				s.saveSess()
				s.mu.Unlock()
				data["Msg"] = "Password changed. Other sessions were signed out."
			}
		}
	}
	s.render(w, "account.html", data)
}
