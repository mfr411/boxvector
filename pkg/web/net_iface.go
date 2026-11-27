//go:build linux

package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Legacy-mode interface editing. State is kept in /var/lib/boxvector-net/ifaces.json (root only) and
// rendered to /etc/network/interfaces.d/boxvector-<name>; ifupdown2's `ifreload -a` applies it without
// bringing everything down. Every change is "commit-confirm": it is rolled back automatically
// unless confirmed within confirmWindow.

const (
	ifaceStateFile = "/var/lib/boxvector-net/ifaces.json"
	ifaceDir       = "/etc/network/interfaces.d"
	confirmWindow  = 60 * time.Second
)

var reBr = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,14}$`)

type ifc struct {
	Kind     string   `json:"kind"` // vlan | bridge | addr (extra settings on an existing interface)
	Name     string   `json:"name"`
	Parent   string   `json:"parent,omitempty"`
	VID      int      `json:"vid,omitempty"`
	Ports    []string `json:"ports,omitempty"`
	MTU      int      `json:"mtu,omitempty"`
	Orig     int      `json:"orig_mtu,omitempty"` // MTU before boxvector changed it (kind addr)
	Addrs    []string `json:"addrs,omitempty"`
	GW       string   `json:"gw,omitempty"`        // optional gateway, must be on-link for one of Addrs
	GWMetric int      `json:"gw_metric,omitempty"` // >0 when another default route already exists
}

type ifPending struct {
	Deadline time.Time `json:"deadline"`
	Summary  string    `json:"summary"`
}

type ifMgr struct {
	h       *netHelper
	mu      sync.Mutex
	state   map[string]*ifc
	pending *ifPending
	timer   *time.Timer
	undo    map[string]*ifc
}

func newIfMgr(h *netHelper) *ifMgr {
	m := &ifMgr{h: h, state: map[string]*ifc{}}
	if b, err := os.ReadFile(ifaceStateFile); err == nil {
		var l []*ifc
		if json.Unmarshal(b, &l) == nil {
			for _, c := range l {
				m.state[c.Name] = c
			}
		}
	}
	go func() {
		time.Sleep(2 * time.Second)
		m.mu.Lock()
		defer m.mu.Unlock()
		_ = applyAddrDiff(map[string]*ifc{}, m.state)
	}()
	_ = installHook()
	return m
}

func devExists(n string) bool { _, err := os.Stat("/sys/class/net/" + n); return err == nil }

func cloneState(s map[string]*ifc) map[string]*ifc {
	o := make(map[string]*ifc, len(s))
	for k, v := range s {
		c := *v
		c.Ports = append([]string(nil), v.Ports...)
		c.Addrs = append([]string(nil), v.Addrs...)
		o[k] = &c
	}
	return o
}

func (c *ifc) render() string {
	var v4, v6 []string
	for _, a := range c.Addrs {
		if strings.Contains(a, ":") {
			v6 = append(v6, a)
		} else {
			v4 = append(v4, a)
		}
	}
	head := func(fam string, addrs []string, first bool) string {
		var b strings.Builder
		fmt.Fprintf(&b, "auto %s\n", c.Name)
		if len(addrs) == 0 {
			fmt.Fprintf(&b, "iface %s %s manual\n", c.Name, fam)
		} else {
			fmt.Fprintf(&b, "iface %s %s static\n", c.Name, fam)
			for _, a := range addrs {
				fmt.Fprintf(&b, "\taddress %s\n", a)
			}
		}
		if first {
			switch c.Kind {
			case "vlan":
				fmt.Fprintf(&b, "\tvlan-raw-device %s\n\tvlan-id %d\n", c.Parent, c.VID)
			case "bridge":
				p := "none"
				if len(c.Ports) > 0 {
					p = strings.Join(c.Ports, " ")
				}
				fmt.Fprintf(&b, "\tbridge-ports %s\n\tbridge-stp off\n", p)
			}
			if c.MTU > 0 {
				fmt.Fprintf(&b, "\tmtu %d\n", c.MTU)
			}
		}
		if g := c.GW; g != "" && len(addrs) > 0 && strings.Contains(g, ":") == (fam == "inet6") {
			fmt.Fprintf(&b, "\tgateway %s\n", g)
			if c.GWMetric > 0 {
				fmt.Fprintf(&b, "\tmetric %d\n", c.GWMetric)
			}
		}
		return b.String()
	}
	out := "# managed by boxvector, do not edit\n"
	if len(v4) > 0 || len(v6) == 0 {
		out += head("inet", v4, true) + "\n"
		if len(v6) > 0 {
			out += head("inet6", v6, false) + "\n"
		}
	} else {
		out += head("inet6", v6, true) + "\n"
	}
	return out
}

func (m *ifMgr) writeAll() error {
	if err := os.MkdirAll("/var/lib/boxvector-net", 0o700); err != nil {
		return err
	}
	keep := map[string]bool{}
	names := make([]string, 0, len(m.state))
	for n := range m.state {
		names = append(names, n)
	}
	sort.Strings(names)
	l := make([]*ifc, 0, len(names))
	for _, n := range names {
		c := m.state[n]
		l = append(l, c)
		if c.Kind == "addr" {
			continue // existing interface: applied with ip and re-applied by the if-up hook, no second iface stanza
		}
		f := filepath.Join(ifaceDir, "boxvector-"+c.Name)
		keep[f] = true
		if err := writeFileAtomic(f, []byte(c.render()), 0o644); err != nil {
			return err
		}
	}
	if err := writeAddrFiles(m.state); err != nil {
		return err
	}
	old, _ := filepath.Glob(filepath.Join(ifaceDir, "boxvector-*"))
	for _, f := range old {
		if !keep[f] && !strings.HasSuffix(f, ".tmp") {
			_ = os.Remove(f)
		}
	}
	b, _ := json.MarshalIndent(l, "", " ")
	return writeFileAtomic(ifaceStateFile, b, 0o600)
}

func writeFileAtomic(path string, b []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (m *ifMgr) reload() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ifreload", "-a").CombinedOutput()
	s := strings.TrimSpace(string(out))
	// ifupdown2 prints a harmless warning when bridge-utils is missing
	var keep []string
	for _, ln := range strings.Split(s, "\n") {
		if !strings.Contains(ln, "brctl: missing") {
			keep = append(keep, ln)
		}
	}
	return strings.TrimSpace(strings.Join(keep, "\n")), err
}

// rollback restores the snapshot and reloads. Caller holds mu.
func (m *ifMgr) rollback() error {
	if m.timer != nil {
		m.timer.Stop()
	}
	cur := m.state
	if m.undo != nil {
		m.state = m.undo
	}
	m.pending, m.undo = nil, nil
	if err := m.writeAll(); err != nil {
		return err
	}
	var err error
	if structChanged(cur, m.state) {
		removeGone(cur, m.state)
		_, err = m.reload()
	}
	_ = applyAddrDiff(cur, m.state)
	return err
}

// change runs mutate on the state, applies it and arms the confirm timer.
func (m *ifMgr) change(summary string, mutate func(s map[string]*ifc) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pending != nil {
		return errors.New("a change is waiting for confirmation: confirm or revert it first")
	}
	if _, err := exec.LookPath("ifreload"); err != nil {
		return errors.New("ifupdown2 (ifreload) is not installed; this host is not in legacy ifupdown2 mode")
	}
	snap := cloneState(m.state)
	if err := mutate(m.state); err != nil {
		m.state = snap
		return err
	}
	m.undo = snap
	if err := m.writeAll(); err != nil {
		_ = m.rollback()
		return err
	}
	// ifreload only when a VLAN/bridge is added/removed: reloading flaps links, so plain address/MTU
	// changes on existing interfaces go through ip directly.
	if structChanged(snap, m.state) {
		removeGone(snap, m.state)
		if out, err := m.reload(); err != nil {
			_ = m.rollback()
			return errors.New("ifreload failed, change reverted: " + out)
		}
	}
	if err := applyAddrDiff(snap, m.state); err != nil {
		_ = m.rollback()
		return errors.New("change reverted: " + err.Error())
	}
	m.pending = &ifPending{Deadline: time.Now().Add(confirmWindow), Summary: summary}
	m.timer = time.AfterFunc(confirmWindow, func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.pending != nil {
			_ = m.rollback()
		}
	})
	return nil
}

func (m *ifMgr) confirm() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pending == nil {
		return false
	}
	m.timer.Stop()
	m.pending, m.undo = nil, nil
	return true
}

func (m *ifMgr) revert() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pending == nil {
		return errors.New("nothing to revert")
	}
	return m.rollback()
}

func parseCIDR(a string) (string, error) {
	p, err := netip.ParsePrefix(strings.TrimSpace(a))
	if err != nil || p.Addr().Is4In6() || p.Addr().IsUnspecified() || p.Addr().IsMulticast() {
		return "", errors.New("invalid address, use CIDR such as 192.0.2.1/24")
	}
	return p.String(), nil // keeps the host bits
}

// carrier returns the device that reaches the client, so a change there can be flagged.
func carrier(client string) string {
	if _, err := netip.ParseAddr(client); err != nil {
		return ""
	}
	out, err := exec.Command("ip", "route", "get", client).Output()
	if err != nil {
		return ""
	}
	f := strings.Fields(string(out))
	for i, w := range f {
		if w == "dev" && i+1 < len(f) {
			return f[i+1]
		}
	}
	return ""
}

func (m *ifMgr) upsertAddr(s map[string]*ifc, dev string) *ifc {
	if c, ok := s[dev]; ok {
		return c
	}
	c := &ifc{Kind: "addr", Name: dev, Orig: sysMTU(dev)}
	s[dev] = c
	return c
}

func (m *ifMgr) handle(w http.ResponseWriter, r *http.Request) {
	reply := func(code int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(v)
	}
	fail := func(err error) {
		code := 400
		if strings.Contains(err.Error(), "current session") {
			code = 409
		}
		reply(code, map[string]string{"error": err.Error()})
	}
	op, dev := r.FormValue("op"), strings.TrimSpace(r.FormValue("dev"))
	force := r.FormValue("force") == "1"
	if op != "vlan" && op != "bridge" && dev != "" {
		if !reDev.MatchString(dev) || !devExists(dev) {
			fail(errors.New("unknown interface"))
			return
		}
	}
	// refuse risky changes to the interface that carries the caller's session
	risky := func() error {
		if !force && dev != "" && (dev == carrier(r.FormValue("client")) || dev == defaultDev()) {
			return errors.New("this interface may carry your current session (" + dev + ")")
		}
		return nil
	}
	var err error
	switch op {
	case "vlan":
		parent := strings.TrimSpace(r.FormValue("parent"))
		vid, _ := strconv.Atoi(r.FormValue("vid"))
		if !reDev.MatchString(parent) || !devExists(parent) || vid < 1 || vid > 4094 {
			fail(errors.New("a valid parent interface and VLAN id 1-4094 are required"))
			return
		}
		name := fmt.Sprintf("%s.%d", parent, vid)
		if len(name) > 15 {
			fail(errors.New("interface name too long"))
			return
		}
		c := &ifc{Kind: "vlan", Name: name, Parent: parent, VID: vid}
		if a := strings.TrimSpace(r.FormValue("addr")); a != "" {
			if a, err = parseCIDR(a); err != nil {
				fail(err)
				return
			}
			c.Addrs = []string{a}
		}
		if err = applyGW(c, r.FormValue("gw")); err != nil {
			fail(err)
			return
		}
		err = m.change("Add VLAN "+name, func(s map[string]*ifc) error {
			if _, ok := s[name]; ok || devExists(name) {
				return errors.New(name + " already exists")
			}
			s[name] = c
			return nil
		})
	case "bridge":
		name := strings.TrimSpace(r.FormValue("name"))
		if !reBr.MatchString(name) {
			fail(errors.New("invalid bridge name"))
			return
		}
		var ports []string
		for _, p := range strings.Fields(strings.ReplaceAll(r.FormValue("ports"), ",", " ")) {
			if !reDev.MatchString(p) || !devExists(p) {
				fail(errors.New("unknown port " + p))
				return
			}
			if !force && (p == carrier(r.FormValue("client")) || p == defaultDev()) {
				fail(errors.New("port " + p + " may carry your current session (" + p + ")"))
				return
			}
			ports = append(ports, p)
		}
		c := &ifc{Kind: "bridge", Name: name, Ports: ports}
		if a := strings.TrimSpace(r.FormValue("addr")); a != "" {
			if a, err = parseCIDR(a); err != nil {
				fail(err)
				return
			}
			c.Addrs = []string{a}
		}
		if err = applyGW(c, r.FormValue("gw")); err != nil {
			fail(err)
			return
		}
		err = m.change("Add bridge "+name, func(s map[string]*ifc) error {
			if _, ok := s[name]; ok || devExists(name) {
				return errors.New(name + " already exists")
			}
			s[name] = c
			return nil
		})
	case "addaddr", "deladdr":
		a, perr := parseCIDR(r.FormValue("addr"))
		if perr != nil {
			fail(perr)
			return
		}
		if err = risky(); err != nil {
			fail(err)
			return
		}
		err = m.change(strings.Replace(op, "addr", " address ", 1)+a+" on "+dev, func(s map[string]*ifc) error {
			c := m.upsertAddr(s, dev)
			has := false
			for _, x := range c.Addrs {
				has = has || x == a
			}
			if op == "addaddr" {
				if has {
					return errors.New("address already managed on " + dev)
				}
				c.Addrs = append(c.Addrs, a)
			} else {
				if !has {
					return errors.New("only addresses added through boxvector can be removed here")
				}
				var n []string
				for _, x := range c.Addrs {
					if x != a {
						n = append(n, x)
					}
				}
				c.Addrs = n
				if c.Kind == "addr" && len(c.Addrs) == 0 && c.MTU == 0 {
					delete(s, dev)
				}
			}
			return nil
		})
	case "mtu":
		n, _ := strconv.Atoi(r.FormValue("mtu"))
		if n < 68 || n > 9216 {
			fail(errors.New("MTU must be 68-9216"))
			return
		}
		if err = risky(); err != nil {
			fail(err)
			return
		}
		err = m.change(fmt.Sprintf("Set MTU %d on %s", n, dev), func(s map[string]*ifc) error {
			m.upsertAddr(s, dev).MTU = n
			return nil
		})
	case "del":
		err = m.change("Delete "+dev, func(s map[string]*ifc) error {
			c, ok := s[dev]
			if !ok || c.Kind == "addr" {
				return errors.New("only VLANs and bridges created by boxvector can be deleted")
			}
			for _, o := range s {
				if o.Parent == dev || containsStr(o.Ports, dev) {
					return errors.New(o.Name + " still depends on " + dev + ": delete it first")
				}
			}
			delete(s, dev)
			return nil
		})
	case "confirm":
		if !m.confirm() {
			fail(errors.New("nothing to confirm (already reverted?)"))
			return
		}
	case "revert":
		err = m.revert()
	default:
		err = errors.New("unknown operation")
	}
	if err != nil {
		fail(err)
		return
	}
	reply(200, map[string]bool{"ok": true})
}

func (m *ifMgr) status(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l := make([]*ifc, 0, len(m.state))
	for _, c := range m.state {
		l = append(l, c)
	}
	sort.Slice(l, func(i, j int) bool { return l[i].Name < l[j].Name })
	_, e := exec.LookPath("ifreload")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ifupdown2": e == nil, "pending": m.pending, "managed": l})
}

func sysMTU(dev string) int {
	b, err := os.ReadFile("/sys/class/net/" + dev + "/mtu")
	n, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return 0
	}
	return n
}

// defaultDev is the device that carries the default route (the management path in most setups).
func defaultDev() string {
	out, err := exec.Command("ip", "-4", "route", "show", "default").Output()
	if err != nil {
		return ""
	}
	f := strings.Fields(string(out))
	for i, w := range f {
		if w == "dev" && i+1 < len(f) {
			return f[i+1]
		}
	}
	return ""
}

const addrDir = "/var/lib/boxvector-net/addr.d"

// writeAddrFiles persists extra addresses / MTU of existing interfaces for the if-up hook.
func writeAddrFiles(s map[string]*ifc) error {
	if err := os.MkdirAll(addrDir, 0o700); err != nil {
		return err
	}
	keep := map[string]bool{}
	for _, c := range s {
		if c.Kind != "addr" || (len(c.Addrs) == 0 && c.MTU == 0) {
			continue
		}
		var b strings.Builder
		for _, a := range c.Addrs {
			b.WriteString("addr " + a + "\n")
		}
		if c.MTU > 0 {
			fmt.Fprintf(&b, "mtu %d\n", c.MTU)
		}
		keep[c.Name] = true
		if err := writeFileAtomic(filepath.Join(addrDir, c.Name), []byte(b.String()), 0o600); err != nil {
			return err
		}
	}
	old, _ := filepath.Glob(addrDir + "/*")
	for _, f := range old {
		if !keep[filepath.Base(f)] {
			_ = os.Remove(f)
		}
	}
	return nil
}

func ipRun(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, findIP(), args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// applyAddrDiff moves existing interfaces from the old managed state to the new one with ip.
func applyAddrDiff(old, cur map[string]*ifc) error {
	names := map[string]bool{}
	for n, c := range old {
		if c.Kind == "addr" {
			names[n] = true
		}
	}
	for n, c := range cur {
		if c.Kind == "addr" {
			names[n] = true
		}
	}
	for dev := range names {
		if !reDev.MatchString(dev) || !devExists(dev) {
			continue
		}
		var o, n *ifc
		if c, ok := old[dev]; ok && c.Kind == "addr" {
			o = c
		}
		if c, ok := cur[dev]; ok && c.Kind == "addr" {
			n = c
		}
		have := func(c *ifc) map[string]bool {
			m := map[string]bool{}
			if c != nil {
				for _, a := range c.Addrs {
					m[a] = true
				}
			}
			return m
		}
		ho, hn := have(o), have(n)
		for a := range ho {
			if !hn[a] {
				_, _ = ipRun("addr", "del", a, "dev", dev)
			}
		}
		for a := range hn {
			if out, err := ipRun("addr", "replace", a, "dev", dev); err != nil {
				return errors.New("ip addr: " + out)
			}
		}
		switch {
		case n != nil && n.MTU > 0:
			if out, err := ipRun("link", "set", "dev", dev, "mtu", strconv.Itoa(n.MTU)); err != nil {
				return errors.New("ip link mtu: " + out)
			}
		case o != nil && o.MTU > 0 && o.Orig > 0:
			_, _ = ipRun("link", "set", "dev", dev, "mtu", strconv.Itoa(o.Orig))
		}
	}
	return nil
}

const hookScript = `#!/bin/sh
# managed by boxvector: re-apply extra addresses / MTU saved through the web UI
f="/var/lib/boxvector-net/addr.d/$IFACE"
case "$IFACE" in */*|"") exit 0;; esac
[ -r "$f" ] || exit 0
while read -r k v; do
  case "$k" in
    addr) ip addr replace "$v" dev "$IFACE" ;;
    mtu) ip link set dev "$IFACE" mtu "$v" ;;
  esac
done < "$f"
exit 0
`

func installHook() error {
	const p = "/etc/network/if-up.d/boxvector-addr"
	if b, err := os.ReadFile(p); err == nil && string(b) == hookScript {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return writeFileAtomic(p, []byte(hookScript), 0o755)
}

// structChanged reports whether managed VLANs/bridges (anything but plain address/MTU entries) differ.
func structChanged(a, b map[string]*ifc) bool {
	pick := func(s map[string]*ifc) map[string]string {
		m := map[string]string{}
		for n, c := range s {
			if c.Kind != "addr" {
				m[n] = c.render()
			}
		}
		return m
	}
	x, y := pick(a), pick(b)
	if len(x) != len(y) {
		return true
	}
	for n, v := range x {
		if y[n] != v {
			return true
		}
	}
	return false
}

// removeGone deletes from the kernel every VLAN/bridge that left the managed state. ifreload does not
// remove them once their stanza is gone, and children must go before their parents.
func removeGone(old, cur map[string]*ifc) {
	var gone []*ifc
	for n, c := range old {
		if c.Kind == "addr" {
			continue
		}
		if _, ok := cur[n]; !ok && devExists(n) {
			gone = append(gone, c)
		}
	}
	depth := func(c *ifc) int {
		d := 0
		for p := c.Parent; p != ""; d++ {
			pc, ok := old[p]
			if !ok || d > 8 {
				break
			}
			p = pc.Parent
		}
		return d
	}
	sort.Slice(gone, func(i, j int) bool { return depth(gone[i]) > depth(gone[j]) })
	for _, c := range gone {
		_, _ = ipRun("link", "del", c.Name)
	}
}

func containsStr(l []string, v string) bool {
	for _, x := range l {
		if x == v {
			return true
		}
	}
	return false
}

// applyGW validates an optional gateway: same family as one of the interface addresses and inside its
// subnet. When the host already has a default route the new one gets a higher metric, so it can never
// replace the route that carries management traffic.
func applyGW(c *ifc, gw string) error {
	gw = strings.TrimSpace(gw)
	if gw == "" {
		return nil
	}
	g, err := netip.ParseAddr(gw)
	if err != nil || g.IsUnspecified() || g.IsMulticast() || g.Is4In6() {
		return errors.New("invalid gateway address")
	}
	ok := false
	for _, a := range c.Addrs {
		p, perr := netip.ParsePrefix(a)
		if perr == nil && p.Masked().Contains(g) && p.Addr() != g {
			ok = true
		}
	}
	if !ok {
		return errors.New("gateway must be inside the subnet of the interface address (an address is required)")
	}
	c.GW = g.String()
	if defaultDev() != "" {
		c.GWMetric = 200
	}
	return nil
}
