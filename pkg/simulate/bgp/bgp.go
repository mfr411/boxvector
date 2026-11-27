// Package bgp implements the small subset of BGP-4 needed to send prefixes to
// a BIRD instance: OPEN, KEEPALIVE and UPDATE with a configurable set of path
// attributes. There is no FSM and no message parsing beyond header framing.
package bgp

import (
	"encoding/binary"
	"fmt"
	"net"
	"net/netip"
	"time"

	"github.com/mfr411/boxvector/pkg/simulate/attribute"
)

// Message types (RFC 4271 section 4.1).
const (
	msgOpen      = 1
	msgUpdate    = 2
	msgKeepalive = 4
)

const headerLen = 19

// Session is one BGP connection to a single neighbor.
type Session struct {
	conn net.Conn

	// LocalASN and PeerASN are exchanged in the OPEN message.
	LocalASN uint32
	PeerASN  uint32

	// RouterID identifies the simulator to the receiving BIRD instance.
	RouterID netip.Addr

	keepalive time.Duration
}

// Dial connects to neighbor and completes the OPEN/KEEPALIVE exchange.
func Dial(neighbor string, localASN, peerASN uint32, routerID netip.Addr, keepalive time.Duration) (*Session, error) {
	dialer := net.Dialer{Timeout: 5 * time.Second}
	conn, err := dialer.Dial("tcp", neighbor)
	if err != nil {
		return nil, err
	}
	s := &Session{conn: conn, LocalASN: localASN, PeerASN: peerASN, RouterID: routerID, keepalive: keepalive}
	if err = s.handshake(); err != nil {
		conn.Close()
		return nil, err
	}
	go s.keepalives()
	return s, nil
}

// Close ends the session.
func (s *Session) Close() error {
	return s.conn.Close()
}

func (s *Session) handshake() error {
	open := make([]byte, 16)
	binary.BigEndian.PutUint16(open[0:2], 4)  // version
	binary.BigEndian.PutUint16(open[2:4], uint16(s.LocalASN))
	binary.BigEndian.PutUint16(open[4:6], uint16(s.keepalive/time.Second))
	rid := s.RouterID.As4()
	copy(open[6:10], rid[:])
	open[10] = 1  // optional parameter: capability
	open[11] = 0  // one option
	open[12] = 4  // capability code 4 = multiprotocol
	open[13] = 6  // capability length
	binary.BigEndian.PutUint16(open[14:16], 1) // AFI IPv4

	if err := s.write(msgOpen, open); err != nil {
		return err
	}
	if err := s.write(msgKeepalive, nil); err != nil {
		return err
	}
	// Read until KEEPALIVE, acknowledging nothing else.
	for i := 0; i < 5; i++ {
		s.conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		var hdr [headerLen]byte
		if _, err := readFull(s.conn, hdr[:]); err != nil {
			return fmt.Errorf("read: %w", err)
		}
		body := make([]byte, binary.BigEndian.Uint16(hdr[16:18])-headerLen)
		if _, err := readFull(s.conn, body); err != nil {
			return fmt.Errorf("read body: %w", err)
		}
		switch hdr[18] {
		case msgKeepalive:
			return nil
		case msgOpen:
			gotASN := binary.BigEndian.Uint16(body[2:4])
			if gotASN != 0 && uint32(gotASN) != s.PeerASN {
				return fmt.Errorf("peer announced AS%d, expected AS%d", gotASN, s.PeerASN)
			}
		}
	}
	return fmt.Errorf("no keepalive from peer")
}

func (s *Session) keepalives() {
	t := time.NewTicker(s.keepalive / 3)
	defer t.Stop()
	for range t.C {
		if err := s.write(msgKeepalive, nil); err != nil {
			return
		}
	}
}

// Announce sends one UPDATE advertising prefix with the given path attributes.
func (s *Session) Announce(prefix netip.Prefix, path []uint32, attrs []attribute.Attribute) error {
	nlri := encodeNLRI(prefix)
	body := binary.BigEndian.AppendUint16(nil, 0) // withdrawn length

	for _, a := range append(baseAttrs(prefix, path), attrs...) {
		body = append(body, a.Bytes()...)
	}
	body = append(body, nlri...)
	return s.write(msgUpdate, body)
}

// Withdraw sends one UPDATE withdrawing prefix.
func (s *Session) Withdraw(prefix netip.Prefix) error {
	nlri := encodeNLRI(prefix)
	body := binary.BigEndian.AppendUint16(nil, uint16(len(nlri)))
	body = append(body, nlri...)
	body = binary.BigEndian.AppendUint16(body, 0)
	return s.write(msgUpdate, body)
}

// baseAttrs are always present: ORIGIN, AS_PATH and NEXT_HOP.
func baseAttrs(prefix netip.Prefix, path []uint32) []attribute.Attribute {
	nh := prefix.Addr()
	if prefix.Addr().Is6() {
		nh = netip.MustParseAddr("::ffff:127.0.0.1")
	}
	return []attribute.Attribute{
		attribute.New(attribute.Origin, attribute.OriginValue(0)),
		attribute.New(attribute.ASPath, attribute.ASPathValue(path, true)),
		attribute.New(attribute.NextHop, attribute.NextHopValue(nh)),
	}
}

// encodeNLRI encodes an IPv4 prefix in NLRI form (length byte + bits).
func encodeNLRI(p netip.Prefix) []byte {
	bits := p.Bits()
	addr := p.Addr().As4()
	n := (bits + 7) / 8
	return append([]byte{byte(bits)}, addr[:n]...)
}

func (s *Session) write(t byte, body []byte) error {
	msg := make([]byte, headerLen+len(body))
	msg[0] = 0xff
	for i := 1; i < 16; i++ {
		msg[i] = 0xff
	}
	binary.BigEndian.PutUint16(msg[16:18], uint16(len(msg)))
	msg[18] = t
	copy(msg[headerLen:], body)
	s.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_, err := s.conn.Write(msg)
	return err
}

func readFull(c net.Conn, buf []byte) (int, error) {
	read := 0
	for read < len(buf) {
		n, err := c.Read(buf[read:])
		if err != nil {
			return read, err
		}
		read += n
	}
	return read, nil
}
