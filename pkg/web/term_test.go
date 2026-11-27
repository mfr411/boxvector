package web

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestCanTerminal(t *testing.T) {
	if !canTerminal("root") {
		t.Fatal("root must be allowed")
	}
	if canTerminal("definitely-not-a-user-xyz") {
		t.Fatal("unknown user must be refused")
	}
}

func TestSetPeerKey(t *testing.T) {
	doc := "peers:\n  A:\n    asn: 65001\n    neighbors: [192.0.2.1]\n  B:\n    asn: 65002\n    neighbors: [192.0.2.2]\n"
	off := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"}
	out, err := setPeerKey(doc, "A", "disabled", off)
	if err != nil || !strings.Contains(out, "disabled: true") {
		t.Fatalf("disable: %v\n%s", err, out)
	}
	if strings.Count(out, "disabled") != 1 {
		t.Fatalf("B must stay untouched:\n%s", out)
	}
	back, err := setPeerKey(out, "A", "disabled", nil)
	if err != nil || strings.Contains(back, "disabled") {
		t.Fatalf("enable: %v\n%s", err, back)
	}
	if _, err := setPeerKey(doc, "nope", "disabled", off); err == nil {
		t.Fatal("unknown peer must fail")
	}
}
