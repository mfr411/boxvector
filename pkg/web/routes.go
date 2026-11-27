package web

import (
	"encoding/hex"
	"net"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/mfr411/boxvector/pkg/bird"
)

const maxRouteRows = 2000

type routeRow struct {
	Prefix, Type, Source, Since, Pref, Via, Dev, Table string
	Cfg, Off                                           bool // defined in kernel.static-routes / disabled there
	Comment                                            string
}

var (
	reRtHead = regexp.MustCompile(`^(\S+)?\s+(\w+)\s+\[([^\]]*)\]\s*\*?\s*(?:\((\d+)[^)]*\))?`)
	reRtVia  = regexp.MustCompile(`via (\S+)(?: on (\S+))?`)
	reRtDev  = regexp.MustCompile(`^\s+dev (\S+)`)
)

// birdRoutes parses "show route table <t>" (plain output) into rows.
func birdRoutes(sock, table string) (rows []routeRow, total int, err error) {
	out, _, err := bird.RunCommand("show route table "+table, sock)
	if err != nil {
		return nil, 0, err
	}
	var cur *routeRow
	prefix := ""
	flush := func() {
		if cur != nil {
			total++
			if len(rows) < maxRouteRows {
				rows = append(rows, *cur)
			}
			cur = nil
		}
	}
	for _, l := range strings.Split(out, "\n") {
		if l == "" || strings.HasPrefix(l, "BIRD ") || strings.HasPrefix(l, "Table ") {
			continue
		}
		if l[0] != ' ' && l[0] != '\t' || strings.Contains(l, "[") {
			if m := reRtHead.FindStringSubmatch(l); m != nil {
				flush()
				if m[1] != "" {
					prefix = m[1]
				}
				src, since := m[3], ""
				if f := strings.Fields(src); len(f) > 1 {
					src, since = f[0], strings.Join(f[1:], " ")
				}
				cur = &routeRow{Prefix: prefix, Type: m[2], Source: src, Since: since, Pref: m[4], Table: table}
				if v := reRtVia.FindStringSubmatch(l); v != nil {
					cur.Via, cur.Dev = v[1], v[2]
				}
				continue
			}
		}
		if cur == nil {
			continue
		}
		if v := reRtVia.FindStringSubmatch(l); v != nil && cur.Via == "" {
			cur.Via, cur.Dev = v[1], v[2]
		} else if d := reRtDev.FindStringSubmatch(l); d != nil {
			cur.Dev = d[1]
		}
	}
	flush()
	return rows, total, nil
}

// kernelRoutes lists the main routing table straight from /proc/net (same data as
// `ip route`), so no child process is needed under the systemd sandbox.
func kernelRoutes() (rows []routeRow, total int, err error) {
	add := func(r routeRow) {
		total++
		if len(rows) < maxRouteRows {
			rows = append(rows, r)
		}
	}
	if b, e := os.ReadFile("/proc/net/route"); e == nil {
		for i, l := range strings.Split(string(b), "\n") {
			f := strings.Fields(l)
			if i == 0 || len(f) < 11 {
				continue
			}
			flags, _ := strconv.ParseUint(f[3], 16, 32)
			if flags&1 == 0 { // RTF_UP
				continue
			}
			dst, gw, mask := hexIP4(f[1]), hexIP4(f[2]), hexIP4(f[7])
			ones, _ := net.IPMask(mask.To4()).Size()
			r := routeRow{Prefix: dst.String() + "/" + strconv.Itoa(ones), Type: "unicast", Dev: f[0], Pref: f[6], Table: "4"}
			if flags&2 != 0 {
				r.Via = gw.String()
			}
			if r.Dev == "*" {
				r.Dev = ""
				r.Type = "blackhole"
				if flags&0x200 != 0 {
					r.Type = "unreachable"
				}
			}
			add(r)
		}
	}
	if b, e := os.ReadFile("/proc/net/ipv6_route"); e == nil {
		for _, l := range strings.Split(string(b), "\n") {
			f := strings.Fields(l)
			if len(f) < 10 {
				continue
			}
			flags, _ := strconv.ParseUint(f[8], 16, 32)
			plen, _ := strconv.ParseUint(f[1], 16, 8)
			metric, _ := strconv.ParseUint(f[5], 16, 32)
			// skip local/anycast host routes, multicast and the kernel's null template
			if flags&0x80000000 != 0 || metric == 0xffffffff || strings.HasPrefix(f[0], "ff") {
				continue
			}
			reject := flags&0x200 != 0
			if flags&1 == 0 && !reject {
				continue
			}
			r := routeRow{Prefix: hexIP6(f[0]).String() + "/" + strconv.Itoa(int(plen)), Type: "unicast", Dev: f[9], Pref: strconv.FormatUint(metric, 10), Table: "6"}
			if flags&2 != 0 {
				r.Via = hexIP6(f[4]).String()
			}
			if reject {
				r.Type, r.Dev = "blackhole", ""
				if plen == 0 {
					r.Type = "unreachable"
				}
			}
			add(r)
		}
	}
	return rows, total, nil
}

func hexIP4(s string) net.IP {
	n, _ := strconv.ParseUint(s, 16, 32)
	return net.IPv4(byte(n), byte(n>>8), byte(n>>16), byte(n>>24))
}

func hexIP6(s string) net.IP {
	b, _ := hex.DecodeString(s)
	return net.IP(b)
}
