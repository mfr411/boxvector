//go:build !linux

package web

import "errors"

type kroute struct {
	Dst     string `json:"dst"`
	Via     string `json:"via"`
	Dev     string `json:"dev"`
	Metric  uint32 `json:"metric"`
	Comment string `json:"comment"`
	Off     bool   `json:"off"`
}

func (k kroute) key() string { return k.Dst + "|" + k.Via + "|" + k.Dev }

// RunNetHelper is only available on Linux.
func RunNetHelper(string) error { return errors.New("net helper is Linux only") }
