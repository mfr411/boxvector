//go:build linux

package web

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"sort"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// Live per-host traffic (iftop style) for the Traffic page. The sampler runs inside the root
// helper because an AF_PACKET socket needs CAP_NET_RAW. It only runs while someone is looking:
// each dev gets one sampler that stops itself after trafficIdle without a request.

const (
	trafficIdle   = 20 * time.Second
	trafficRing   = 40
	trafficMaxFlw = 4000
	trafficMaxDev = 3
)

type tflow struct {
	Src, Dst string
	ring     [trafficRing]uint64
	sec      int64
	total    uint64
	proto    string
	port     uint16
}

func (f *tflow) advance(now int64) {
	if f.sec == 0 {
		f.sec = now
		return
	}
	d := now - f.sec
	if d <= 0 {
		return
	}
	if d >= trafficRing {
		f.ring = [trafficRing]uint64{}
	} else {
		for i := int64(1); i <= d; i++ {
			f.ring[(f.sec+i)%trafficRing] = 0
		}
	}
	f.sec = now
}

// rate over the last n whole seconds (the current, partial second is left out), in bits/s.
func (f *tflow) rate(n int, now int64) uint64 {
	f.advance(now)
	var sum uint64
	for i := 1; i <= n; i++ {
		sum += f.ring[(now-int64(i)+trafficRing*2)%trafficRing]
	}
	return sum * 8 / uint64(n)
}

type trafficRow struct {
	Src   string `json:"src"`
	Dst   string `json:"dst"`
	Proto string `json:"proto"`
	Port  uint16 `json:"port"`
	R2    uint64 `json:"r2"`
	R10   uint64 `json:"r10"`
	R40   uint64 `json:"r40"`
	Total uint64 `json:"total"`
}

type sampler struct {
	dev   string
	fd    int
	mu    sync.Mutex
	flows map[string]*tflow
	last  time.Time
	pkts  uint64
	start time.Time
}

type trafficMgr struct {
	mu sync.Mutex
	by map[string]*sampler
}

func newTrafficMgr() *trafficMgr { return &trafficMgr{by: map[string]*sampler{}} }

func htons(v uint16) uint16 { return v<<8 | v>>8 }

func openPacket(dev string) (int, error) {
	ifc, err := net.InterfaceByName(dev)
	if err != nil {
		return -1, err
	}
	fd, err := syscall.Socket(syscall.AF_PACKET, syscall.SOCK_RAW|syscall.SOCK_CLOEXEC, int(htons(syscall.ETH_P_ALL)))
	if err != nil {
		return -1, err
	}
	if err := syscall.Bind(fd, &syscall.SockaddrLinklayer{Protocol: htons(syscall.ETH_P_ALL), Ifindex: ifc.Index}); err != nil {
		syscall.Close(fd)
		return -1, err
	}
	tv := syscall.Timeval{Sec: 1}
	_ = syscall.SetsockoptTimeval(fd, syscall.SOL_SOCKET, syscall.SO_RCVTIMEO, &tv)
	_ = syscall.SetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_RCVBUF, 4<<20)
	return fd, nil
}

// parsePacket returns src, dst, protocol name and the service port of an Ethernet frame.
func parsePacket(b []byte) (src, dst, proto string, port uint16, ok bool) {
	if len(b) < 14 {
		return
	}
	et := binary.BigEndian.Uint16(b[12:14])
	off := 14
	for i := 0; i < 2 && (et == 0x8100 || et == 0x88a8); i++ {
		if len(b) < off+4 {
			return
		}
		et = binary.BigEndian.Uint16(b[off+2 : off+4])
		off += 4
	}
	var pr byte
	var l4 int
	switch et {
	case 0x0800:
		if len(b) < off+20 {
			return
		}
		ihl := int(b[off]&0x0f) * 4
		if ihl < 20 {
			return
		}
		src, dst = net.IP(b[off+12:off+16]).String(), net.IP(b[off+16:off+20]).String()
		pr, l4 = b[off+9], off+ihl
		if binary.BigEndian.Uint16(b[off+6:off+8])&0x1fff != 0 {
			pr = 0xff // later fragment: no L4 header
		}
	case 0x86dd:
		if len(b) < off+40 {
			return
		}
		src, dst = net.IP(b[off+8:off+24]).String(), net.IP(b[off+24:off+40]).String()
		pr, l4 = b[off+6], off+40
	default:
		return
	}
	switch pr {
	case 6, 17:
		proto = map[byte]string{6: "tcp", 17: "udp"}[pr]
		if len(b) >= l4+4 {
			sp, dp := binary.BigEndian.Uint16(b[l4:l4+2]), binary.BigEndian.Uint16(b[l4+2:l4+4])
			// the lower port is almost always the service
			port = dp
			if sp < dp {
				port = sp
			}
		}
	case 1:
		proto = "icmp"
	case 58:
		proto = "icmp6"
	case 47:
		proto = "gre"
	case 89:
		proto = "ospf"
	case 50:
		proto = "esp"
	default:
		proto = "ip"
	}
	return src, dst, proto, port, true
}

func (s *sampler) run(m *trafficMgr) {
	buf := make([]byte, 160)
	for {
		s.mu.Lock()
		idle := time.Since(s.last) > trafficIdle
		s.mu.Unlock()
		if idle {
			break
		}
		// MSG_TRUNC: n is the real frame length even though only the headers are copied.
		n, _, errno := rawRecv(s.fd, buf)
		if errno != 0 {
			if errno == syscall.EAGAIN || errno == syscall.EINTR {
				continue
			}
			break
		}
		cp := n
		if cp > len(buf) {
			cp = len(buf)
		}
		src, dst, proto, port, ok := parsePacket(buf[:cp])
		if !ok {
			continue
		}
		now := time.Now().Unix()
		k := src + ">" + dst + "/" + proto
		s.mu.Lock()
		s.pkts++
		f := s.flows[k]
		if f == nil {
			if len(s.flows) >= trafficMaxFlw {
				s.evict(now)
			}
			f = &tflow{Src: src, Dst: dst, proto: proto}
			s.flows[k] = f
		}
		f.advance(now)
		f.ring[now%trafficRing] += uint64(n)
		f.total += uint64(n)
		if port != 0 {
			f.port = port
		}
		s.mu.Unlock()
	}
	syscall.Close(s.fd)
	m.mu.Lock()
	if m.by[s.dev] == s {
		delete(m.by, s.dev)
	}
	m.mu.Unlock()
}

// evict drops the flows that carried the least over the last 40 s (lock held).
func (s *sampler) evict(now int64) {
	type kv struct {
		k string
		v uint64
	}
	var l []kv
	for k, f := range s.flows {
		l = append(l, kv{k, f.rate(trafficRing-1, now)})
	}
	sort.Slice(l, func(i, j int) bool { return l[i].v < l[j].v })
	for i := 0; i < len(l)/4; i++ {
		delete(s.flows, l[i].k)
	}
}

func rawRecv(fd int, b []byte) (int, int, syscall.Errno) {
	n, _, e := syscall.Syscall6(syscall.SYS_RECVFROM, uintptr(fd), uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)), uintptr(syscall.MSG_TRUNC), 0, 0)
	return int(n), 0, e
}

func (m *trafficMgr) get(dev string) (*sampler, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s := m.by[dev]; s != nil {
		s.mu.Lock()
		s.last = time.Now()
		s.mu.Unlock()
		return s, nil
	}
	if len(m.by) >= trafficMaxDev {
		return nil, errors.New("too many interfaces are being sampled; close another Traffic view first")
	}
	fd, err := openPacket(dev)
	if err != nil {
		return nil, err
	}
	s := &sampler{dev: dev, fd: fd, flows: map[string]*tflow{}, last: time.Now(), start: time.Now()}
	m.by[dev] = s
	go s.run(m)
	return s, nil
}

func (s *sampler) top(limit int) ([]trafficRow, uint64) {
	now := time.Now().Unix()
	s.mu.Lock()
	defer s.mu.Unlock()
	rows := make([]trafficRow, 0, len(s.flows))
	var tot uint64
	for _, f := range s.flows {
		r := trafficRow{Src: f.Src, Dst: f.Dst, Proto: f.proto, Port: f.port, R2: f.rate(2, now), R10: f.rate(10, now), R40: f.rate(40-1, now), Total: f.total}
		tot += r.R2
		if r.R40 == 0 && r.R10 == 0 && r.R2 == 0 {
			continue
		}
		rows = append(rows, r)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].R10 != rows[j].R10 {
			return rows[i].R10 > rows[j].R10
		}
		return rows[i].Total > rows[j].Total
	})
	if len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, tot
}

func (m *trafficMgr) handle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	dev := r.FormValue("dev")
	if !reDev.MatchString(dev) {
		w.WriteHeader(400)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid interface"})
		return
	}
	s, err := m.get(dev)
	if err != nil {
		w.WriteHeader(400)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	rows, tot := s.top(60)
	warm := time.Since(s.start) < 3*time.Second
	_ = json.NewEncoder(w).Encode(map[string]any{"dev": dev, "rows": rows, "total_bps": tot, "packets": s.pkts, "warming": warm})
}
