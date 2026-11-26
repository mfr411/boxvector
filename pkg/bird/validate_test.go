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
