//go:build linux

package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"os/user"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// The net helper is the root side of the network editing features (kernel routes, later
// interfaces). It only accepts structured, validated operations over a unix socket that root and
// the boxvector group can open; no shell and no free-form arguments ever reach a command.
// The managed state lives here (root-only), never in a file the web service can write.

const netStateFile = "/var/lib/boxvector-net/routes.json"

var reDev = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.:@-]{0,14}$`)

type kroute struct {
	Dst     string `json:"dst"`
	Via     string `json:"via"`
	Dev     string `json:"dev"`
	Metric  uint32 `json:"metric"`
	Comment string `json:"comment"`
	Off     bool   `json:"off"`
}

func (k kroute) key() string { return fmt.Sprintf("%s|%s|%s|%d", k.Dst, k.Via, k.Dev, k.Metric) }

// validateRoute normalises and checks every field before it can reach `ip`.
func validateRoute(k kroute) (kroute, error) {
	p, err := netip.ParsePrefix(strings.TrimSpace(k.Dst))
	if err != nil || p.Addr().Is4In6() {
		return k, errors.New("invalid destination prefix")
	}
	k.Dst = p.Masked().String()
	k.Via = strings.TrimSpace(k.Via)
	if k.Via != "" {
		a, err := netip.ParseAddr(k.Via)
		if err != nil || a.Is4In6() || a.Zone() != "" || a.Is4() != p.Addr().Is4() {
			return k, errors.New("invalid gateway (must match the address family of the destination)")
		}
		k.Via = a.String()
	}
	k.Dev = strings.TrimSpace(k.Dev)
	if k.Dev != "" {
		if !reDev.MatchString(k.Dev) {
			return k, errors.New("invalid interface name")
		}
		if _, err := os.Stat("/sys/class/net/" + k.Dev); err != nil {
			return k, errors.New("interface does not exist: " + k.Dev)
		}
	}
	if k.Via == "" && k.Dev == "" {
		return k, errors.New("a gateway or an interface is required (blackhole and similar types are not editable here)")
	}
	k.Comment = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, strings.TrimSpace(k.Comment))
	if len(k.Comment) > 80 {
		k.Comment = k.Comment[:80]
	}
	return k, nil
}

// guardRoute refuses to remove/disable a route that probably carries the caller's own session.
func guardRoute(k kroute, client string, force bool) error {
	if force {
		return nil
	}
	a, err := netip.ParseAddr(client)
	if err != nil {
		return nil
	}
	p, _ := netip.ParsePrefix(k.Dst)
	if p.Contains(a.Unmap()) {
		return errors.New("this route may carry your current session (" + client + ")")
	}
	return nil
}

type netHelper struct {
	mu    sync.Mutex
	ip    string
	state map[string]kroute
	file  string
}

func findIP() string {
	if p, err := exec.LookPath("ip"); err == nil {
		return p
	}
	for _, p := range []string{"/usr/sbin/ip", "/sbin/ip", "/usr/bin/ip", "/bin/ip"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "ip"
}

func (h *netHelper) load() {
	h.state = map[string]kroute{}
	b, err := os.ReadFile(h.file)
	if err != nil {
		return
	}
	var l []kroute
	if json.Unmarshal(b, &l) != nil {
		return
	}
	for _, k := range l {
		if v, err := validateRoute(k); err == nil {
			v.Off = k.Off
			h.state[v.key()] = v
		}
	}
}

func (h *netHelper) list() []kroute {
	l := make([]kroute, 0, len(h.state))
	for _, k := range h.state {
		l = append(l, k)
	}
	sort.Slice(l, func(i, j int) bool { return l[i].key() < l[j].key() })
	return l
}

func (h *netHelper) save() error {
	if err := os.MkdirAll("/var/lib/boxvector-net", 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(h.list(), "", " ")
	tmp := h.file + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, h.file)
}

func (h *netHelper) ipRoute(verb string, k kroute) (string, error) {
	fam := "-4"
	if strings.Contains(k.Dst, ":") {
		fam = "-6"
	}
	args := []string{fam, "route", verb, k.Dst}
	if k.Via != "" {
		args = append(args, "via", k.Via)
	}
	if k.Dev != "" {
		args = append(args, "dev", k.Dev)
	}
	if k.Metric > 0 {
		args = append(args, "metric", strconv.FormatUint(uint64(k.Metric), 10))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, h.ip, args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// apply runs one operation. Caller holds mu.
func (h *netHelper) apply(op string, k kroute, client string, force bool) error {
	switch op {
	case "add":
		if out, err := h.ipRoute("add", k); err != nil {
			return errors.New("ip route add: " + out)
		}
		k.Off = false
		h.state[k.key()] = k
	case "del", "disable":
		if err := guardRoute(k, client, force); err != nil {
			return err
		}
		if out, err := h.ipRoute("del", k); err != nil && !strings.Contains(out, "No such process") {
			return errors.New("ip route del: " + out)
		}
		if op == "del" {
			delete(h.state, k.key())
		} else {
			old := h.state[k.key()]
			if k.Comment == "" {
				k.Comment = old.Comment
			}
			k.Off = true
			h.state[k.key()] = k
		}
	case "enable":
		old, ok := h.state[k.key()]
		if !ok || !old.Off {
			return errors.New("route is not disabled")
		}
		if out, err := h.ipRoute("replace", old); err != nil {
			return errors.New("ip route replace: " + out)
		}
		old.Off = false
		h.state[k.key()] = old
	case "comment":
		old := h.state[k.key()]
		old.Dst, old.Via, old.Dev, old.Metric = k.Dst, k.Via, k.Dev, k.Metric
		old.Comment = k.Comment
		if old.Comment == "" && !old.Off {
			delete(h.state, k.key())
		} else {
			h.state[k.key()] = old
		}
	default:
		return errors.New("unknown operation")
	}
	return h.save()
}

// restore re-applies enabled managed routes at start (interfaces may come up late).
func (h *netHelper) restore() {
	for try := 0; try < 12; try++ {
		h.mu.Lock()
		pending := 0
		for _, k := range h.state {
			if k.Off {
				continue
			}
			if _, err := h.ipRoute("replace", k); err != nil {
				pending++
			}
		}
		h.mu.Unlock()
		if pending == 0 {
			return
		}
		time.Sleep(5 * time.Second)
	}
}

// RunNetHelper serves the root network helper on a unix socket until it fails.
func RunNetHelper(sock string) error {
	if os.Geteuid() != 0 {
		return errors.New("the net helper must run as root")
	}
	_ = os.Remove(sock)
	old := syscall.Umask(0o117)
	ln, err := net.Listen("unix", sock)
	syscall.Umask(old)
	if err != nil {
		return err
	}
	if g, err := user.LookupGroup("boxvector"); err == nil {
		gid, _ := strconv.Atoi(g.Gid)
		_ = os.Chown(sock, 0, gid)
	}
	_ = os.Chmod(sock, 0o660)
	h := &netHelper{ip: findIP(), file: netStateFile}
	h.load()
	go h.restore()
	mux := http.NewServeMux()
	reply := func(w http.ResponseWriter, code int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("GET /routes", func(w http.ResponseWriter, r *http.Request) {
		h.mu.Lock()
		l := h.list()
		h.mu.Unlock()
		reply(w, 200, l)
	})
	mux.HandleFunc("POST /route", func(w http.ResponseWriter, r *http.Request) {
		m, _ := strconv.ParseUint(r.FormValue("metric"), 10, 32)
		k, err := validateRoute(kroute{Dst: r.FormValue("dst"), Via: r.FormValue("via"), Dev: r.FormValue("dev"), Metric: uint32(m), Comment: r.FormValue("comment")})
		if err != nil {
			reply(w, 400, map[string]string{"error": err.Error()})
			return
		}
		h.mu.Lock()
		err = h.apply(r.FormValue("op"), k, r.FormValue("client"), r.FormValue("force") == "1")
		h.mu.Unlock()
		if err != nil {
			code := 400
			if strings.Contains(err.Error(), "current session") {
				code = 409
			}
			reply(w, code, map[string]string{"error": err.Error()})
			return
		}
		reply(w, 200, map[string]bool{"ok": true})
	})
	fmt.Fprintf(os.Stderr, "boxvector net helper on %s\n", sock)
	return (&http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}).Serve(ln)
}
