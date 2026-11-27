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
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// VPP interface mode. The desired state is a vppcfg YAML file (/etc/vpp/boxvector-vppcfg.yaml);
// vppcfg check validates it, vppcfg apply computes and runs the minimal diff against the live VPP,
// and boxvector-vppcfg.service re-applies it after a reboot. Changes use the same commit-confirm
// window as the legacy mode: the previous YAML is applied again unless the change is confirmed.

var (
	vppcfgFile = "/etc/vpp/boxvector-vppcfg.yaml"
	reVPPName  = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9/_-]{0,62}(\.[0-9]{1,10})?$`)
	// sections of a vppcfg file that hold interfaces we may edit
	vppSections = []string{"interfaces", "bondethernets", "loopbacks", "taps"}
	vppSubSecs  = []string{"interfaces", "bondethernets"} // sections vppcfg allows sub-interfaces in
)

func vppcfgBin() string {
	if _, err := os.Stat("/opt/vppcfg-venv/bin/vppcfg"); err == nil {
		return "/opt/vppcfg-venv/bin/vppcfg"
	}
	p, _ := exec.LookPath("vppcfg")
	return p
}

var vppcfgRun = func(args ...string) (string, error) {
	bin := vppcfgBin()
	if bin == "" {
		return "", errors.New("vppcfg is not installed (run the installer with VPP, or: python3 -m venv /opt/vppcfg-venv && /opt/vppcfg-venv/bin/pip install vppcfg)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, args...).CombinedOutput()
	s := string(out)
	if i := strings.Index(s, "Traceback"); i >= 0 {
		s = s[i:]
	}
	return strings.TrimSpace(s), err
}

type vppMgr struct {
	mu      sync.Mutex
	pending *ifPending
	timer   *time.Timer
	undo    []byte // previous YAML ("" = there was no file)
}

func newVppMgr() *vppMgr { return &vppMgr{} }

// ---- yaml.Node helpers (keep sub-interface ids as integers, keep unknown keys) ----

func yGet(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

func ySet(n *yaml.Node, key string, v *yaml.Node, intKey bool) {
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			n.Content[i+1] = v
			return
		}
	}
	k := &yaml.Node{Kind: yaml.ScalarNode, Value: key}
	if intKey {
		k.Tag = "!!int"
	}
	n.Content = append(n.Content, k, v)
}

func yDel(n *yaml.Node, key string) {
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			n.Content = append(n.Content[:i], n.Content[i+2:]...)
			return
		}
	}
}

func yEnsure(n *yaml.Node, key string, intKey bool) *yaml.Node {
	if v := yGet(n, key); v != nil && v.Kind == yaml.MappingNode {
		return v
	}
	v := &yaml.Node{Kind: yaml.MappingNode}
	ySet(n, key, v, intKey)
	return v
}

func yScalar(v string, tag string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Value: v, Tag: tag}
}

func ySeq(n *yaml.Node) []string {
	var l []string
	if n != nil && n.Kind == yaml.SequenceNode {
		for _, c := range n.Content {
			l = append(l, c.Value)
		}
	}
	return l
}

func ySetSeq(n *yaml.Node, key string, l []string) {
	if len(l) == 0 {
		yDel(n, key)
		return
	}
	s := &yaml.Node{Kind: yaml.SequenceNode, Style: yaml.FlowStyle}
	for _, a := range l {
		s.Content = append(s.Content, yScalar(a, "!!str"))
	}
	ySet(n, key, s, false)
}

// root returns the document's top-level mapping.
func vppRoot(doc *yaml.Node) (*yaml.Node, error) {
	if doc.Kind == yaml.DocumentNode && len(doc.Content) == 1 {
		doc = doc.Content[0]
	}
	if doc.Kind != yaml.MappingNode {
		return nil, errors.New("vppcfg file has no top-level mapping")
	}
	return doc, nil
}

// vppFind locates an interface ("Name" or "Name.subid") and returns its node and section.
func vppFind(root *yaml.Node, dev string, sections []string) (*yaml.Node, error) {
	base, sub, hasSub := strings.Cut(dev, ".")
	for _, sec := range sections {
		if n := yGet(yGet(root, sec), base); n != nil {
			if !hasSub {
				return n, nil
			}
			if s := yGet(yGet(n, "sub-interfaces"), sub); s != nil {
				return s, nil
			}
			return nil, errors.New("unknown sub-interface " + dev)
		}
	}
	return nil, errors.New("unknown VPP interface " + dev)
}

func (m *vppMgr) load() (*yaml.Node, []byte, error) {
	b, err := os.ReadFile(vppcfgFile)
	if err != nil {
		tmp, terr := os.CreateTemp("", "vppdump")
		if terr != nil {
			return nil, nil, terr
		}
		tmp.Close()
		defer os.Remove(tmp.Name())
		if out, derr := vppcfgRun("dump", "-o", tmp.Name()); derr != nil {
			return nil, nil, errors.New("vppcfg dump failed: " + out)
		}
		if b, err = os.ReadFile(tmp.Name()); err != nil {
			return nil, nil, err
		}
		var doc yaml.Node
		if yaml.Unmarshal(b, &doc) != nil || len(b) == 0 {
			return nil, nil, errors.New("vppcfg dump returned no usable config")
		}
		return &doc, nil, nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, nil, err
	}
	return &doc, b, nil
}

func checkAddr(a string) (string, error) {
	p, err := netip.ParsePrefix(strings.TrimSpace(a))
	if err != nil || p.Addr().IsUnspecified() || p.Addr().IsMulticast() || p.Addr().Is4In6() {
		return "", errors.New("address must be CIDR, e.g. 192.0.2.1/24")
	}
	return p.String(), nil
}

// vppMutate applies one operation to the YAML tree.
func vppMutate(root *yaml.Node, f func(string) string) (string, error) {
	op, dev := f("op"), strings.TrimSpace(f("dev"))
	if dev != "" && !reVPPName.MatchString(dev) {
		return "", errors.New("invalid interface name")
	}
	switch op {
	case "sub":
		parent := strings.TrimSpace(f("parent"))
		vid, e1 := strconv.Atoi(f("vid"))
		inner := 0
		if f("inner") != "" {
			inner, _ = strconv.Atoi(f("inner"))
		}
		proto := f("proto")
		if proto == "" {
			proto = "dot1q"
		}
		if !reVPPName.MatchString(parent) || strings.Contains(parent, ".") {
			return "", errors.New("parent must be a VPP interface (not a sub-interface)")
		}
		if e1 != nil || vid < 1 || vid > 4094 || inner < 0 || inner > 4094 {
			return "", errors.New("VLAN ID must be 1-4094")
		}
		if proto != "dot1q" && proto != "dot1ad" {
			return "", errors.New("protocol must be dot1q or dot1ad")
		}
		pn, err := vppFind(root, parent, vppSubSecs)
		if err != nil {
			return "", errors.New("parent " + parent + " not found (or cannot carry sub-interfaces)")
		}
		if st := yGet(pn, "state"); st == nil || st.Value != "up" {
			ySet(pn, "state", yScalar("up", "!!str"), false) // vppcfg refuses an up sub-interface on a down parent
		}
		id := vid
		if inner > 0 {
			id = vid*4096 + inner // QinQ: one sub-interface id per outer/inner pair
		}
		sid := strconv.Itoa(id)
		subs := yEnsure(pn, "sub-interfaces", false)
		if yGet(subs, sid) != nil {
			return "", errors.New(parent + "." + sid + " already exists")
		}
		enc := &yaml.Node{Kind: yaml.MappingNode}
		ySet(enc, proto, yScalar(strconv.Itoa(vid), "!!int"), false)
		if inner > 0 {
			ySet(enc, "inner-dot1q", yScalar(strconv.Itoa(inner), "!!int"), false)
		}
		ySet(enc, "exact-match", yScalar("true", "!!bool"), false)
		sn := &yaml.Node{Kind: yaml.MappingNode}
		ySet(sn, "encapsulation", enc, false)
		if a := strings.TrimSpace(f("addr")); a != "" {
			ca, err := checkAddr(a)
			if err != nil {
				return "", err
			}
			ySetSeq(sn, "addresses", []string{ca})
		}
		ySet(subs, sid, sn, true)
		return "Add sub-interface " + parent + "." + sid, nil
	case "addaddr", "deladdr":
		n, err := vppFind(root, dev, vppSections)
		if err != nil {
			return "", err
		}
		ca, err := checkAddr(f("addr"))
		if err != nil {
			return "", err
		}
		cur := ySeq(yGet(n, "addresses"))
		var out []string
		found := false
		for _, a := range cur {
			if a == ca {
				found = true
				if op == "deladdr" {
					continue
				}
			}
			out = append(out, a)
		}
		if op == "addaddr" {
			if found {
				return "", errors.New("address already present")
			}
			out = append(out, ca)
		} else if !found {
			return "", errors.New("address not found on " + dev)
		}
		ySetSeq(n, "addresses", out)
		return strings.Replace(op, "addr", " address ", 1) + ca + " on " + dev, nil
	case "mtu":
		n, err := vppFind(root, dev, vppSections)
		if err != nil {
			return "", err
		}
		v, e := strconv.Atoi(f("mtu"))
		if e != nil || v < 68 || v > 9216 {
			return "", errors.New("MTU must be 68-9216")
		}
		ySet(n, "mtu", yScalar(strconv.Itoa(v), "!!int"), false)
		return "Set MTU " + strconv.Itoa(v) + " on " + dev, nil
	case "state":
		n, err := vppFind(root, dev, vppSections)
		if err != nil {
			return "", err
		}
		st := f("state")
		if st != "up" && st != "down" {
			return "", errors.New("state must be up or down")
		}
		ySet(n, "state", yScalar(st, "!!str"), false)
		return "Set " + dev + " " + st, nil
	case "del":
		base, sub, ok := strings.Cut(dev, ".")
		if !ok {
			return "", errors.New("only sub-interfaces can be deleted here; physical interfaces belong to the VPP startup config")
		}
		for _, sec := range vppSubSecs {
			if n := yGet(yGet(root, sec), base); n != nil {
				subs := yGet(n, "sub-interfaces")
				if yGet(subs, sub) == nil {
					break
				}
				yDel(subs, sub)
				if len(subs.Content) == 0 {
					yDel(n, "sub-interfaces")
				}
				return "Delete " + dev, nil
			}
		}
		return "", errors.New("unknown sub-interface " + dev)
	}
	return "", errors.New("unknown operation")
}

func (m *vppMgr) apply(b []byte) error {
	tmp, err := os.CreateTemp("", "vppcfg*.yaml")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, _ = tmp.Write(b)
	tmp.Close()
	if out, err := vppcfgRun("check", "-c", tmp.Name()); err != nil {
		return errors.New("vppcfg check: " + lastLines(out, 4))
	}
	if out, err := vppcfgRun("apply", "-c", tmp.Name()); err != nil {
		return errors.New("vppcfg apply: " + lastLines(out, 4))
	}
	return nil
}

func lastLines(s string, n int) string {
	l := strings.Split(strings.TrimSpace(s), "\n")
	for i, x := range l {
		x = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(x), "[ERROR ]"))
		if j := strings.Index(x, ": "); j > 0 && strings.Contains(x[:j], ".") {
			x = x[j+2:]
		}
		l[i] = x
	}
	if len(l) > n {
		l = l[len(l)-n:]
	}
	return strings.Join(l, " | ")
}

func (m *vppMgr) rollbackLocked() error {
	if m.timer != nil {
		m.timer.Stop()
	}
	prev := m.undo
	m.pending, m.undo = nil, nil
	if prev == nil {
		_ = os.Remove(vppcfgFile)
		return nil
	}
	if err := writeFileAtomic(vppcfgFile, prev, 0o640); err != nil {
		return err
	}
	return m.apply(prev)
}

func (m *vppMgr) change(f func(string) string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pending != nil {
		return "", errors.New("a change is waiting for confirmation: confirm or revert it first")
	}
	doc, old, err := m.load()
	if err != nil {
		return "", err
	}
	root, err := vppRoot(doc)
	if err != nil {
		return "", err
	}
	sum, err := vppMutate(root, f)
	if err != nil {
		return "", err
	}
	nb, err := yaml.Marshal(doc.Content[0])
	if err != nil {
		return "", err
	}
	if old == nil {
		// first managed change: the baseline is whatever VPP runs now
		var base yaml.Node
		tmp, _ := os.CreateTemp("", "vppdump")
		tmp.Close()
		defer os.Remove(tmp.Name())
		if out, derr := vppcfgRun("dump", "-o", tmp.Name()); derr == nil {
			if bb, rerr := os.ReadFile(tmp.Name()); rerr == nil && yaml.Unmarshal(bb, &base) == nil {
				old = bb
			}
		} else {
			return "", errors.New("vppcfg dump failed: " + out)
		}
	}
	m.undo = old
	if err := m.apply(nb); err != nil {
		if old != nil {
			_ = m.apply(old)
		}
		m.undo = nil
		return "", err
	}
	if err := writeFileAtomic(vppcfgFile, nb, 0o640); err != nil {
		_ = m.rollbackLocked()
		return "", err
	}
	m.pending = &ifPending{Deadline: time.Now().Add(confirmWindow), Summary: sum}
	m.timer = time.AfterFunc(confirmWindow, func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.pending != nil {
			_ = m.rollbackLocked()
		}
	})
	return sum, nil
}

func (m *vppMgr) confirm() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pending == nil {
		return false
	}
	m.timer.Stop()
	m.pending, m.undo = nil, nil
	return true
}

func (m *vppMgr) handle(w http.ResponseWriter, r *http.Request) {
	reply := func(code int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(v)
	}
	switch r.FormValue("op") {
	case "confirm":
		if !m.confirm() {
			reply(400, map[string]string{"error": "nothing to confirm (already reverted?)"})
			return
		}
	case "revert":
		m.mu.Lock()
		var err error
		if m.pending == nil {
			err = errors.New("nothing to revert")
		} else {
			err = m.rollbackLocked()
		}
		m.mu.Unlock()
		if err != nil {
			reply(400, map[string]string{"error": err.Error()})
			return
		}
	default:
		if _, err := m.change(r.FormValue); err != nil {
			reply(400, map[string]string{"error": err.Error()})
			return
		}
	}
	reply(200, map[string]bool{"ok": true})
}

func (m *vppMgr) status(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, e := os.Stat(vppSock)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"vppcfg": vppcfgBin() != "", "vpp": e == nil, "pending": m.pending})
}

var _ = fmt.Sprintf
