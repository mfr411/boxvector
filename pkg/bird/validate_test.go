package bird

import "testing"

func TestValidationErrorFormats(t *testing.T) {
	for _, s := range []string{
		"bird: ./AS65510_X.conf:10:22 syntax error, unexpected IMPORT",                                        // BIRD 2
		"bird: 2026-11-26 17:20:35.619 [0000] <FATAL> ./AS65510_X.conf:10:22 syntax error, unexpected IMPORT", // BIRD 3
	} {
		m := reValidationError.FindStringSubmatch(s)
		if m == nil || m[1] != "./AS65510_X.conf" || m[2] != "10" || m[3] != "22" || m[4] != "syntax error, unexpected IMPORT" {
			t.Errorf("parse %q -> %q", s, m)
		}
	}
}

func TestVersion(t *testing.T) {
	if ma, _, pa, ok := ParseVersion("BIRD version v2.0.12"); !ok || ma != 2 || pa != 12 {
		t.Error("parse v2.0.12")
	}
	if ma, mi, pa, ok := ParseVersion("BIRD version 3.4.0"); !ok || ma != 3 || mi != 4 || pa != 0 {
		t.Error("parse 3.4.0")
	}
	for v, want := range map[string]bool{"2.0.7": true, "2.20.0": true, "3.0": true, "2.0.6": false, "1.6.8": false} {
		if AtLeast(v, "2.0.7") != want {
			t.Errorf("AtLeast(%s) != %v", v, want)
		}
	}
}
