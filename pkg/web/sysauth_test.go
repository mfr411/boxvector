//go:build cgo && linux

package web

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSystemLogin(t *testing.T) {
	// sha512-crypt of "s3cret-pw" (salt "saltsalt"), made with the system libcrypt via mkpasswd
	h := os.Getenv("BV_TEST_ROOT_HASH")
	if h == "" {
		t.Skip("BV_TEST_ROOT_HASH not set")
	}
	f := filepath.Join(t.TempDir(), "shadow")
	_ = os.WriteFile(f, []byte("root:"+h+":19000:0:99999:7:::\nnobody:"+h+":19000:0:99999:7:::\ndaemon:!:19000:0:99999:7:::\n"), 0o600)
	if !systemLogin(f, "root", "s3cret-pw") {
		t.Fatal("root with correct password must pass")
	}
	for _, c := range [][2]string{{"root", "wrong"}, {"nobody", "s3cret-pw"}, {"daemon", ""}, {"ghost", "s3cret-pw"}, {"root:x", "s3cret-pw"}, {"", ""}} {
		if systemLogin(f, c[0], c[1]) {
			t.Fatalf("must be refused: %q", c[0])
		}
	}
}
