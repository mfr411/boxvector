package irr

import "testing"

func TestCheckQuery(t *testing.T) {
	ok := []string{"AS-EXAMPLE", "RIPE::AS-FOO", "AS65530", "AS-FOO.BAR"}
	bad := []string{"-h", "AS-FOO -h evil", "AS;rm", "", "AS\nX"}
	for _, s := range ok {
		if err := checkQuery(s, "rr.ntt.net"); err != nil {
			t.Errorf("%q rejected: %v", s, err)
		}
	}
	for _, s := range bad {
		if checkQuery(s, "rr.ntt.net") == nil {
			t.Errorf("%q accepted", s)
		}
	}
	if checkQuery("AS-FOO", "-x evil") == nil {
		t.Error("bad server accepted")
	}
}
