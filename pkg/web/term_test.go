package web

import (
	"strings"
	"testing"
	"time"

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

func TestTermAuthorize(t *testing.T) {
	h := &termHelper{m: map[string]*termSess{}, fails: map[string][]time.Time{}}
	if c, _ := h.authorize("root", ""); c != 403 {
		t.Fatalf("empty password must be refused, got %d", c)
	}
	if c, _ := h.authorize("", "x"); c != 403 {
		t.Fatalf("empty user must be refused, got %d", c)
	}
	for i := 0; i < termFailMax; i++ {
		if c, _ := h.authorize("definitely-not-a-user-xyz", "wrong"); c != 403 {
			t.Fatalf("try %d: want 403, got %d", i, c)
		}
	}
	if c, _ := h.authorize("definitely-not-a-user-xyz", "wrong"); c != 429 {
		t.Fatalf("must lock after %d failures, got %d", termFailMax, c)
	}
}
