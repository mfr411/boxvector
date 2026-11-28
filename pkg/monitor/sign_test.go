package monitor

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestReleaseSignature(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	other, _, _ := ed25519.GenerateKey(rand.Reader)
	file, sha := "boxvector_1.0.0_amd64.deb", "abc123"
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, signMessage(file, sha)))
	keys := []ed25519.PublicKey{pub}

	if err := verifyWith(keys, file, sha, sig); err != nil {
		t.Fatalf("valid signature rejected: %v", err)
	}
	for name, c := range map[string]struct {
		keys           []ed25519.PublicKey
		file, sha, sig string
	}{
		"other file name": {keys, "boxvector_9.9.9_amd64.deb", sha, sig},
		"other content":   {keys, file, "def456", sig},
		"no signature":    {keys, file, sha, ""},
		"garbage":         {keys, file, sha, "not-base64!!"},
		"untrusted key":   {[]ed25519.PublicKey{other}, file, sha, sig},
		"no keys at all":  {nil, file, sha, sig},
	} {
		if verifyWith(c.keys, c.file, c.sha, c.sig) == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestSignDebRoundTrip(t *testing.T) {
	d := t.TempDir()
	key, deb := filepath.Join(d, "k"), filepath.Join(d, "boxvector_1.0.0_amd64.deb")
	pubHex, err := KeyGen(key)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(deb, []byte("!<arch>\npayload"), 0o644)
	sig, err := SignDeb(key, deb)
	if err != nil {
		t.Fatal(err)
	}
	pub, _ := hex.DecodeString(pubHex)
	sha, _ := fileSHA(deb)
	if err := verifyWith([]ed25519.PublicKey{pub}, filepath.Base(deb), sha, sig); err != nil {
		t.Fatalf("round trip: %v", err)
	}
	os.WriteFile(deb, []byte("!<arch>\ntampered"), 0o644)
	sha, _ = fileSHA(deb)
	if verifyWith([]ed25519.PublicKey{pub}, filepath.Base(deb), sha, sig) == nil {
		t.Fatal("tampered package accepted")
	}
}

func TestDownloadRequiresTrustedSignature(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	dir := t.TempDir()
	kf := filepath.Join(dir, "release.pub")
	os.WriteFile(kf, []byte(hex.EncodeToString(pub)+"\n"), 0o644)
	old := releaseKeyFileOverride
	releaseKeyFileOverride = kf
	defer func() { releaseKeyFileOverride = old }()

	body := []byte("!<arch>\nfake package")
	sum := sha256.Sum256(body)
	sha := hex.EncodeToString(sum[:])
	file := "boxvector_1.0.0_amd64.deb"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(body) }))
	defer srv.Close()
	c := AgentConf{URL: srv.URL, Secret: "s"}
	good := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, signMessage(file, sha)))
	_, evil, _ := ed25519.GenerateKey(rand.Reader)
	bad := base64.StdEncoding.EncodeToString(ed25519.Sign(evil, signMessage(file, sha)))

	for name, tc := range map[string]struct {
		sig string
		ok  bool
	}{"signed by trusted key": {good, true}, "no signature": {"", false}, "signed by attacker": {bad, false}} {
		dst := filepath.Join(dir, name)
		err := download(c, Order{File: file, SHA256: sha, Size: int64(len(body)), Sig: tc.sig}, dst)
		if (err == nil) != tc.ok {
			t.Errorf("%s: err=%v", name, err)
		}
		if _, e := os.Stat(dst); (e == nil) != tc.ok {
			t.Errorf("%s: file present=%v, want %v", name, e == nil, tc.ok)
		}
	}
}
