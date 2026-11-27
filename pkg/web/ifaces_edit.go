package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

// Interface editing (legacy ifupdown2 mode). The web service only validates the session and
// forwards structured fields to the root net helper, which re-validates and applies them with
// commit-confirm: the change reverts by itself unless confirmed.

func sysIfaces() []string {
	es, _ := os.ReadDir("/sys/class/net")
	var l []string
	for _, e := range es {
		l = append(l, e.Name())
	}
	sort.Strings(l)
	return l
}

func (s *server) ifPage(w http.ResponseWriter, sess *session, f map[string]string, data map[string]any) {
	data["F"] = f
	data["Devs"] = sysIfaces()
	s.render(w, "ifedit", ctxPage{Title: "Interfaces", Nav: "interfaces", User: sess.user, CSRF: sess.csrf, Data: data})
}

func ifForm(r *http.Request) map[string]string {
	f := map[string]string{}
	for _, k := range []string{"op", "dev", "parent", "vid", "name", "ports", "addr", "mtu"} {
		v := r.PostFormValue(k)
		if v == "" {
			v = r.URL.Query().Get(k)
		}
		f[k] = strings.TrimSpace(v)
	}
	return f
}

var ifActs = map[string][2]string{ // op -> title, help
	"addaddr": {"Add address", "Add an IP address (CIDR) to this interface. It is saved to /etc/network/interfaces.d and survives a reboot."},
	"deladdr": {"Remove address", "Only addresses added through boxvector can be removed here."},
	"mtu":     {"Set MTU", "Set the MTU (68-9216) and keep it across reboots."},
	"del":     {"Delete interface", "Delete this VLAN or bridge, and its saved configuration."},
}

func (s *server) ifGate(w http.ResponseWriter, sess *session) bool {
	if !canTerminal(sess.user) {
		http.Error(w, "editing interfaces is limited to root and members of the sudo group", http.StatusForbidden)
		return false
	}
	return true
}

func (s *server) ifNewPage(w http.ResponseWriter, r *http.Request, sess *session) {
	if s.ifGate(w, sess) {
		s.ifPage(w, sess, ifForm(r), map[string]any{"Mode": "new", "Title": "Add interface"})
	}
}

func (s *server) ifActPage(w http.ResponseWriter, r *http.Request, sess *session) {
	if !s.ifGate(w, sess) {
		return
	}
	f := ifForm(r)
	a, ok := ifActs[f["op"]]
	if !ok {
		http.Error(w, "unknown action", http.StatusBadRequest)
		return
	}
	s.ifPage(w, sess, f, map[string]any{"Mode": "act", "Title": a[0], "Text": a[1], "Danger": f["op"] == "del"})
}

func (s *server) ifPost(w http.ResponseWriter, r *http.Request, sess *session) {
	if !s.ifGate(w, sess) {
		return
	}
	f := ifForm(r)
	mode, title, text := "act", "", ""
	if a, ok := ifActs[f["op"]]; ok {
		title, text = a[0], a[1]
	} else if f["op"] == "vlan" || f["op"] == "bridge" {
		mode, title = "new", "Add interface"
	} else {
		http.Error(w, "unknown action", http.StatusBadRequest)
		return
	}
	v := url.Values{"client": {s.auth.clientIP(r)}}
	for k, x := range f {
		v.Set(k, x)
	}
	if r.PostFormValue("force") == "1" {
		v.Set("force", "1")
	}
	msg, code := s.netCall("/iface", v)
	sum := title + " " + strings.TrimSpace(f["dev"]+" "+f["parent"]+" "+f["vid"]+" "+f["name"]+" "+f["addr"]+" "+f["mtu"])
	s.store.log(Event{Time: time.Now(), User: sess.user, Action: "interface", OK: msg == "", Summary: strings.Join(strings.Fields(sum), " "), Output: msg})
	if msg != "" {
		s.ifPage(w, sess, f, map[string]any{"Mode": mode, "Title": title, "Text": text, "Err": msg, "Force": code == http.StatusConflict, "Danger": f["op"] == "del"})
		return
	}
	s.ifPage(w, sess, f, map[string]any{"Mode": "done", "Title": title, "Done": strings.Join(strings.Fields(sum), " ")})
}

// ifConfirm handles the banner buttons (confirm / revert) as JSON.
func (s *server) ifConfirm(w http.ResponseWriter, r *http.Request, sess *session) {
	if !canTerminal(sess.user) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	op := r.PostFormValue("op")
	if op != "confirm" && op != "revert" {
		http.Error(w, "bad op", http.StatusBadRequest)
		return
	}
	msg, _ := s.netCall("/iface", url.Values{"op": {op}})
	s.store.log(Event{Time: time.Now(), User: sess.user, Action: "interface", OK: msg == "", Summary: strings.ToUpper(op[:1]) + op[1:] + " pending interface change", Output: msg})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// ifStatus exposes the helper's pending change to the page banner.
func (s *server) ifStatus(w http.ResponseWriter, r *http.Request, sess *session) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	resp, err := s.netClient().Get("http://helper/iface")
	if err != nil {
		_, _ = w.Write([]byte(`{"pending":null,"ifupdown2":false,"helper":false}`))
		return
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var m map[string]any
	if json.Unmarshal(b, &m) != nil {
		m = map[string]any{}
	}
	m["helper"] = true
	m["can"] = canTerminal(sess.user)
	delete(m, "managed")
	_ = json.NewEncoder(w).Encode(m)
}
