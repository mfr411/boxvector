package web

import (
	"html/template"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestDiff(t *testing.T) {
	if d := Diff("a\nb", "a\nb", 3); d != nil {
		t.Errorf("identical input gave diff %v", d)
	}
	d := Diff("a\nb\nc", "a\nB\nc", 0)
	if len(d) != 2 || d[0].Op != '-' || d[0].Text != "b" || d[1].Op != '+' || d[1].Text != "B" {
		t.Errorf("unexpected diff %v", d)
	}
}

func TestInsertPeer(t *testing.T) {
	doc := "# keep me\nasn: 65530\npeers:\n  A:\n    asn: 65510\n"
	p := &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{
		{Kind: yaml.ScalarNode, Value: "asn"}, {Kind: yaml.ScalarNode, Value: "65522", Tag: "!!int"}}}
	out, err := insertPeer(doc, "B", p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "# keep me") || !strings.Contains(out, "  B:\n    asn: 65522") {
		t.Errorf("unexpected output:\n%s", out)
	}
	if _, err := insertPeer(out, "b", p); err == nil {
		t.Error("duplicate peer name accepted")
	}
}

func TestSnapshotNames(t *testing.T) {
	s := &store{stateDir: t.TempDir()}
	for _, bad := range []string{"../x.yml", "/etc/passwd", "20261126-173017.089.yml/../../x"} {
		if _, err := s.ReadSnapshot(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestPasswordFile(t *testing.T) {
	f := filepath.Join(t.TempDir(), "users")
	if err := SetPassword(f, "admin", "short"); err == nil {
		t.Error("short password accepted")
	}
	if err := SetPassword(f, "admin", "a-long-password"); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(f); st.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", st.Mode().Perm())
	}
	a := newAuth(f, false)
	if _, _, ok := a.login("1.2.3.4", "admin", "wrong-password"); ok {
		t.Error("wrong password accepted")
	}
	if _, _, ok := a.login("1.2.3.4", "admin", "a-long-password"); !ok {
		t.Error("right password rejected")
	}
}

func TestPeerRulesTriState(t *testing.T) {
	seen := map[string]bool{}
	for _, r := range peerRules {
		if seen[r.Key] || r.Label == "" {
			t.Errorf("bad rule %+v", r)
		}
		seen[r.Key] = true
	}
	if len(ruleKeys()) != len(peerRules) || ruleKeys()[0] != "r_filter-bogon-routes" {
		t.Errorf("ruleKeys %v", ruleKeys())
	}
}

func TestPeerNewTemplateRenders(t *testing.T) {
	tp := template.Must(template.New("").ParseFS(assets, "templates/layout.html", "templates/peer_new.html"))
	for name, f := range map[string]map[string]string{"empty": nil, "filled": {"r_filter-rpki": "off", "name": "x"}} {
		var b strings.Builder
		if err := tp.ExecuteTemplate(&b, "layout", ctxPage{Title: "Add peer", Data: peerFormData(f, "")}); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if strings.Count(b.String(), "<select name=\"r_") != len(peerRules) {
			t.Errorf("%s: expected %d rule selects", name, len(peerRules))
		}
	}
}
