package web

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Kernel route editing: the web service only validates the session and forwards structured
// fields to the root net helper (net_helper.go), which re-validates everything.

func (s *server) netClient() *http.Client {
	return &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "unix", s.opt.NetSocket)
	}}, Timeout: 15 * time.Second}
}

// netCall posts to the helper; msg is the error text to show ("" on success), code the HTTP status.
func (s *server) netCall(path string, v url.Values) (msg string, code int) {
	resp, err := s.netClient().PostForm("http://helper"+path, v)
	if err != nil {
		return "The network helper is not running. Start it with: systemctl enable --now boxvector-net", 502
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	var m struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(b, &m)
	return m.Error, resp.StatusCode
}

// managedRoutes returns the helper's state (disabled routes and comments); nil when unavailable.
func (s *server) managedRoutes() []kroute {
	resp, err := s.netClient().Get("http://helper/routes")
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	var l []kroute
	_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&l)
	return l
}

// mergeKernel marks editable kernel rows, attaches comments and appends disabled routes.
func (s *server) mergeKernel(rows []routeRow) []routeRow {
	st := s.managedRoutes()
	by := map[string]kroute{}
	for _, k := range st {
		by[k.key()] = k
	}
	for i := range rows {
		r := &rows[i]
		if r.Type != "unicast" || (r.Via == "" && r.Dev == "") {
			continue
		}
		m, _ := strconv.ParseUint(r.Pref, 10, 32)
		r.K, r.Metric = true, uint32(m)
		if k, ok := by[kroute{Dst: r.Prefix, Via: r.Via, Dev: r.Dev, Metric: uint32(m)}.key()]; ok {
			r.Comment = k.Comment
		}
	}
	for _, k := range st {
		if k.Off {
			rows = append(rows, routeRow{Prefix: k.Dst, Type: "unicast", Via: k.Via, Dev: k.Dev, Pref: strconv.FormatUint(uint64(k.Metric), 10), K: true, Off: true, Metric: k.Metric, Comment: k.Comment})
		}
	}
	return rows
}

func kform(r *http.Request) map[string]string {
	g := func(k string) string {
		if v := r.PostFormValue(k); v != "" {
			return strings.TrimSpace(v)
		}
		return strings.TrimSpace(r.URL.Query().Get(k))
	}
	f := map[string]string{}
	for _, k := range []string{"dst", "via", "dev", "metric", "comment", "do"} {
		f[k] = g(k)
	}
	return f
}

func (s *server) kroutePage(w http.ResponseWriter, sess *session, f map[string]string, data map[string]any) {
	data["F"] = f
	s.render(w, "kroute", ctxPage{Title: "Routes", Nav: "routes", User: sess.user, CSRF: sess.csrf, Data: data})
}

func (s *server) kGate(w http.ResponseWriter, sess *session) bool {
	if !canTerminal(sess.user) {
		http.Error(w, "editing kernel routes is limited to root and members of the sudo group", http.StatusForbidden)
		return false
	}
	return true
}

var kActs = map[string][3]string{ // do -> title, text, danger
	"del":     {"Remove route", "Remove this kernel route now. It is also forgotten by boxvector, so it stays gone until something re-adds it.", "1"},
	"disable": {"Disable route", "Take this route out of the kernel now and keep it here so you can enable it again.", "1"},
	"enable":  {"Enable route", "Put this saved route back into the kernel.", ""},
}

func (s *server) kRouteActPage(w http.ResponseWriter, r *http.Request, sess *session) {
	if !s.kGate(w, sess) {
		return
	}
	f := kform(r)
	a, ok := kActs[f["do"]]
	if !ok {
		http.Error(w, "unknown action", http.StatusBadRequest)
		return
	}
	s.kroutePage(w, sess, f, map[string]any{"Mode": "act", "Title": a[0], "Text": a[1], "Danger": a[2] != ""})
}

func (s *server) kRouteActPost(w http.ResponseWriter, r *http.Request, sess *session) {
	if !s.kGate(w, sess) {
		return
	}
	f := kform(r)
	a, ok := kActs[f["do"]]
	if !ok {
		http.Error(w, "unknown action", http.StatusBadRequest)
		return
	}
	s.kSend(w, r, sess, f, f["do"], a[0], map[string]any{"Mode": "act", "Title": a[0], "Text": a[1], "Danger": a[2] != ""})
}

func (s *server) kSend(w http.ResponseWriter, r *http.Request, sess *session, f map[string]string, op, title string, page map[string]any) {
	v := url.Values{"op": {op}, "dst": {f["dst"]}, "via": {f["via"]}, "dev": {f["dev"]}, "metric": {f["metric"]},
		"comment": {f["comment"]}, "client": {s.auth.clientIP(r)}}
	if r.PostFormValue("force") == "1" {
		v.Set("force", "1")
	}
	msg, code := s.netCall("/route", v)
	sum := title + " " + f["dst"]
	if f["via"] != "" {
		sum += " via " + f["via"]
	}
	if f["dev"] != "" {
		sum += " dev " + f["dev"]
	}
	s.store.log(Event{Time: time.Now(), User: sess.user, Action: "kernel route", OK: msg == "", Summary: sum, Output: msg})
	if msg != "" {
		page["Err"] = msg
		page["Force"] = code == http.StatusConflict
		s.kroutePage(w, sess, f, page)
		return
	}
	s.kroutePage(w, sess, f, map[string]any{"Mode": "done", "Title": title, "Done": sum})
}

func (s *server) kRouteNewPage(w http.ResponseWriter, r *http.Request, sess *session) {
	if !s.kGate(w, sess) {
		return
	}
	s.kroutePage(w, sess, kform(r), map[string]any{"Mode": "new", "Title": "Add route"})
}

func (s *server) kRouteNewPost(w http.ResponseWriter, r *http.Request, sess *session) {
	if !s.kGate(w, sess) {
		return
	}
	s.kSend(w, r, sess, kform(r), "add", "Add route", map[string]any{"Mode": "new", "Title": "Add route"})
}

func (s *server) kRouteCommentPage(w http.ResponseWriter, r *http.Request, sess *session) {
	if !s.kGate(w, sess) {
		return
	}
	s.kroutePage(w, sess, kform(r), map[string]any{"Mode": "comment", "Title": "Comment route"})
}

func (s *server) kRouteCommentPost(w http.ResponseWriter, r *http.Request, sess *session) {
	if !s.kGate(w, sess) {
		return
	}
	s.kSend(w, r, sess, kform(r), "comment", "Comment route", map[string]any{"Mode": "comment", "Title": "Comment route"})
}
