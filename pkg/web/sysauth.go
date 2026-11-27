package web

import (
	"os"
	"os/user"
	"strings"
)

// shadowHash returns the password hash of a local account from shadowFile.
func shadowHash(shadowFile, name string) (string, bool) {
	b, err := os.ReadFile(shadowFile)
	if err != nil {
		return "", false
	}
	for _, l := range strings.Split(string(b), "\n") {
		f := strings.SplitN(l, ":", 3)
		if len(f) == 3 && f[0] == name {
			return f[1], true
		}
	}
	return "", false
}

// systemAllowed: root and members of the sudo/wheel/admin groups may sign in.
func systemAllowed(name string) bool {
	u, err := user.Lookup(name)
	if err != nil {
		return false
	}
	if u.Uid == "0" {
		return true
	}
	gids, err := u.GroupIds()
	if err != nil {
		return false
	}
	for _, id := range gids {
		if g, err := user.LookupGroupId(id); err == nil && (g.Name == "sudo" || g.Name == "wheel" || g.Name == "admin") {
			return true
		}
	}
	return false
}

const dummyShadow = "$6$rounds=5000$boxvectordummy$0000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000"

// systemLogin verifies a server account (shadow). Unknown users burn equal time.
func systemLogin(shadowFile, name, password string) bool {
	if name == "" || len(name) > 32 || strings.ContainsAny(name, ":/ \x00\n") {
		return false
	}
	h, ok := shadowHash(shadowFile, name)
	if !ok || !systemAllowed(name) {
		cryptVerify(password, dummyShadow)
		return false
	}
	return cryptVerify(password, h)
}
