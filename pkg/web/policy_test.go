package web

import (
	"strings"
	"testing"
)

func TestCompileRule(t *testing.T) {
	ok := []struct {
		in, want string
		d        polDir
	}{
		{"only origin 65001 65002", "if !((bgp_path.last ~ [65001, 65002])) then reject;", polImport},
		{"only prefix 10.0.0.0/8 le 24", "net ~ [10.0.0.0/8{8,24}]", polImport},
		{"reject path-len > 8", "if ((bgp_path.len > 8)) then reject;", polImport},
		{"suppress community 65000:666", "(65000,666) ~ bgp_community", polExport},
		{"med 50 when community 65000:100", "then { bgp_med = 50; }", polImport},
		{"prepend 2 when ipv6", "bgp_path.prepend(ASN); bgp_path.prepend(ASN);", polExport},
		{"prepend 1 when origin 65001", "bgp_path.prepend(ASN);", polImport},
		{"only origin 65001 and prefix 192.0.2.0/24", " && ", polImport},
		{"reject first-as 64512-65534", "64512..65534", polImport},
		{"only path 65001 * 65010", "[= 65001 * 65010 =]", polImport},
		{"only not path-contains AS64512", "!(", polImport},
		{"add-large-community 65000:1:2", "bgp_large_community.add((65000,1,2));", polExport},
		{"only prefix 2001:db8::/32 ge 48 le 48", "NET_IP6", polImport},
	}
	for _, c := range ok {
		got, err := compileRule(c.in, c.d)
		if err != nil || !strings.Contains(got, c.want) {
			t.Errorf("%q => %q, %v (want %q)", c.in, got, err, c.want)
		}
	}
	bad := []struct {
		in string
		d  polDir
	}{
		{"only origin abc", polImport}, {"only origin 65001 and origin 1 or origin 2", polImport},
		{"prepend 99", polExport}, {"local-pref 5", polImport},
		{"only prefix 10.0.0.1/8", polImport}, {"only prefix 10.0.0.0/8 le 40", polImport},
		{"only prefix 10.0.0.0/16 le 8", polImport}, {"med x", polExport}, {"nonsense 1", polImport},
		{"only", polImport}, {"only community 70000:1", polImport}, {"reject path-len ~ 3", polImport},
		{"only origin 65001; accept", polImport}, {"med 5 when", polExport},
	}
	for _, c := range bad {
		if got, err := compileRule(c.in, c.d); err == nil {
			t.Errorf("%q accepted: %q", c.in, got)
		}
	}
}

func TestPolicyRoundTrip(t *testing.T) {
	rules := "only origin 65001\nreject path-len > 8"
	v, msg := buildPolicyValue(rules, "bgp_med = 1;", polImport)
	if msg != "" {
		t.Fatal(msg)
	}
	r, raw := parsePolicyValue(v)
	if r != rules || raw != "bgp_med = 1;" {
		t.Fatalf("round trip: %q | %q", r, raw)
	}
	if v, _ := buildPolicyValue("", "", polImport); v != "" {
		t.Fatalf("empty gave %q", v)
	}
	if _, msg := buildPolicyValue("only origin x", "", polImport); !strings.Contains(msg, "rule 1") {
		t.Fatalf("msg %q", msg)
	}
}

func TestPeerPolicyRoundTrip(t *testing.T) {
	f := map[string]string{"name": "X", "asn": "65001", "neighbors": "192.0.2.2",
		"imp_rules": "only origin 65001", "imp_raw": "if (net.len > 30) then reject;", "exp_rules": "prepend 2\nmed 10"}
	p, msg := buildPeer(f)
	if msg != "" {
		t.Fatal(msg)
	}
	g := formFromPeer("X", p)
	for _, k := range []string{"imp_rules", "imp_raw", "exp_rules"} {
		if g[k] != f[k] {
			t.Errorf("%s: %q != %q", k, g[k], f[k])
		}
	}
	if g["exp_raw"] != "" {
		t.Errorf("exp_raw %q", g["exp_raw"])
	}
	f["imp_rules"], f["imp_raw"] = "", ""
	p, _ = buildPeer(f)
	for i := 0; i+1 < len(p.Content); i += 2 {
		if p.Content[i].Value == "pre-import-filter" {
			t.Error("empty policy still written")
		}
	}
}
