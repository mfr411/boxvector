package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
)

// Traffic page: per-host live rates (iftop style). Sampling happens in the root helper; the web
// service only checks the session (root/sudo) and relays.

func (s *server) trafficPage(w http.ResponseWriter, r *http.Request, sess *session) {
	if !s.isAdmin(sess) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	s.render(w, "traffic", ctxPage{Title: "Traffic", Nav: "traffic", User: sess.user, CSRF: sess.csrf, Data: map[string]any{"Devs": sysIfaces()}})
}

func (s *server) trafficAPI(w http.ResponseWriter, r *http.Request, sess *session) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if !s.isAdmin(sess) {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "forbidden"})
		return
	}
	resp, err := s.netClient().PostForm("http://helper/traffic", url.Values{"dev": {r.URL.Query().Get("dev")}})
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "The network helper is not running. Start it with: systemctl enable --now boxvector-net"})
		return
	}
	defer resp.Body.Close()
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, io.LimitReader(resp.Body, 1<<20))
}
