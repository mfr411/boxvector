package monitor

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// releaseKeys are the Ed25519 public keys (hex) allowed to sign releases. Agents refuse a package
// whose signature does not verify against one of them, so a compromised monitor cannot push code.
// More keys (rotation) can be dropped in /etc/boxvector/release.pub, one hex key per line.
var releaseKeys = []string{
	"53a4559501acae7b0869932f790abe00cb6a5d6cf8ab405d7085169c23995912",
}

const releaseKeyFile = "/etc/boxvector/release.pub"

// releaseKeyFileOverride lets tests point at a temporary key file.
var releaseKeyFileOverride string

// signMessage binds the signature to the file name (version + arch) and its content hash.
func signMessage(file, sha string) []byte {
	return []byte("boxvector-release-v1:" + file + ":" + sha)
}

func fileSHA(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func trustedKeys(extra string) []ed25519.PublicKey {
	var out []ed25519.PublicKey
	add := func(s string) {
		if b, err := hex.DecodeString(strings.TrimSpace(s)); err == nil && len(b) == ed25519.PublicKeySize {
			out = append(out, ed25519.PublicKey(b))
		}
	}
	for _, k := range releaseKeys {
		add(k)
	}
	if b, err := os.ReadFile(extra); err == nil {
		for _, l := range strings.Split(string(b), "\n") {
			if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
				add(l)
			}
		}
	}
	return out
}

// VerifyRelease checks a base64 Ed25519 signature for file (name + sha256 hex) against the trusted keys.
func VerifyRelease(file, sha, sig string) error {
	f := releaseKeyFile
	if releaseKeyFileOverride != "" {
		f = releaseKeyFileOverride
	}
	return verifyWith(trustedKeys(f), file, sha, sig)
}

func verifyWith(keys []ed25519.PublicKey, file, sha, sig string) error {
	if len(keys) == 0 {
		return errors.New("no release signing key is configured")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(sig))
	if err != nil || len(raw) != ed25519.SignatureSize {
		return errors.New("signature missing or malformed")
	}
	for _, k := range keys {
		if ed25519.Verify(k, signMessage(file, sha), raw) {
			return nil
		}
	}
	return errors.New("signature does not match any trusted release key")
}

// KeyGen writes a new private key (mode 600, seed hex) and returns the public key in hex.
func KeyGen(path string) (string, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(hex.EncodeToString(priv.Seed())+"\n"), 0o600); err != nil {
		return "", err
	}
	return hex.EncodeToString(pub), nil
}

// SignDeb signs a .deb with the private key at keyPath and returns the base64 signature.
func SignDeb(keyPath, deb string) (string, error) {
	b, err := os.ReadFile(keyPath)
	if err != nil {
		return "", err
	}
	seed, err := hex.DecodeString(strings.TrimSpace(string(b)))
	if err != nil || len(seed) != ed25519.SeedSize {
		return "", fmt.Errorf("%s: not a boxvector release key", keyPath)
	}
	sha, err := fileSHA(deb)
	if err != nil {
		return "", err
	}
	sig := ed25519.Sign(ed25519.NewKeyFromSeed(seed), signMessage(filepath.Base(deb), sha))
	return base64.StdEncoding.EncodeToString(sig), nil
}

// VerifyDebFile hashes the file on disk and verifies its signature.
func VerifyDebFile(deb, sig string) error {
	sha, err := fileSHA(deb)
	if err != nil {
		return err
	}
	return VerifyRelease(filepath.Base(deb), sha, sig)
}
