package web

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// ifaceInfo is one network interface read from the host (/sys/class/net),
// the same data a router's interface list shows. Nothing here is simulated.
type ifaceInfo struct {
	Name   string   `json:"n"`
	Index  int      `json:"idx"`
	Type   string   `json:"type"`
	MTU    int      `json:"mtu"`
	MAC    string   `json:"mac"`
	Driver string   `json:"drv"`
	Speed  int      `json:"spd"` // Mbps, 0 = unknown
	Master string   `json:"master,omitempty"`
	Up     bool     `json:"up"`  // running
	Dis    bool     `json:"dis"` // administratively down
	V4     []string `json:"v4"`
	V6     []string `json:"v6"`
	RxB    uint64   `json:"rx"`
	TxB    uint64   `json:"tx"`
	RxP    uint64   `json:"rxk"`
	TxP    uint64   `json:"txk"`
	RxErr  uint64   `json:"rxe"`
	TxErr  uint64   `json:"txe"`
	RxDrop uint64   `json:"rxd"`
	TxDrop uint64   `json:"txd"`
}

func sysRead(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func sysUint(p string) uint64 {
	v, _ := strconv.ParseUint(sysRead(p), 10, 64)
	return v
}

var ifTypeNames = map[string]string{"bridge": "Bridge", "veth": "Veth", "vlan": "VLAN", "tun": "Tun", "tap": "Tap",
	"bond": "Bond", "wireguard": "WireGuard", "vxlan": "VXLAN", "dummy": "Dummy", "macvlan": "MACVLAN", "gre": "GRE", "ipip": "IPIP", "sit": "SIT"}

func listIfaces() []ifaceInfo {
	nics, err := net.Interfaces()
	if err != nil {
		return nil
	}
	out := make([]ifaceInfo, 0, len(nics))
	for _, n := range nics {
		b := "/sys/class/net/" + n.Name
		devtype := ""
		for _, l := range strings.Split(sysRead(b+"/uevent"), "\n") {
			if v, ok := strings.CutPrefix(l, "DEVTYPE="); ok {
				devtype = v
			}
		}
		if devtype == "" && sysRead(b+"/tun_flags") != "" {
			devtype = "tun"
		}
		if devtype == "" && sysRead(b+"/wireguard") != "" {
			devtype = "wireguard"
		}
		typ := "Ethernet"
		switch {
		case n.Flags&net.FlagLoopback != 0:
			typ = "Loopback"
		case devtype != "":
			if t, ok := ifTypeNames[devtype]; ok {
				typ = t
			} else {
				typ = strings.ToUpper(devtype[:1]) + devtype[1:]
			}
		}
		drv := "-"
		if l, err := filepath.EvalSymlinks(b + "/device/driver"); err == nil {
			drv = filepath.Base(l)
		} else if devtype != "" {
			drv = devtype
		}
		adm := n.Flags&net.FlagUp != 0
		run := adm && sysRead(b+"/carrier") != "0" && (sysRead(b+"/operstate") == "up" || sysRead(b+"/operstate") == "unknown")
		spd := 0
		if run {
			if v, err := strconv.Atoi(sysRead(b + "/speed")); err == nil && v > 0 {
				spd = v
			}
		}
		master := ""
		if l, err := os.Readlink(b + "/master"); err == nil {
			master = filepath.Base(l)
		}
		if master == "" { // VLAN / QinQ / macvlan etc: parent is the lower device
			if g, _ := filepath.Glob(b + "/lower_*"); len(g) == 1 {
				master = strings.TrimPrefix(filepath.Base(g[0]), "lower_")
			}
		}
		fi := ifaceInfo{Name: n.Name, Index: n.Index, Type: typ, MTU: n.MTU, MAC: strings.ToUpper(n.HardwareAddr.String()),
			Driver: drv, Speed: spd, Master: master, Up: run, Dis: !adm, V4: []string{}, V6: []string{},
			RxB: sysUint(b + "/statistics/rx_bytes"), TxB: sysUint(b + "/statistics/tx_bytes"),
			RxP: sysUint(b + "/statistics/rx_packets"), TxP: sysUint(b + "/statistics/tx_packets"),
			RxErr: sysUint(b + "/statistics/rx_errors"), TxErr: sysUint(b + "/statistics/tx_errors"),
			RxDrop: sysUint(b + "/statistics/rx_dropped"), TxDrop: sysUint(b + "/statistics/tx_dropped")}
		if fi.MAC == "" {
			fi.MAC = "00:00:00:00:00:00"
		}
		if addrs, err := n.Addrs(); err == nil {
			for _, a := range addrs {
				if ip, ok := a.(*net.IPNet); ok {
					if ip.IP.To4() != nil {
						fi.V4 = append(fi.V4, ip.String())
					} else {
						fi.V6 = append(fi.V6, ip.String())
					}
				}
			}
		}
		out = append(out, fi)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Index < out[j].Index })
	return out
}

func (s *server) ifacesAPI(w http.ResponseWriter, r *http.Request, sess *session) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	l := listIfaces()
	if l == nil {
		l = []ifaceInfo{}
	}
	_ = json.NewEncoder(w).Encode(l)
}

func (s *server) ifacesPage(w http.ResponseWriter, r *http.Request, sess *session) {
	s.render(w, "interfaces", ctxPage{Title: "Interfaces", Nav: "interfaces", User: sess.user, CSRF: sess.csrf})
}
