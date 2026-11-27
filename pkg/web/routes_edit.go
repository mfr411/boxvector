package web

import (
	"fmt"
	"net/http"
	"net/netip"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Static routes live in kernel.static-routes of the config file. Everything here edits that list
// through the same review / dry-run / Apply flow as peers.

var reRouteComment = regexp.MustCompile(`^[A-Za-z0-9 ._:/,()+-]{0,80}$`)

type staticCfg struct {
	Prefix, Via, Type, Comment string
	Distance                   int
	Disabled                   bool
}

func mapGet(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func strNode(v string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v}
}

// staticSeq returns kernel.static-routes, creating the path when create is set.
func staticSeq(root *yaml.Node, create bool) *yaml.Node {
	if len(root.Content) == 0 {
		if !create {
			return nil
		}
		root.Kind = yaml.DocumentNode
		root.Content = []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}
	}
	top := root.Content[0]
	if top.Kind != yaml.MappingNode {
		return nil
	}
	k := mapGet(top, "kernel")
	if k == nil {
		if !create {
			return nil
		}
		k = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		top.Content = append(top.Content, strNode("kernel"), k)
	} else if k.Kind != yaml.MappingNode {
		if !create {
			return nil
		}
		*k = yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	}
	sq := mapGet(k, "static-routes")
	if sq == nil {
		if !create {
			return nil
		}
		sq = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		k.Content = append(k.Content, strNode("static-routes"), sq)
	} else if sq.Kind != yaml.SequenceNode {
		if !create {
			return nil
		}
		*sq = yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	}
	return sq
}

func encodeDoc(root *yaml.Node) (string, error) {
	var b strings.Builder
	e := yaml.NewEncoder(&b)
	e.SetIndent(2)
	if err := e.Encode(root); err != nil {
		return "", err
	}
	return b.String(), nil
}

func parseDoc(doc string) (*yaml.Node, error) {
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(doc), &root); err != nil {
		return nil, fmt.Errorf("current config is not valid YAML: %w", err)
	}
	return &root, nil
}

func findStatic(sq *yaml.Node, prefix string) (int, *yaml.Node) {
	if sq == nil {
		return -1, nil
	}
	for i, it := range sq.Content {
		if it.Kind == yaml.MappingNode {
			if p := mapGet(it, "prefix"); p != nil && p.Value == prefix {
				return i, it
			}
		}
	}
	return -1, nil
}

// readStatics lists the static routes defined in the config text.
func readStatics(doc string) []staticCfg {
	root, err := parseDoc(doc)
	if err != nil {
		return nil
	}
	sq := staticSeq(root, false)
	if sq == nil {
		return nil
	}
	var out []staticCfg
	for _, it := range sq.Content {
		if it.Kind != yaml.MappingNode {
			continue
		}
		var c staticCfg
		if it.Decode(&struct {
			Prefix   *string `yaml:"prefix"`
			Via      *string `yaml:"via"`
			Type     *string `yaml:"type"`
			Comment  *string `yaml:"comment"`
			Distance *int    `yaml:"distance"`
			Disabled *bool   `yaml:"disabled"`
		}{&c.Prefix, &c.Via, &c.Type, &c.Comment, &c.Distance, &c.Disabled}) != nil {
			continue
		}
		if c.Type == "" {
			c.Type = "unicast"
		}
		out = append(out, c)
	}
	return out
}

// validateStatic normalises and checks one route the same way the generator does.
func validateStatic(prefix, typ, via, dist, comment string) (staticCfg, error) {
	var c staticCfg
	p, err := netip.ParsePrefix(strings.TrimSpace(prefix))
	if err != nil {
		return c, fmt.Errorf("Destination must be a prefix like 192.0.2.0/24 or 2001:db8::/32.")
	}
	if p.Masked() != p {
		return c, fmt.Errorf("%s has host bits set, use %s.", p, p.Masked())
	}
	c.Prefix = p.String()
	c.Type = strings.TrimSpace(typ)
	if c.Type == "" {
		c.Type = "unicast"
	}
	via = strings.TrimSpace(via)
	switch c.Type {
	case "unicast":
		a, err := netip.ParseAddr(strings.SplitN(via, "%", 2)[0])
		if err != nil {
			return c, fmt.Errorf("Gateway must be an IP address (optional %%interface suffix).")
		}
		if a.Is4() != p.Addr().Is4() {
			return c, fmt.Errorf("Gateway %s is not the same address family as %s.", a, p)
		}
		c.Via = via
	case "blackhole", "unreachable", "prohibit":
		if via != "" {
			return c, fmt.Errorf("Type %s does not take a gateway.", c.Type)
		}
	default:
		return c, fmt.Errorf("Unknown type %q.", c.Type)
	}
	if d := strings.TrimSpace(dist); d != "" {
		n, err := strconv.Atoi(d)
		if err != nil || n < 1 || n > 255 {
			return c, fmt.Errorf("Distance must be 1-255 (empty = BIRD default).")
		}
		c.Distance = n
	}
	if !reRouteComment.MatchString(comment) {
		return c, fmt.Errorf("Comment: up to 80 characters, letters, digits and . _ : / , ( ) + - only.")
	}
	c.Comment = strings.TrimSpace(comment)
	return c, nil
}

func addStatic(doc string, c staticCfg) (string, error) {
	root, err := parseDoc(doc)
	if err != nil {
		return "", err
	}
	sq := staticSeq(root, true)
	if sq == nil {
		return "", fmt.Errorf("config file is not a YAML mapping")
	}
	if i, _ := findStatic(sq, c.Prefix); i >= 0 {
		return "", fmt.Errorf("A static route for %s already exists.", c.Prefix)
	}
	n := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	add := func(k string, v *yaml.Node) { n.Content = append(n.Content, strNode(k), v) }
	add("prefix", strNode(c.Prefix))
	if c.Via != "" {
		add("via", strNode(c.Via))
	}
	if c.Type != "unicast" {
		add("type", strNode(c.Type))
	}
	if c.Distance > 0 {
		add("distance", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.Itoa(c.Distance)})
	}
	if c.Comment != "" {
		add("comment", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: c.Comment, Style: yaml.DoubleQuotedStyle})
	}
	sq.Content = append(sq.Content, n)
	return encodeDoc(root)
}

// editStatic changes one key of a static route (val nil = remove the key); key "" removes the whole route.
func editStatic(doc, prefix, key string, val *yaml.Node) (string, error) {
	root, err := parseDoc(doc)
	if err != nil {
		return "", err
	}
	sq := staticSeq(root, false)
	i, it := findStatic(sq, prefix)
	if i < 0 {
		return "", fmt.Errorf("%s is not a static route of the config file (learned or connected routes cannot be changed here).", prefix)
	}
	if key == "" {
		sq.Content = append(sq.Content[:i], sq.Content[i+1:]...)
		if len(sq.Content) == 0 { // leave no empty kernel/static-routes behind
			k := mapGet(root.Content[0], "kernel")
			dropKey(k, "static-routes")
			if len(k.Content) == 0 {
				dropKey(root.Content[0], "kernel")
			}
		}
		return encodeDoc(root)
	}
	for j := 0; j+1 < len(it.Content); j += 2 {
		if it.Content[j].Value == key {
			if val == nil {
				it.Content = append(it.Content[:j], it.Content[j+2:]...)
			} else {
				it.Content[j+1] = val
			}
			return encodeDoc(root)
		}
	}
	if val != nil {
		it.Content = append(it.Content, strNode(key), val)
	}
	return encodeDoc(root)
}

// annotateStatics marks BIRD rows that come from kernel.static-routes and adds the disabled ones.
func (s *server) annotateStatics(rows []routeRow) []routeRow {
	cur, err := s.store.current()
	if err != nil {
		return rows
	}
	cfg := map[string]staticCfg{}
	for _, c := range readStatics(cur) {
		cfg[c.Prefix] = c
	}
	if len(cfg) == 0 {
		return rows
	}
	seen := map[string]bool{}
	for i := range rows {
		if c, ok := cfg[rows[i].Prefix]; ok && strings.HasPrefix(rows[i].Source, "static") {
			rows[i].Cfg, rows[i].Comment = true, c.Comment
			seen[c.Prefix] = true
		}
	}
	for _, c := range cfg {
		if c.Disabled && !seen[c.Prefix] {
			pf := ""
			if c.Distance > 0 {
				pf = strconv.Itoa(c.Distance)
			}
			rows = append(rows, routeRow{Prefix: c.Prefix, Type: c.Type, Source: "static (disabled)", Via: c.Via, Pref: pf, Cfg: true, Off: true, Comment: c.Comment})
		}
	}
	return rows
}

// ---- handlers ----

func (s *server) routePage(w http.ResponseWriter, sess *session, name string, d map[string]any) {
	s.render(w, name, ctxPage{Title: "Routes", Nav: "routes", User: sess.user, CSRF: sess.csrf, Data: d})
}

func routeForm(r *http.Request) map[string]string {
	f := map[string]string{}
	for _, k := range []string{"prefix", "type", "via", "distance", "comment"} {
		f[k] = strings.TrimSpace(r.PostFormValue(k))
	}
	return f
}

func (s *server) routeNewPage(w http.ResponseWriter, r *http.Request, sess *session) {
	s.routePage(w, sess, "route_new", map[string]any{"F": map[string]string{"type": "unicast"}, "Err": ""})
}

func (s *server) routeNewPost(w http.ResponseWriter, r *http.Request, sess *session) {
	f := routeForm(r)
	fail := func(msg string) { s.routePage(w, sess, "route_new", map[string]any{"F": f, "Err": msg}) }
	c, err := validateStatic(f["prefix"], f["type"], f["via"], f["distance"], f["comment"])
	if err != nil {
		fail(err.Error())
		return
	}
	cur, err := s.store.current()
	if err != nil {
		fail("Read config: " + err.Error())
		return
	}
	cand, err := addStatic(cur, c)
	if err != nil {
		fail(err.Error())
		return
	}
	s.review(w, sess, cur, cand, "Add static route "+c.Prefix)
}

func (s *server) routeActPage(w http.ResponseWriter, r *http.Request, sess *session) {
	s.routeActRender(w, sess, r.URL.Query().Get("prefix"), r.URL.Query().Get("do"), "")
}

func (s *server) routeActRender(w http.ResponseWriter, sess *session, prefix, do, msg string) {
	d := map[string]any{"Prefix": prefix, "Do": do, "Err": msg}
	switch do {
	case "remove":
		d["Title"], d["Text"], d["Danger"] = "Remove route", "Remove the static route to", true
	case "disable":
		d["Title"], d["Text"], d["Danger"] = "Disable route", "Disable the static route to", true
	default:
		d["Title"], d["Text"] = "Enable route", "Enable the static route to"
	}
	s.routePage(w, sess, "route_act", d)
}

func (s *server) routeActPost(w http.ResponseWriter, r *http.Request, sess *session) {
	prefix, do := strings.TrimSpace(r.PostFormValue("prefix")), r.PostFormValue("do")
	if do != "remove" && do != "enable" && do != "disable" {
		s.routeActRender(w, sess, prefix, do, "Unknown action.")
		return
	}
	cur, err := s.store.current()
	if err != nil {
		s.routeActRender(w, sess, prefix, do, "Read config: "+err.Error())
		return
	}
	var cand string
	switch do {
	case "remove":
		cand, err = editStatic(cur, prefix, "", nil)
	case "disable":
		cand, err = editStatic(cur, prefix, "disabled", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"})
	default:
		cand, err = editStatic(cur, prefix, "disabled", nil)
	}
	if err != nil {
		s.routeActRender(w, sess, prefix, do, err.Error())
		return
	}
	s.review(w, sess, cur, cand, strings.ToUpper(do[:1])+do[1:]+" static route "+prefix)
}

func (s *server) routeCommentPage(w http.ResponseWriter, r *http.Request, sess *session) {
	p := r.URL.Query().Get("prefix")
	cm := ""
	if cur, err := s.store.current(); err == nil {
		for _, c := range readStatics(cur) {
			if c.Prefix == p {
				cm = c.Comment
			}
		}
	}
	s.routePage(w, sess, "route_comment", map[string]any{"Prefix": p, "Comment": cm, "Err": ""})
}

func (s *server) routeCommentPost(w http.ResponseWriter, r *http.Request, sess *session) {
	prefix, cm := strings.TrimSpace(r.PostFormValue("prefix")), strings.TrimSpace(r.PostFormValue("comment"))
	fail := func(msg string) {
		s.routePage(w, sess, "route_comment", map[string]any{"Prefix": prefix, "Comment": cm, "Err": msg})
	}
	if !reRouteComment.MatchString(cm) {
		fail("Comment: up to 80 characters, letters, digits and . _ : / , ( ) + - only.")
		return
	}
	cur, err := s.store.current()
	if err != nil {
		fail("Read config: " + err.Error())
		return
	}
	var val *yaml.Node
	if cm != "" {
		val = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: cm, Style: yaml.DoubleQuotedStyle}
	}
	cand, err := editStatic(cur, prefix, "comment", val)
	if err != nil {
		fail(err.Error())
		return
	}
	s.review(w, sess, cur, cand, "Comment static route "+prefix)
}

func dropKey(m *yaml.Node, key string) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return
		}
	}
}
