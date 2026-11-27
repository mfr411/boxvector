//go:build !cgo || !linux

package web

// cryptVerify is unavailable without cgo; system login is then always refused.
func cryptVerify(password, hash string) bool { return false }
