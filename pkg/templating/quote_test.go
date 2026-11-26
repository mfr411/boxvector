package templating

import "testing"

func TestBirdQuote(t *testing.T) {
	cases := map[string]string{
		`plain peer`:                 `plain peer`,
		`x"; import all; #`:          `x'; import all; #`,
		"line1\nprotocol bgp evil {": "line1protocol bgp evil {",
		`back\slash`:                 `back\slash`,
	}
	for in, want := range cases {
		if got := BirdQuote(in); got != want {
			t.Errorf("BirdQuote(%q) = %q, want %q", in, got, want)
		}
	}
}
