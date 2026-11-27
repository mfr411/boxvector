//go:build cgo && linux

package web

/*
#cgo LDFLAGS: -lcrypt
#include <crypt.h>
#include <stdlib.h>
#include <string.h>

static int bv_check(const char *pw, const char *hash) {
	struct crypt_data *d = calloc(1, sizeof(struct crypt_data));
	if (!d) return 0;
	char *r = crypt_r(pw, hash, d);
	int ok = (r != NULL && strlen(r) == strlen(hash) && strcmp(r, hash) == 0);
	explicit_bzero(d, sizeof(*d));
	free(d);
	return ok;
}
*/
import "C"

import "unsafe"

// cryptVerify checks a password against a crypt(3) hash from /etc/shadow
// (yescrypt, sha512, sha256, ... as supported by the system libcrypt).
func cryptVerify(password, hash string) bool {
	if hash == "" || hash[0] == '!' || hash[0] == '*' {
		return false
	}
	p, h := C.CString(password), C.CString(hash)
	defer C.free(unsafe.Pointer(p))
	defer C.free(unsafe.Pointer(h))
	return C.bv_check(p, h) == 1
}
