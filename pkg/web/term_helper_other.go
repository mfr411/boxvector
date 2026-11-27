//go:build !linux

package web

import "errors"

// RunTermHelper is only available on Linux.
func RunTermHelper(string) error { return errors.New("terminal helper is Linux only") }
