package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/mfr411/boxvector/pkg/bird"
)

type statusInfo struct {
	Host      string `json:"host"`
	Up        int64  `json:"up"`   // daemon uptime, seconds
	Sess      int    `json:"sess"` // established BGP sessions
	SessTotal int    `json:"tot"`
	Apply     string `json:"apply"` // last reconfiguration, HH:MM:SS
	Now       int64  `json:"now"`
}

// statusAPI feeds the status bar from the running BIRD (nothing simulated).
func (s *server) statusAPI(w http.ResponseWriter, r *http.Request, sess *session) {
	ri := s.router()
	st := statusInfo{Host: ri.Hostname, Now: time.Now().Unix(), Apply: "-"}
	if out, _, err := bird.RunCommand("show status", ri.Socket); err == nil {
		for _, l := range strings.Split(out, "\n") {
			l = strings.TrimSpace(l)
			if v, ok := strings.CutPrefix(l, "Last reboot on "); ok {
				if t, err := time.ParseInLocation("2006-01-02 15:04:05", v, time.Local); err == nil {
					st.Up = int64(time.Since(t).Seconds())
				}
			}
			if v, ok := strings.CutPrefix(l, "Last reconfiguration on "); ok {
				if t, err := time.ParseInLocation("2006-01-02 15:04:05", v, time.Local); err == nil {
					st.Apply = t.Format("15:04:05")
				}
			}
			if v, ok := strings.CutPrefix(l, "Hostname is "); ok && st.Host == "" {
				st.Host = v
			}
		}
	}
	if out, _, err := bird.RunCommand("show protocols all", ri.Socket); err == nil {
		if ps, err := bird.ParseProtocols(out); err == nil {
			for _, p := range ps {
				if p.BGP == nil {
					continue
				}
				st.SessTotal++
				if p.Info == "Established" {
					st.Sess++
				}
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(st)
}
