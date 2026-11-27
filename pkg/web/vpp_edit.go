package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// VPP interface editing UI. Same flow as the legacy Interfaces page; the root helper owns vppcfg.

var vppActs = map[string][2]string{
	"addaddr": {"Add address", "Add an IP address (CIDR) to this VPP interface. It is saved to /etc/vpp/boxvector-vppcfg.yaml and survives a reboot."},
	"deladdr": {"Remove address", "Remove an IP address from this VPP interface."},
	"mtu":     {"Set MTU", "Set the MTU (68-9216). The physical interface MTU must be equal or larger."},
	"state":   {"Set state", "Bring this VPP interface up or down."},
	"del":     {"Delete sub-interface", "Delete this sub-interface and its saved configuration."},
}

func vppDevs() []string {
	var l []string
	for _, i := range readVPP().Ifaces {
		if !strings.Contains(i.Name, ".") && i.Name != "local0" {
			l = append(l, i.Name)
		}
	}
	return l
}

func (s *server) vppFormPage(w http.ResponseWriter, sess *session, f map[string]string, d map[string]any) {
	d["VPP"] = true
	d["Devs"] = vppDevs()
	s.ifPage(w, sess, f, d)
}

func (s *server) vppNewPage(w http.ResponseWriter, r *http.Request, sess *session) {
	if s.ifGate(w, sess) {
		f := ifForm(r)
		f["op"] = "sub"
		s.vppFormPage(w, sess, f, map[string]any{"Mode": "new", "Title": "Add sub-interface"})
	}
}

func (s *server) vppActPage(w http.ResponseWriter, r *http.Request, sess *session) {
	if !s.ifGate(w, sess) {
		return
	}
	f := ifForm(r)
	a, ok := vppActs[f["op"]]
	if !ok {
		http.Error(w, "unknown action", http.StatusBadRequest)
		return
	}
	s.vppFormPage(w, sess, f, map[string]any{"Mode": "act", "Title": a[0], "Text": a[1], "Danger": f["op"] == "del"})
}

func (s *server) vppPost(w http.ResponseWriter, r *http.Request, sess *session) {
	if !s.ifGate(w, sess) {
		return
	}
	f := ifForm(r)
	mode, title, text := "act", "", ""
	if a, ok := vppActs[f["op"]]; ok {
		title, text = a[0], a[1]
	} else if f["op"] == "sub" {
		mode, title = "new", "Add sub-interface"
	} else {
		http.Error(w, "unknown action", http.StatusBadRequest)
		return
	}
	v := url.Values{}
	for k, x := range f {
		v.Set(k, x)
	}
	msg, _ := s.netCall("/vppif", v)
	sum := strings.Join(strings.Fields(title+" "+f["dev"]+" "+f["parent"]+" "+f["vid"]+" "+f["inner"]+" "+f["addr"]+" "+f["mtu"]+" "+f["state"]), " ")
	s.store.log(Event{Time: time.Now(), User: sess.user, Action: "vpp-interface", OK: msg == "", Summary: sum, Output: msg})
	if msg != "" {
		s.vppFormPage(w, sess, f, map[string]any{"Mode": mode, "Title": title, "Text": text, "Err": msg, "Danger": f["op"] == "del"})
		return
	}
	s.vppFormPage(w, sess, f, map[string]any{"Mode": "done", "Title": title, "Done": sum})
}

func (s *server) vppConfirm(w http.ResponseWriter, r *http.Request, sess *session) {
	if !canTerminal(sess.user) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	op := r.PostFormValue("op")
	if op != "confirm" && op != "revert" {
		http.Error(w, "bad op", http.StatusBadRequest)
		return
	}
	msg, _ := s.netCall("/vppif", url.Values{"op": {op}})
	s.store.log(Event{Time: time.Now(), User: sess.user, Action: "vpp-interface", OK: msg == "", Summary: strings.ToUpper(op[:1]) + op[1:] + " pending VPP change", Output: msg})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func (s *server) vppStatus(w http.ResponseWriter, r *http.Request, sess *session) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	m := map[string]any{"pending": nil, "helper": false}
	if resp, err := s.netClient().Get("http://helper/vppif"); err == nil {
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		_ = json.Unmarshal(b, &m)
		m["helper"] = true
	}
	m["can"] = canTerminal(sess.user)
	_ = json.NewEncoder(w).Encode(m)
}
