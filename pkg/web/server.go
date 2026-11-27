package web

import (
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/mfr411/boxvector/pkg/bird"
)

//go:embed templates/*.html static/*
var assets embed.FS

// Options configures the web server.
type Options struct {
	Listen       string
	ConfigFile   string
	AuthFile     string
	StateDir     string
	TLSCert      string
	TLSKey       string
	TrustProxy   bool
	Version      string
	TermSocket   string // root terminal helper socket
	TermInsecure bool   // allow the terminal over plain HTTP
	SystemAuth   bool   // sign in with the server's own accounts (root / sudo group)
}

type server struct {
	opt   Options
	auth  *auth
	store *store
	tpl   map[string]*template.Template
}

// routerInfo is the subset of the boxvector config the UI needs; parsed
// separately so the UI never triggers PeeringDB/IRR lookups.
type routerInfo struct {
	ASN       uint32 `yaml:"asn"`
	Hostname  string `yaml:"hostname"`
	Socket    string `yaml:"bird-socket"`
	BIRDDir   string `yaml:"bird-directory"`
	BIRDBin   string `yaml:"bird-binary"`
	routerErr string
}

func (s *server) router() routerInfo {
	ri := routerInfo{Socket: "/run/bird/bird.ctl", BIRDDir: "/etc/bird/"}
	b, err := os.ReadFile(s.opt.ConfigFile)
	if err == nil {
		err = yaml.Unmarshal(b, &ri)
	}
	if err != nil {
		ri.routerErr = err.Error()
	}
	if ri.Socket == "" {
		ri.Socket = "/run/bird/bird.ctl"
	}
	if ri.BIRDDir == "" {
		ri.BIRDDir = "/etc/bird/"
	}
	if ri.Hostname == "" {
		ri.Hostname, _ = os.Hostname()
	}
	return ri
}

// Serve runs the web UI until the listener fails.
func Serve(opt Options) error {
	if err := os.MkdirAll(opt.StateDir, 0o700); err != nil {
		return err
	}
	if opt.SystemAuth {
		if _, err := os.Open("/etc/shadow"); err != nil {
			return fmt.Errorf("system login needs read access to /etc/shadow (service group shadow): %w", err)
		}
	} else if st, err := os.Stat(opt.AuthFile); err != nil {
		return fmt.Errorf("auth file %s: %w (create a user with: boxvector web passwd <user>)", opt.AuthFile, err)
	} else if st.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("auth file %s must not be readable by group/other (chmod 600)", opt.AuthFile)
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	s := &server{
		opt:   opt,
		auth:  newAuthOpt(opt),
		store: &store{configFile: opt.ConfigFile, stateDir: opt.StateDir, binary: exe},
		tpl:   map[string]*template.Template{},
	}
	funcs := template.FuncMap{
		"num": func(i int) string {
			if i < 0 {
				return "—"
			}
			s := strconv.Itoa(i)
			for k := len(s) - 3; k > 0; k -= 3 {
				s = s[:k] + "," + s[k:]
			}
			return s
		},
		"ago":  func(t time.Time) string { return t.Format("02 Jan 15:04:05") },
		"op":   func(b byte) string { return string(b) },
		"list": func(v ...string) []string { return v },
		"snapt": func(n string) string {
			if tm, err := time.ParseInLocation("20060102-150405", n[:15], time.Local); err == nil && len(n) >= 15 {
				return tm.Format("02 Jan 15:04:05")
			}
			return n
		},
	}
	for _, p := range []string{"login", "peers", "peer_new", "peer_delete", "peer_toggle", "peer_comment", "route_new", "route_act", "route_comment", "config", "review", "routes", "logs", "interfaces", "vpp", "terminal"} {
		s.tpl[p] = template.Must(template.New("").Funcs(funcs).ParseFS(assets, "templates/layout.html", "templates/"+p+".html"))
	}

	static, _ := fs.Sub(assets, "static")
	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(static))))
	mux.HandleFunc("GET /login", s.loginPage)
	mux.HandleFunc("POST /login", s.loginPost)
	mux.HandleFunc("POST /logout", s.authed(s.logoutPost))
	mux.HandleFunc("GET /{$}", s.authed(s.peersPage))
	mux.HandleFunc("GET /interfaces", s.authed(s.ifacesPage))
	mux.HandleFunc("GET /api/ifaces", s.authed(s.ifacesAPI))
	mux.HandleFunc("GET /vpp", s.authed(s.vppPage))
	mux.HandleFunc("GET /api/vpp", s.authed(s.vppAPI))
	mux.HandleFunc("GET /api/status", s.authed(s.statusAPI))
	mux.HandleFunc("GET /peers/new", s.authed(s.peerNewPage))
	mux.HandleFunc("POST /peers/new", s.authed(s.peerNewPost))
	mux.HandleFunc("GET /config", s.authed(s.configPage))
	mux.HandleFunc("POST /config/review", s.authed(s.reviewPost))
	mux.HandleFunc("GET /peers/edit", s.authed(s.peerEditPage))
	mux.HandleFunc("POST /peers/edit", s.authed(s.peerEditPost))
	mux.HandleFunc("GET /peers/delete", s.authed(s.peerDeletePage))
	mux.HandleFunc("POST /peers/delete", s.authed(s.peerDeletePost))
	mux.HandleFunc("GET /peers/toggle", s.authed(s.peerTogglePage))
	mux.HandleFunc("POST /peers/toggle", s.authed(s.peerTogglePost))
	mux.HandleFunc("GET /peers/comment", s.authed(s.peerCommentPage))
	mux.HandleFunc("POST /peers/comment", s.authed(s.peerCommentPost))
	mux.HandleFunc("POST /config/apply", s.authed(s.applyPost))
	mux.HandleFunc("POST /config/restore", s.authed(s.restorePost))
	mux.HandleFunc("GET /routes", s.authed(s.routesPage))
	mux.HandleFunc("GET /routes/new", s.authed(s.routeNewPage))
	mux.HandleFunc("POST /routes/new", s.authed(s.routeNewPost))
	mux.HandleFunc("GET /routes/act", s.authed(s.routeActPage))
	mux.HandleFunc("POST /routes/act", s.authed(s.routeActPost))
	mux.HandleFunc("GET /routes/comment", s.authed(s.routeCommentPage))
	mux.HandleFunc("POST /routes/comment", s.authed(s.routeCommentPost))
	mux.HandleFunc("GET /logs", s.authed(s.logsPage))
	mux.HandleFunc("GET /terminal", s.authed(s.termPage))
	mux.HandleFunc("POST /term/new", s.authed(s.termNew))
	mux.HandleFunc("GET /term/out", s.authed(s.termOut))
	mux.HandleFunc("POST /term/in", s.authed(s.termForward("in", "id", "d")))
	mux.HandleFunc("POST /term/resize", s.authed(s.termForward("resize", "id", "cols", "rows")))
	mux.HandleFunc("POST /term/close", s.authed(s.termForward("close", "id")))

	srv := &http.Server{
		Addr:              opt.Listen,
		Handler:           secure(mux),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      6 * time.Minute, // apply may run bgpq4/PeeringDB
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    32 << 10,
	}
	if opt.TLSCert != "" {
		return srv.ListenAndServeTLS(opt.TLSCert, opt.TLSKey)
	}
	return srv.ListenAndServe()
}

// secure adds strict security headers and caps request bodies.
func secure(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hd := w.Header()
		sty := "'self'"
		if r.URL.Path == "/terminal" {
			sty = "'self' 'unsafe-inline'" // xterm.js injects its renderer styles
		}
		hd.Set("Content-Security-Policy", "default-src 'none'; style-src "+sty+"; script-src 'self'; connect-src 'self'; img-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
		hd.Set("X-Content-Type-Options", "nosniff")
		hd.Set("X-Frame-Options", "DENY")
		hd.Set("Referrer-Policy", "no-referrer")
		hd.Set("Cache-Control", "no-store")
		hd.Set("X-Robots-Tag", "noindex, nofollow")
		r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
		h.ServeHTTP(w, r)
	})
}

type ctxPage struct {
	Title   string
	Nav     string
	User    string
	CSRF    string
	Router  routerInfo
	Version string
	VPP     bool
	Data    any
}

func (s *server) render(w http.ResponseWriter, name string, p ctxPage) {
	p.Router = s.router()
	p.VPP = vppInstalled()
	if p.Version == "" {
		_, v, err := bird.RunCommand("show status", p.Router.Socket)
		if err == nil {
			p.Version = "BIRD " + v
		} else {
			p.Version = "BIRD unreachable"
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tpl[name].ExecuteTemplate(w, "layout", p); err != nil {
		http.Error(w, "template error", http.StatusInternalServerError)
	}
}

type handler func(w http.ResponseWriter, r *http.Request, sess *session)

func (s *server) authed(h handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, _ := s.auth.get(r)
		if sess == nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		if r.Method == http.MethodPost && !csrfOK(sess, r) {
			http.Error(w, "invalid CSRF token, reload the page", http.StatusForbidden)
			return
		}
		h(w, r, sess)
	}
}

func (s *server) cookie(r *http.Request, value string, maxAge int) *http.Cookie {
	secure := r.TLS != nil || (s.opt.TrustProxy && r.Header.Get("X-Forwarded-Proto") == "https")
	return &http.Cookie{Name: sessionCookie, Value: value, Path: "/", MaxAge: maxAge, HttpOnly: true, Secure: secure, SameSite: http.SameSiteStrictMode}
}

// ---- login ----

func (s *server) loginPage(w http.ResponseWriter, r *http.Request) {
	s.render(w, "login", ctxPage{Title: "Sign in", Version: "-"})
}

func (s *server) loginPost(w http.ResponseWriter, r *http.Request) {
	ip := s.auth.clientIP(r)
	if s.auth.blocked(ip) {
		w.WriteHeader(http.StatusTooManyRequests)
		s.render(w, "login", ctxPage{Title: "Sign in", Version: "-", Data: "Too many attempts. Try again in 10 minutes."})
		return
	}
	_, tok, ok := s.auth.login(ip, r.PostFormValue("user"), r.PostFormValue("password"))
	if !ok {
		w.WriteHeader(http.StatusUnauthorized)
		s.render(w, "login", ctxPage{Title: "Sign in", Version: "-", Data: "Wrong user or password."})
		return
	}
	http.SetCookie(w, s.cookie(r, tok, int(sessionMax.Seconds())))
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *server) logoutPost(w http.ResponseWriter, r *http.Request, _ *session) {
	if _, tok := s.auth.get(r); tok != "" {
		s.auth.logout(tok)
	}
	http.SetCookie(w, s.cookie(r, "", -1))
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// ---- peers ----

type peerRow struct {
	Name, Proto, ASN, Neighbor, State, Info, Since, Desc string
	Off                                                  bool   // disabled in the config file
	Class                                                string // "", "w", "d"
	Imported, Exported                                   int
}

type summary struct {
	Up, Down, R4, R6 int
	Last             string
}

var reCount = regexp.MustCompile(`(\d+) of \d+ routes for \d+ networks in table master([46])`)

func (s *server) peersPage(w http.ResponseWriter, r *http.Request, sess *session) {
	ri := s.router()
	var rows []peerRow
	var sum summary
	var errMsg string
	out, _, err := bird.RunCommand("show protocols all", ri.Socket)
	if err != nil {
		errMsg = "Cannot reach BIRD at " + ri.Socket + ": " + err.Error()
	} else {
		names := map[string]struct{ Name string }{}
		if b, err := os.ReadFile(filepath.Join(ri.BIRDDir, "protocols.json")); err == nil {
			_ = json.Unmarshal(b, &names)
		}
		cfgPeers := s.peerMeta()
		states, err := bird.ParseProtocols(out)
		if err != nil {
			errMsg = "Parse BIRD output: " + err.Error()
		}
		for _, p := range states {
			if p.BGP == nil {
				continue
			}
			row := peerRow{Proto: p.Name, Name: p.Name, ASN: strconv.Itoa(p.BGP.NeighborAS), Neighbor: p.BGP.NeighborAddress,
				State: p.State, Info: p.Info, Since: p.Since, Imported: -1, Exported: -1}
			if n, ok := names[p.Name]; ok && n.Name != "" {
				row.Name = n.Name
			}
			if p.Routes != nil {
				row.Imported, row.Exported = p.Routes.Imported, p.Routes.Exported
			}
			if m, ok := cfgPeers[row.Name]; ok {
				row.Off, row.Desc = m.off, m.desc
			}
			switch {
			case p.Info == "Established":
				sum.Up++
			case p.State == "down":
				row.Class = "d"
				sum.Down++
			default:
				row.Class = "w"
				sum.Down++
			}
			rows = append(rows, row)
		}
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
		if c, _, err := bird.RunCommand("show route count", ri.Socket); err == nil {
			for _, m := range reCount.FindAllStringSubmatch(c, -1) {
				n, _ := strconv.Atoi(m[1])
				if m[2] == "4" {
					sum.R4 += n
				} else {
					sum.R6 += n
				}
			}
		}
	}
	if h := s.store.History(20); len(h) > 0 {
		for _, ev := range h {
			if ev.Action != "validate" && ev.OK {
				sum.Last = ev.Time.Format("02 Jan 15:04")
				break
			}
		}
	}
	s.render(w, "peers", ctxPage{Title: "Peers", Nav: "peers", User: sess.user, CSRF: sess.csrf,
		Data: map[string]any{"Rows": rows, "Sum": sum, "Err": errMsg}})
}

// ---- policy rules (tri-state per peer: inherit / on / off) ----

type ruleDef struct {
	Key, Label, Hint string
	Def              bool // global default when neither peer nor template sets it
}

var peerRules = []ruleDef{
	{"filter-bogon-routes", "Bogon prefixes", "Reject RFC1918, reserved and documentation prefixes", true},
	{"filter-bogon-asns", "Bogon ASNs", "Reject paths containing private or reserved ASNs", true},
	{"filter-rpki", "RPKI invalid", "Drop routes with RPKI status invalid", true},
	{"strict-rpki", "Strict RPKI", "Accept RPKI valid only", false},
	{"filter-irr", "IRR prefix filter", "Only accept prefixes registered in the peer's IRR as-set", false},
	{"filter-as-set", "AS-SET origin", "Reject routes not originated by an ASN in the peer's AS-SET", false},
	{"filter-max-prefix", "Max-prefix", "Enforce the import limit on this session", true},
	{"filter-prefix-length", "Prefix length", "Reject IPv4 outside /8-/24 and IPv6 outside /12-/48", true},
	{"filter-transit-asns", "Transit ASNs (Peerlock Lite)", "Reject paths containing transit-free ASNs", false},
	{"enforce-first-as", "Enforce first AS", "First AS in the path must be the peer's ASN", true},
	{"remove-private-asns", "Remove private ASNs", "Strip private ASNs from the path on export", true},
	{"filter-blocklist", "Blocklist", "Reject ASNs and prefixes in the global blocklist", true},
}

func ruleKeys() []string {
	k := make([]string, len(peerRules))
	for i, r := range peerRules {
		k[i] = "r_" + r.Key
	}
	return k
}

func peerFormData(f map[string]string, errMsg string) map[string]any {
	if f == nil {
		f = map[string]string{}
	}
	d := map[string]any{"Rules": peerRules, "F": f}
	if errMsg != "" {
		d["Err"] = errMsg
	}
	return d
}

// ---- add peer ----

var reName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._\-]{0,63}$`)

func (s *server) peerNewPage(w http.ResponseWriter, r *http.Request, sess *session) {
	s.render(w, "peer_new", ctxPage{Title: "Add peer", Nav: "peers", User: sess.user, CSRF: sess.csrf, Data: peerFormData(nil, "")})
}

func formFromRequest(r *http.Request) map[string]string {
	f := map[string]string{}
	for _, k := range append([]string{"name", "asn", "neighbors", "description", "template", "limit4", "limit6", "asset"}, ruleKeys()...) {
		f[k] = strings.TrimSpace(r.PostFormValue(k))
	}
	return f
}

func (s *server) peerNewPost(w http.ResponseWriter, r *http.Request, sess *session) {
	if r.PostFormValue("mode") == "raw" {
		s.peerNewRaw(w, r, sess)
		return
	}
	f := formFromRequest(r)
	fail := func(msg string) {
		s.render(w, "peer_new", ctxPage{Title: "Add peer", Nav: "peers", User: sess.user, CSRF: sess.csrf, Data: peerFormData(f, msg)})
	}
	peer, msg := buildPeer(f)
	if msg != "" {
		fail(msg)
		return
	}
	cur, err := s.store.current()
	if err != nil {
		fail("Read config: " + err.Error())
		return
	}
	cand, err := insertPeer(cur, f["name"], peer)
	if err != nil {
		fail(err.Error())
		return
	}
	s.review(w, sess, cur, cand, "Add peer "+f["name"])
}

// ---- edit peer ----

func findPeers(root *yaml.Node) *yaml.Node {
	if len(root.Content) == 0 || root.Content[0].Kind != yaml.MappingNode {
		return nil
	}
	top := root.Content[0]
	for i := 0; i+1 < len(top.Content); i += 2 {
		if top.Content[i].Value == "peers" && top.Content[i+1].Kind == yaml.MappingNode {
			return top.Content[i+1]
		}
	}
	return nil
}

func findPeer(root *yaml.Node, name string) *yaml.Node {
	ps := findPeers(root)
	if ps == nil {
		return nil
	}
	for i := 0; i+1 < len(ps.Content); i += 2 {
		if ps.Content[i].Value == name && ps.Content[i+1].Kind == yaml.MappingNode {
			return ps.Content[i+1]
		}
	}
	return nil
}

// formFromPeer turns an existing peer node into form values.
func formFromPeer(name string, n *yaml.Node) map[string]string {
	f := map[string]string{"name": name}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i].Value, n.Content[i+1]
		switch k {
		case "asn":
			f["asn"] = v.Value
		case "description":
			f["description"] = v.Value
		case "template":
			f["template"] = v.Value
		case "as-set":
			f["asset"] = v.Value
		case "import-limit4":
			f["limit4"] = v.Value
		case "import-limit6":
			f["limit6"] = v.Value
		case "neighbors":
			var l []string
			for _, c := range v.Content {
				l = append(l, c.Value)
			}
			f["neighbors"] = strings.Join(l, ", ")
		default:
			for _, rd := range peerRules {
				if rd.Key == k {
					if v.Value == "true" {
						f["r_"+k] = "on"
					} else if v.Value == "false" {
						f["r_"+k] = "off"
					}
				}
			}
		}
	}
	return f
}

func editFormData(f map[string]string, msg string) map[string]any {
	d := peerFormData(f, msg)
	d["Edit"] = true
	return d
}

func (s *server) peerEditPage(w http.ResponseWriter, r *http.Request, sess *session) {
	name := r.URL.Query().Get("name")
	page := func(d map[string]any) {
		s.render(w, "peer_new", ctxPage{Title: "Edit peer", Nav: "peers", User: sess.user, CSRF: sess.csrf, Data: d})
	}
	cur, err := s.store.current()
	if err != nil {
		page(editFormData(map[string]string{"name": name}, "Read config: "+err.Error()))
		return
	}
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(cur), &root); err != nil {
		page(editFormData(map[string]string{"name": name}, "Current config is not valid YAML: "+err.Error()))
		return
	}
	n := findPeer(&root, name)
	if n == nil {
		page(editFormData(map[string]string{"name": name}, "Peer "+name+" is not defined in "+s.opt.ConfigFile+" (it may come from a template or another file)."))
		return
	}
	page(editFormData(formFromPeer(name, n), ""))
}

func (s *server) peerEditPost(w http.ResponseWriter, r *http.Request, sess *session) {
	f := formFromRequest(r)
	fail := func(msg string) {
		s.render(w, "peer_new", ctxPage{Title: "Edit peer", Nav: "peers", User: sess.user, CSRF: sess.csrf, Data: editFormData(f, msg)})
	}
	peer, msg := buildPeer(f)
	if msg != "" {
		fail(msg)
		return
	}
	cur, err := s.store.current()
	if err != nil {
		fail("Read config: " + err.Error())
		return
	}
	cand, err := replacePeer(cur, f["name"], peer)
	if err != nil {
		fail(err.Error())
		return
	}
	s.review(w, sess, cur, cand, "Edit peer "+f["name"])
}

// replacePeer updates the form-managed keys of an existing peer in place and keeps every other key.
func replacePeer(doc, name string, peer *yaml.Node) (string, error) {
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(doc), &root); err != nil {
		return "", fmt.Errorf("current config is not valid YAML: %w", err)
	}
	old := findPeer(&root, name)
	if old == nil {
		return "", fmt.Errorf("peer %q is not defined in the config file", name)
	}
	managed := formKeys()
	var kept []*yaml.Node
	for i := 0; i+1 < len(old.Content); i += 2 {
		if !managed[old.Content[i].Value] {
			kept = append(kept, old.Content[i], old.Content[i+1])
		}
	}
	old.Content = append(append([]*yaml.Node{}, peer.Content...), kept...)
	var b strings.Builder
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(&root); err != nil {
		return "", err
	}
	return b.String(), nil
}

// buildPeer validates the form values and returns the YAML node for the peer.
// On a validation problem it returns a message for the user.
func buildPeer(f map[string]string) (*yaml.Node, string) {
	if !reName.MatchString(f["name"]) {
		return nil, "Name: letters, digits, space, dot, dash or underscore (max 64)."
	}
	asn, err := strconv.ParseUint(strings.TrimPrefix(strings.ToUpper(f["asn"]), "AS"), 10, 32)
	if err != nil || asn == 0 {
		return nil, "ASN must be a number between 1 and 4294967295."
	}
	var neigh []string
	for _, n := range strings.FieldsFunc(f["neighbors"], func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\r' }) {
		a, err := netip.ParseAddr(n)
		if err != nil {
			return nil, "Neighbor " + n + " is not an IP address."
		}
		neigh = append(neigh, a.String())
	}
	if len(neigh) == 0 {
		return nil, "At least one neighbor IP is required."
	}
	if len(f["description"]) > 120 || strings.ContainsAny(f["description"], "\"\n\r") {
		return nil, "Description: max 120 characters, no double quotes."
	}
	peer := yaml.Node{Kind: yaml.MappingNode}
	add := func(k string, v *yaml.Node) {
		peer.Content = append(peer.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: k}, v)
	}
	scalar := func(v string, tag string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Value: v, Tag: tag} }
	if f["template"] != "" {
		if !reName.MatchString(f["template"]) {
			return nil, "Invalid template name."
		}
		add("template", scalar(f["template"], "!!str"))
	}
	add("asn", scalar(strconv.FormatUint(asn, 10), "!!int"))
	if f["description"] != "" {
		add("description", scalar(f["description"], "!!str"))
	}
	if f["asset"] != "" {
		if !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9:_.\-]{0,127}$`).MatchString(f["asset"]) {
			return nil, "Invalid AS-SET."
		}
		add("as-set", scalar(f["asset"], "!!str"))
	}
	for _, l := range []string{"limit4", "limit6"} {
		if f[l] != "" {
			n, err := strconv.Atoi(f[l])
			if err != nil || n < 1 || n > 5_000_000 {
				return nil, "Import limits must be between 1 and 5,000,000."
			}
			add("import-"+l, scalar(strconv.Itoa(n), "!!int"))
		}
	}
	for _, rd := range peerRules {
		switch f["r_"+rd.Key] {
		case "", "inherit":
		case "on":
			add(rd.Key, scalar("true", "!!bool"))
		case "off":
			add(rd.Key, scalar("false", "!!bool"))
		default:
			return nil, "Invalid value for " + rd.Label + "."
		}
	}
	nl := &yaml.Node{Kind: yaml.SequenceNode}
	for _, n := range neigh {
		nl.Content = append(nl.Content, scalar(n, "!!str"))
	}
	add("neighbors", nl)
	return &peer, ""
}

// formKeys are the peer keys the form manages; everything else in a peer is left untouched on edit.
func formKeys() map[string]bool {
	m := map[string]bool{"template": true, "asn": true, "description": true, "as-set": true, "import-limit4": true, "import-limit6": true, "neighbors": true}
	for _, r := range peerRules {
		m[r.Key] = true
	}
	return m
}

// insertPeer adds a peer under "peers:" keeping the rest of the document.
func insertPeer(doc, name string, peer *yaml.Node) (string, error) {
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(doc), &root); err != nil {
		return "", fmt.Errorf("current config is not valid YAML: %w", err)
	}
	if len(root.Content) == 0 || root.Content[0].Kind != yaml.MappingNode {
		return "", fmt.Errorf("current config has no top-level mapping")
	}
	top := root.Content[0]
	var peers *yaml.Node
	for i := 0; i+1 < len(top.Content); i += 2 {
		if top.Content[i].Value == "peers" {
			peers = top.Content[i+1]
		}
	}
	if peers == nil {
		peers = &yaml.Node{Kind: yaml.MappingNode}
		top.Content = append(top.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "peers"}, peers)
	}
	if peers.Kind != yaml.MappingNode {
		return "", fmt.Errorf("peers is not a mapping")
	}
	for i := 0; i < len(peers.Content); i += 2 {
		if strings.EqualFold(peers.Content[i].Value, name) {
			return "", fmt.Errorf("a peer named %q already exists", name)
		}
	}
	peers.Content = append(peers.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: name}, peer)
	var b strings.Builder
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(&root); err != nil {
		return "", err
	}
	return b.String(), nil
}

// ---- config / review / apply ----

func (s *server) configPage(w http.ResponseWriter, r *http.Request, sess *session) {
	cur, err := s.store.current()
	snaps, _ := s.store.Snapshots()
	if len(snaps) > 15 {
		snaps = snaps[:15]
	}
	d := map[string]any{"Text": cur, "Snaps": snaps, "File": s.opt.ConfigFile}
	if err != nil {
		d["Err"] = err.Error()
	}
	s.render(w, "config", ctxPage{Title: "Config", Nav: "config", User: sess.user, CSRF: sess.csrf, Data: d})
}

func (s *server) review(w http.ResponseWriter, sess *session, cur, cand, what string) {
	diff := Diff(cur, cand, 3)
	out, ok := "", true
	if diff != nil {
		out, ok = s.store.Validate(cand)
	}
	s.render(w, "review", ctxPage{Title: "Review", Nav: "config", User: sess.user, CSRF: sess.csrf,
		Data: map[string]any{"What": what, "Diff": diff, "Out": out, "OK": ok, "Cand": cand}})
}

func (s *server) reviewPost(w http.ResponseWriter, r *http.Request, sess *session) {
	cur, err := s.store.current()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	cand := strings.ReplaceAll(r.PostFormValue("text"), "\r\n", "\n")
	s.review(w, sess, cur, cand, "Edit config")
}

func (s *server) restorePost(w http.ResponseWriter, r *http.Request, sess *session) {
	cur, err := s.store.current()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	name := r.PostFormValue("snap")
	cand, err := s.store.ReadSnapshot(name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.review(w, sess, cur, cand, "Restore snapshot "+name)
}

func (s *server) applyPost(w http.ResponseWriter, r *http.Request, sess *session) {
	cand := strings.ReplaceAll(r.PostFormValue("text"), "\r\n", "\n")
	what := r.PostFormValue("what")
	action := "apply"
	if strings.HasPrefix(what, "Restore") {
		action = "rollback"
	}
	ev := s.store.Apply(sess.user, cand, action)
	s.render(w, "logs", ctxPage{Title: "Logs", Nav: "logs", User: sess.user, CSRF: sess.csrf,
		Data: map[string]any{"Events": s.store.History(50), "Flash": ev}})
}

// ---- routes ----

var reASN = regexp.MustCompile(`^(?i:AS)?(\d{1,10})$`)

func (s *server) routesPage(w http.ResponseWriter, r *http.Request, sess *session) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	d := map[string]any{"Q": q}
	if q != "" {
		var cmd string
		if a, err := netip.ParseAddr(q); err == nil {
			cmd = "show route for " + a.String() + " all"
		} else if p, err := netip.ParsePrefix(q); err == nil {
			cmd = "show route for " + p.Masked().String() + " all"
		} else if m := reASN.FindStringSubmatch(q); m != nil {
			if n, err := strconv.ParseUint(m[1], 10, 32); err == nil {
				cmd = fmt.Sprintf("show route where bgp_path.last = %d", n)
			}
		}
		if cmd == "" {
			d["Err"] = "Enter an IP address, a prefix or an AS number."
		} else {
			out, _, err := bird.RunCommand(cmd, s.router().Socket)
			if err != nil {
				d["Err"] = err.Error()
			} else {
				lines := strings.Split(strings.TrimSpace(out), "\n")
				if len(lines) > 500 {
					lines = append(lines[:500], fmt.Sprintf("… %d more lines not shown", len(lines)-500))
				}
				d["Out"] = strings.Join(lines, "\n")
				d["Cmd"] = cmd
			}
		}
	}
	view := r.URL.Query().Get("v")
	if view != "kernel" {
		view = "bird"
	}
	d["View"] = view
	var all []routeRow
	var tot int
	if view == "kernel" {
		rows, n, err := kernelRoutes()
		if err != nil {
			d["TblErr"] = "ip route: " + err.Error()
		}
		all, tot = rows, n
	} else {
		for _, tb := range []string{"master4", "master6"} {
			rows, n, err := birdRoutes(s.router().Socket, tb)
			if err != nil {
				d["TblErr"] = "BIRD: " + err.Error()
				break
			}
			all = append(all, rows...)
			tot += n
		}
	}
	if view != "kernel" {
		all = s.annotateStatics(all)
	}
	d["Rows"], d["Total"] = all, tot
	if tot > len(all) {
		d["More"] = tot - len(all)
	}
	s.render(w, "routes", ctxPage{Title: "Routes", Nav: "routes", User: sess.user, CSRF: sess.csrf, Data: d})
}

func (s *server) logsPage(w http.ResponseWriter, r *http.Request, sess *session) {
	s.render(w, "logs", ctxPage{Title: "Logs", Nav: "logs", User: sess.user, CSRF: sess.csrf,
		Data: map[string]any{"Events": s.store.History(100)}})
}

// ---- delete peer ----

func (s *server) peerDeletePage(w http.ResponseWriter, r *http.Request, sess *session) {
	s.render(w, "peer_delete", ctxPage{Title: "Delete peer", Nav: "peers", User: sess.user, CSRF: sess.csrf, Data: map[string]any{"Name": r.URL.Query().Get("name"), "Err": ""}})
}

func (s *server) peerDeletePost(w http.ResponseWriter, r *http.Request, sess *session) {
	name := strings.TrimSpace(r.PostFormValue("name"))
	fail := func(msg string) {
		s.render(w, "peer_delete", ctxPage{Title: "Delete peer", Nav: "peers", User: sess.user, CSRF: sess.csrf, Data: map[string]any{"Name": name, "Err": msg}})
	}
	cur, err := s.store.current()
	if err != nil {
		fail("Read config: " + err.Error())
		return
	}
	cand, err := removePeer(cur, name)
	if err != nil {
		fail(err.Error())
		return
	}
	s.review(w, sess, cur, cand, "Delete peer "+name)
}

// removePeer drops one peer from the peers map and leaves everything else as is.
func removePeer(doc, name string) (string, error) {
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(doc), &root); err != nil {
		return "", fmt.Errorf("current config is not valid YAML: %w", err)
	}
	ps := findPeers(&root)
	if ps == nil {
		return "", fmt.Errorf("no peers section in the config file")
	}
	for i := 0; i+1 < len(ps.Content); i += 2 {
		if ps.Content[i].Value == name {
			ps.Content = append(ps.Content[:i], ps.Content[i+2:]...)
			var b strings.Builder
			enc := yaml.NewEncoder(&b)
			enc.SetIndent(2)
			if err := enc.Encode(&root); err != nil {
				return "", err
			}
			return b.String(), nil
		}
	}
	return "", fmt.Errorf("peer %q is not defined in the config file (it may come from a template or another file)", name)
}

// ---- enable / disable / comment ----

type peerMeta struct {
	off  bool
	desc string
}

// peerMeta reads disabled/description of every peer defined in the config file.
func (s *server) peerMeta() map[string]peerMeta {
	m := map[string]peerMeta{}
	cur, err := s.store.current()
	if err != nil {
		return m
	}
	var root yaml.Node
	if yaml.Unmarshal([]byte(cur), &root) != nil {
		return m
	}
	ps := findPeers(&root)
	if ps == nil {
		return m
	}
	for i := 0; i+1 < len(ps.Content); i += 2 {
		pm := peerMeta{}
		n := ps.Content[i+1]
		for j := 0; n.Kind == yaml.MappingNode && j+1 < len(n.Content); j += 2 {
			switch n.Content[j].Value {
			case "disabled":
				pm.off = n.Content[j+1].Value == "true"
			case "description":
				pm.desc = n.Content[j+1].Value
			}
		}
		m[ps.Content[i].Value] = pm
	}
	return m
}

// setPeerKey sets (or, with val == nil, removes) one key of a peer and keeps everything else.
func setPeerKey(doc, name, key string, val *yaml.Node) (string, error) {
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(doc), &root); err != nil {
		return "", fmt.Errorf("current config is not valid YAML: %w", err)
	}
	n := findPeer(&root, name)
	if n == nil {
		return "", fmt.Errorf("peer %q is not defined in %s (it may come from a template or another file)", name, "the config file")
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			if val == nil {
				n.Content = append(n.Content[:i], n.Content[i+2:]...)
			} else {
				n.Content[i+1] = val
			}
			goto enc
		}
	}
	if val != nil {
		n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, val)
	}
enc:
	var b strings.Builder
	e := yaml.NewEncoder(&b)
	e.SetIndent(2)
	if err := e.Encode(&root); err != nil {
		return "", err
	}
	return b.String(), nil
}

func (s *server) peerTogglePage(w http.ResponseWriter, r *http.Request, sess *session) {
	s.render(w, "peer_toggle", ctxPage{Title: "Peer", Nav: "peers", User: sess.user, CSRF: sess.csrf,
		Data: map[string]any{"Name": r.URL.Query().Get("name"), "Off": r.URL.Query().Get("to") == "disable", "Err": ""}})
}

func (s *server) peerTogglePost(w http.ResponseWriter, r *http.Request, sess *session) {
	name := strings.TrimSpace(r.PostFormValue("name"))
	off := r.PostFormValue("to") == "disable"
	fail := func(msg string) {
		s.render(w, "peer_toggle", ctxPage{Title: "Peer", Nav: "peers", User: sess.user, CSRF: sess.csrf, Data: map[string]any{"Name": name, "Off": off, "Err": msg}})
	}
	cur, err := s.store.current()
	if err != nil {
		fail("Read config: " + err.Error())
		return
	}
	var val *yaml.Node
	if off {
		val = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"}
	}
	cand, err := setPeerKey(cur, name, "disabled", val)
	if err != nil {
		fail(err.Error())
		return
	}
	what := "Enable peer "
	if off {
		what = "Disable peer "
	}
	s.review(w, sess, cur, cand, what+name)
}

func (s *server) peerCommentPage(w http.ResponseWriter, r *http.Request, sess *session) {
	name := r.URL.Query().Get("name")
	s.render(w, "peer_comment", ctxPage{Title: "Comment", Nav: "peers", User: sess.user, CSRF: sess.csrf,
		Data: map[string]any{"Name": name, "Desc": s.peerMeta()[name].desc, "Err": ""}})
}

func (s *server) peerCommentPost(w http.ResponseWriter, r *http.Request, sess *session) {
	name := strings.TrimSpace(r.PostFormValue("name"))
	desc := strings.TrimSpace(r.PostFormValue("description"))
	fail := func(msg string) {
		s.render(w, "peer_comment", ctxPage{Title: "Comment", Nav: "peers", User: sess.user, CSRF: sess.csrf, Data: map[string]any{"Name": name, "Desc": desc, "Err": msg}})
	}
	if len(desc) > 120 || strings.ContainsAny(desc, "\"\n\r") {
		fail("Comment: max 120 characters, no double quotes.")
		return
	}
	cur, err := s.store.current()
	if err != nil {
		fail("Read config: " + err.Error())
		return
	}
	var val *yaml.Node
	if desc != "" {
		val = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: desc, Style: yaml.DoubleQuotedStyle}
	}
	cand, err := setPeerKey(cur, name, "description", val)
	if err != nil {
		fail(err.Error())
		return
	}
	s.review(w, sess, cur, cand, "Comment peer "+name)
}

// peerNewRaw adds one or more peers from raw YAML typed by the user. Accepted: "Name: {asn: .., neighbors: [..]}"
// blocks, optionally wrapped in a top-level "peers:" key. Goes through the same review/dry-run/apply as the form.
func (s *server) peerNewRaw(w http.ResponseWriter, r *http.Request, sess *session) {
	raw := strings.TrimRight(r.PostFormValue("raw"), " \t\r\n")
	fail := func(msg string) {
		d := peerFormData(nil, msg)
		d["Raw"], d["Mode"] = raw, "raw"
		s.render(w, "peer_new", ctxPage{Title: "Add peer", Nav: "peers", User: sess.user, CSRF: sess.csrf, Data: d})
	}
	if strings.TrimSpace(raw) == "" {
		fail("Syntax is empty.")
		return
	}
	if len(raw) > 64<<10 {
		fail("Syntax is too large (max 64 KiB).")
		return
	}
	var n yaml.Node
	if err := yaml.Unmarshal([]byte(raw), &n); err != nil {
		fail("Invalid YAML: " + err.Error())
		return
	}
	if len(n.Content) == 0 || n.Content[0].Kind != yaml.MappingNode {
		fail("Expected a mapping: peer name, then its settings (see the example).")
		return
	}
	m := n.Content[0]
	if len(m.Content) == 2 && m.Content[0].Value == "peers" && m.Content[1].Kind == yaml.MappingNode {
		m = m.Content[1]
	}
	if len(m.Content) == 0 {
		fail("No peer found in the syntax.")
		return
	}
	cur, err := s.store.current()
	if err != nil {
		fail("Read config: " + err.Error())
		return
	}
	var names []string
	cand := cur
	for i := 0; i+1 < len(m.Content); i += 2 {
		name, peer := strings.TrimSpace(m.Content[i].Value), m.Content[i+1]
		if name == "" || len(name) > 64 || strings.ContainsAny(name, "\"\n\r") {
			fail("Invalid peer name.")
			return
		}
		if peer.Kind != yaml.MappingNode {
			fail("Peer " + name + ": settings must be a mapping (asn, neighbors, ...).")
			return
		}
		hasASN, hasNb := false, false
		for j := 0; j+1 < len(peer.Content); j += 2 {
			switch peer.Content[j].Value {
			case "asn":
				hasASN = peer.Content[j+1].Value != ""
			case "neighbors":
				hasNb = len(peer.Content[j+1].Content) > 0
			}
		}
		if !hasASN || !hasNb {
			fail("Peer " + name + ": asn and neighbors are required.")
			return
		}
		if cand, err = insertPeer(cand, name, peer); err != nil {
			fail(err.Error())
			return
		}
		names = append(names, name)
	}
	s.review(w, sess, cur, cand, "Add peer "+strings.Join(names, ", ")+" (syntax)")
}
