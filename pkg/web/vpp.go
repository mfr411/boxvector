package web

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// VPP status page: read-only. Every command below is a fixed string (no user input
// reaches vppctl), talking to the local CLI socket as a member of the vpp group.

const vppSock = "/run/vpp/cli.sock"

type vppThread struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	CPU  int    `json:"cpu"`
}

type vppIface struct {
	Name   string `json:"n"`
	Idx    int    `json:"idx"`
	Up     bool   `json:"up"`
	MTU    string `json:"mtu"`
	RxP    uint64 `json:"rxk"`
	TxP    uint64 `json:"txk"`
	RxB    uint64 `json:"rx"`
	TxB    uint64 `json:"tx"`
	Drops  uint64 `json:"drops"`
	Punt   uint64 `json:"punt"`
	RxErr  uint64 `json:"rxe"`
	TxErr  uint64 `json:"txe"`
	RxMiss uint64 `json:"miss"`
}

type vppLCP struct {
	VPP  string `json:"vpp"`
	Tap  string `json:"tap"`
	Host string `json:"host"`
	Type string `json:"type"`
}

type vppBuf struct {
	Name  string `json:"name"`
	Numa  int    `json:"numa"`
	Size  int    `json:"size"`
	Total int    `json:"total"`
	Avail int    `json:"avail"`
	Used  int    `json:"used"`
}

type vppStatus struct {
	Available bool        `json:"available"`
	Running   bool        `json:"running"`
	Version   string      `json:"version"`
	Threads   []vppThread `json:"threads"`
	Ifaces    []vppIface  `json:"ifaces"`
	LCP       []vppLCP    `json:"lcp"`
	Buffers   []vppBuf    `json:"buffers"`
	Error     string      `json:"error,omitempty"`
}

func vppInstalled() bool {
	_, err := os.Stat(vppSock)
	if err == nil {
		return true
	}
	_, err = os.Stat("/usr/bin/vpp")
	return err == nil
}

var ansiRe = regexp.MustCompile("\x1b\\[[0-9;]*[A-Za-z]")

var vppExec = func(cmd string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "vppctl", "-s", vppSock, cmd).CombinedOutput()
	return ansiRe.ReplaceAllString(strings.ReplaceAll(string(out), "\r", ""), ""), err
}

func parseVPPThreads(s string) []vppThread {
	out := []vppThread{}
	for _, l := range strings.Split(s, "\n") {
		f := strings.Fields(l)
		if len(f) < 4 {
			continue
		}
		id, err := strconv.Atoi(f[0])
		if err != nil {
			continue
		}
		cpu, err := strconv.Atoi(f[3])
		if err != nil {
			continue
		}
		out = append(out, vppThread{ID: id, Name: f[1], CPU: cpu})
	}
	return out
}

func parseVPPIfaces(s string) []vppIface {
	out := []vppIface{}
	var cur *vppIface
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) == "" || strings.Contains(l, "Counter") && strings.Contains(l, "Count") {
			continue
		}
		f := strings.Fields(l)
		if l[0] != ' ' && l[0] != '\t' {
			if len(f) < 4 {
				cur = nil
				continue
			}
			idx, err := strconv.Atoi(f[1])
			if err != nil {
				cur = nil
				continue
			}
			out = append(out, vppIface{Name: f[0], Idx: idx, Up: f[2] == "up", MTU: f[3]})
			cur = &out[len(out)-1]
			if len(f) >= 6 { // first counter shares the line
				cur.add(strings.Join(f[4:len(f)-1], " "), f[len(f)-1])
			}
			continue
		}
		if cur == nil || len(f) < 2 {
			continue
		}
		cur.add(strings.Join(f[:len(f)-1], " "), f[len(f)-1])
	}
	return out
}

func (i *vppIface) add(k, v string) {
	n, err := strconv.ParseUint(v, 10, 64)
	if err != nil {
		return
	}
	switch k {
	case "rx packets":
		i.RxP = n
	case "tx packets":
		i.TxP = n
	case "rx bytes":
		i.RxB = n
	case "tx bytes":
		i.TxB = n
	case "drops":
		i.Drops = n
	case "punt":
		i.Punt = n
	case "rx-error":
		i.RxErr = n
	case "tx-error":
		i.TxErr = n
	case "rx-miss":
		i.RxMiss = n
	}
}

func parseVPPLCP(s string) []vppLCP {
	out := []vppLCP{}
	for _, l := range strings.Split(s, "\n") {
		// itf-pair: [0] loop0 tap4096 bvtest0 4 type tap
		if !strings.HasPrefix(strings.TrimSpace(l), "itf-pair:") {
			continue
		}
		f := strings.Fields(l)
		if len(f) < 5 {
			continue
		}
		t := ""
		if len(f) >= 8 {
			t = f[len(f)-1]
		}
		out = append(out, vppLCP{VPP: f[2], Tap: f[3], Host: f[4], Type: t})
	}
	return out
}

func parseVPPBuffers(s string) []vppBuf {
	out := []vppBuf{}
	for _, l := range strings.Split(s, "\n") {
		f := strings.Fields(l)
		if len(f) < 9 || !strings.Contains(f[0], "numa") {
			continue
		}
		n := make([]int, 8)
		ok := true
		for i := 0; i < 8; i++ {
			v, err := strconv.Atoi(f[1+i])
			if err != nil {
				ok = false
				break
			}
			n[i] = v
		}
		if ok {
			out = append(out, vppBuf{Name: f[0], Numa: n[1], Size: n[2], Total: n[4], Avail: n[5], Used: n[7]})
		}
	}
	return out
}

func readVPP() vppStatus {
	st := vppStatus{Available: vppInstalled(), Threads: []vppThread{}, Ifaces: []vppIface{}, LCP: []vppLCP{}, Buffers: []vppBuf{}}
	if !st.Available {
		return st
	}
	v, err := vppExec("show version")
	if err != nil {
		st.Error = "VPP is installed but not reachable (is the vpp service running? is boxvector-web in the vpp group?)"
		return st
	}
	st.Running = true
	st.Version = strings.TrimSpace(strings.SplitN(v, "\n", 2)[0])
	if o, err := vppExec("show threads"); err == nil {
		st.Threads = parseVPPThreads(o)
	}
	if o, err := vppExec("show interface"); err == nil {
		st.Ifaces = parseVPPIfaces(o)
	}
	if o, err := vppExec("show lcp"); err == nil {
		st.LCP = parseVPPLCP(o)
	}
	if o, err := vppExec("show buffers"); err == nil {
		st.Buffers = parseVPPBuffers(o)
	}
	return st
}

func (s *server) vppAPI(w http.ResponseWriter, r *http.Request, sess *session) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(readVPP())
}

func (s *server) vppPage(w http.ResponseWriter, r *http.Request, sess *session) {
	s.render(w, "vpp", ctxPage{Title: "VPP", Nav: "vpp", User: sess.user, CSRF: sess.csrf})
}
