package web

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// ParseAllow turns "10.0.0.0/8,192.0.2.5" into prefixes. A bare address becomes /32 or /128.
func ParseAllow(list []string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, e := range list {
		for _, p := range strings.Split(e, ",") {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			if !strings.Contains(p, "/") {
				a, err := netip.ParseAddr(p)
				if err != nil {
					return nil, err
				}
				out = append(out, netip.PrefixFrom(a, a.BitLen()))
				continue
			}
			pf, err := netip.ParsePrefix(p)
			if err != nil {
				return nil, err
			}
			out = append(out, pf.Masked())
		}
	}
	return out, nil
}

// ipAllowed applies --allow-from to the client address (X-Forwarded-For only with --trust-proxy).
func (s *server) ipAllowed(r *http.Request) bool {
	if len(s.allow) == 0 {
		return true
	}
	ip := s.auth.clientIP(r)
	if h, _, err := net.SplitHostPort(ip); err == nil {
		ip = h
	}
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return false
	}
	a = a.Unmap()
	for _, p := range s.allow {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// gate refuses clients outside --allow-from before anything else (login page included).
func (s *server) gate(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.ipAllowed(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		h.ServeHTTP(w, r)
	})
}
