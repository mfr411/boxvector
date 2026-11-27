package web

import "testing"

func TestCanTerminal(t *testing.T) {
	if !canTerminal("root") {
		t.Fatal("root must be allowed")
	}
	if canTerminal("definitely-not-a-user-xyz") {
		t.Fatal("unknown user must be refused")
	}
}
