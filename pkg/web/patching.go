package web

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/mfr411/boxvector/pkg/fleet"
)

type flash struct {
	OK  bool
	Msg string
}

type fleetRow struct {
	H fleet.Host
	S fleet.Status
}

func (s *server) patchingRender(w http.ResponseWriter, sess *session, f *flash) {
	d := map[string]any{"Enabled": s.fleet != nil, "Flash": f}
	if s.fleet != nil {
		hs, _ := s.fleet.Hosts()
		st := s.fleet.Statuses()
		var rows []fleetRow
		for _, h := range hs {
			rows = append(rows, fleetRow{H: h, S: st[h.Name]})
		}
		d["Rows"], d["Jobs"], d["Busy"] = rows, s.fleet.Jobs(15), s.fleet.Busy()
	}
	s.render(w, "patching", ctxPage{Title: "Patching", Nav: "patching", User: sess.user, CSRF: sess.csrf, Data: d})
}

func (s *server) patchingPage(w http.ResponseWriter, r *http.Request, sess *session) {
	s.patchingRender(w, sess, nil)
}

func (s *server) fleetOff(w http.ResponseWriter, sess *session) bool {
	if s.fleet == nil {
		s.patchingRender(w, sess, nil)
		return true
	}
	return false
}

func (s *server) patchingRun(w http.ResponseWriter, r *http.Request, sess *session) {
	if s.fleetOff(w, sess) {
		return
	}
	_ = r.ParseForm()
	hosts, pkgs := r.Form["host"], r.Form["pkg"]
	do := r.PostFormValue("do")
	switch do {
	case "reboot":
		if len(hosts) != 1 {
			s.patchingRender(w, sess, &flash{Msg: "Select exactly one router to reboot."})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		if err := s.fleet.Reboot(ctx, hosts[0]); err != nil {
			s.patchingRender(w, sess, &flash{Msg: "Reboot failed: " + err.Error()})
			return
		}
		s.store.log(Event{Time: time.Now(), User: sess.user, Action: "fleet reboot", OK: true, Summary: hosts[0]})
		s.patchingRender(w, sess, &flash{OK: true, Msg: "Reboot requested for " + hosts[0] + "."})
		return
	case "check", "patch":
	default:
		http.Error(w, "bad action", http.StatusBadRequest)
		return
	}
	mode := fleet.Mode(r.PostFormValue("mode"))
	dry := r.PostFormValue("dry") == "1"
	id, err := s.fleet.Start(sess.user, do, hosts, mode, pkgs, dry, "")
	if err != nil {
		s.patchingRender(w, sess, &flash{Msg: err.Error()})
		return
	}
	s.store.log(Event{Time: time.Now(), User: sess.user, Action: "fleet " + do, OK: true, Summary: strings.Join(hosts, ",") + " job " + id})
	s.patchingRender(w, sess, &flash{OK: true, Msg: "Job " + id + " started. Reload to follow progress."})
}

func (s *server) patchingHosts(w http.ResponseWriter, r *http.Request, sess *session) {
	if s.fleetOff(w, sess) {
		return
	}
	var err error
	switch r.PostFormValue("do") {
	case "add":
		port := 0
		if v := strings.TrimSpace(r.PostFormValue("port")); v != "" {
			for _, c := range v {
				if c < '0' || c > '9' || port > 65535 {
					port = -1
					break
				}
				port = port*10 + int(c-'0')
			}
		}
		err = s.fleet.AddHost(fleet.Host{Name: strings.TrimSpace(r.PostFormValue("name")), Addr: strings.TrimSpace(r.PostFormValue("addr")), Port: port, User: strings.TrimSpace(r.PostFormValue("user"))})
	case "remove":
		err = s.fleet.RemoveHost(r.PostFormValue("name"))
	default:
		http.Error(w, "bad action", http.StatusBadRequest)
		return
	}
	if err != nil {
		s.patchingRender(w, sess, &flash{Msg: err.Error()})
		return
	}
	s.store.log(Event{Time: time.Now(), User: sess.user, Action: "fleet " + r.PostFormValue("do") + " host", OK: true, Summary: r.PostFormValue("name")})
	s.patchingRender(w, sess, &flash{OK: true, Msg: "Done."})
}

func (s *server) patchingRollback(w http.ResponseWriter, r *http.Request, sess *session) {
	if s.fleetOff(w, sess) {
		return
	}
	_ = r.ParseForm()
	id, err := s.fleet.Start(sess.user, "rollback", r.Form["host"], "", nil, false, r.PostFormValue("job"))
	if err != nil {
		s.patchingRender(w, sess, &flash{Msg: err.Error()})
		return
	}
	s.store.log(Event{Time: time.Now(), User: sess.user, Action: "fleet rollback", OK: true, Summary: strings.Join(r.Form["host"], ",") + " job " + id})
	s.patchingRender(w, sess, &flash{OK: true, Msg: "Rollback job " + id + " started."})
}
