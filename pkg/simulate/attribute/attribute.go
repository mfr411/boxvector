package attribute

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/netip"
)

// Attribute IDs of the standard BGP path attributes used by Boxvector.
const (
	Origin       = 1
	ASPath       = 2
	NextHop      = 3
	MED          = 4
	LocalPref    = 5
	Communities  = 8
	OriginatorID = 9
	MPNextHop    = 14
	MPReachNLRI  = 16
)

// Flag bits of a path attribute (RFC 4271 section 4.3).
const (
	FlagOptional   = 0x80
	FlagTransitive = 0x40
	FlagPartial    = 0x20
	FlagExtended   = 0x10
)

// Attribute is a single BGP path attribute.
type Attribute struct {
	Flags   uint8
	ID      uint8
	Payload []byte
}

// New builds an attribute with sane flags for the given ID.
func New(id uint8, payload []byte) Attribute {
	optional := id == MED || id == LocalPref || id == Communities || id == OriginatorID
	if id == MED {
		optional = true
	}
	flags := uint8(FlagTransitive)
	if optional {
		flags = FlagOptional
	}
	return Attribute{Flags: flags, ID: id, Payload: payload}
}

// SetFlags overrides the flags set by New.
func (a Attribute) SetFlags(f uint8) Attribute {
	a.Flags = f
	return a
}

// Bytes encodes the attribute as it appears on the wire.
func (a Attribute) Bytes() []byte {
	var hdr [3]byte
	hdr[0] = a.Flags
	hdr[1] = a.ID
	n := len(a.Payload)
	if n > 255 {
		hdr[0] |= FlagExtended
		out := append([]byte{hdr[0], hdr[1], 0, 0}, a.Payload...)
		binary.BigEndian.PutUint16(out[2:4], uint16(n))
		return out
	}
	hdr[2] = uint8(n)
	return append(hdr[:], a.Payload...)
}

// Origin builds the ORIGIN attribute for an origin code.
func OriginValue(code uint8) []byte {
	return []byte{code}
}

// ASPath builds an AS_PATH attribute in AS4 (BIRD 3) or AS2 (BIRD 2: peers
// never send AS4_PATH in practice for the simulator) encoding.
func ASPathValue(path []uint32, as4 bool) []byte {
	out := []byte{}
	if !as4 {
		out = append(out, 2, byte(len(path)))
		for _, asn := range path {
			var b [2]byte
			binary.BigEndian.PutUint16(b[:], uint16(asn))
			out = append(out, b[:]...)
		}
		return out
	}
	out = append(out, 2, byte(len(path)))
	for _, asn := range path {
		var b [4]byte
		binary.BigEndian.PutUint32(b[:], asn)
		out = append(out, b[:]...)
	}
	return out
}

// NextHopValue builds the NEXT_HOP attribute for an IPv4 address.
func NextHopValue(ip netip.Addr) []byte {
	b := ip.As4()
	return b[:]
}

// MEDValue builds the MULTI_EXIT_DISC attribute.
func MEDValue(v uint32) []byte {
	return binary.BigEndian.AppendUint32(nil, v)
}

// LocalPrefValue builds the LOCAL_PREF attribute.
func LocalPrefValue(v uint32) []byte {
	return binary.BigEndian.AppendUint32(nil, v)
}

// CommunitiesValue builds the COMMUNITIES attribute from a list of strings.
func CommunitiesValue(comms []string) ([]byte, error) {
	out := []byte{}
	for _, c := range comms {
		v, err := ParseStandardCommunity(c)
		if err != nil {
			return nil, err
		}
		out = binary.BigEndian.AppendUint32(out, v)
	}
	return out, nil
}

// LargeCommunitiesValue builds the LARGE_COMMUNITIES attribute.
func LargeCommunitiesValue(comms []string) ([]byte, error) {
	out := []byte{}
	for _, c := range comms {
		v, err := ParseLargeCommunity(c)
		if err != nil {
			return nil, err
		}
		out = binary.BigEndian.AppendUint32(out, v[0])
		out = binary.BigEndian.AppendUint32(out, v[1])
		out = binary.BigEndian.AppendUint32(out, v[2])
	}
	return out, nil
}

// asnPair converts a standard community "asn:value" (ASN <= 65535) into the
// 32 bit wire representation.
func asnPair(s string) (uint32, error) {
	var asn, val int
	if _, err := fmt.Sscanf(s, "%d:%d", &asn, &val); err != nil {
		return 0, fmt.Errorf("community %q: %w", s, err)
	}
	if asn < 0 || asn > 65535 || val < 0 || val > 65535 {
		return 0, fmt.Errorf("community %q: parts must be 0-65535", s)
	}
	return uint32(asn)<<16 | uint32(val), nil
}

// ParseStandardCommunity parses an RFC 1997 community (asn:value).
func ParseStandardCommunity(s string) (uint32, error) {
	v, err := asnPair(s)
	if err != nil {
		return 0, err
	}
	return v, nil
}

// ParseLargeCommunity parses an RFC 8092 large community (global:local1:local2).
func ParseLargeCommunity(s string) ([3]uint32, error) {
	var out [3]uint32
	var err error
	out[0], err = asnPair(s + ":0")
	if err != nil {
		return out, fmt.Errorf("large community %q: %w", s, err)
	}
	var global, local1 int
	if _, err = fmt.Sscanf(s, "%d:%d:%d", &global, &local1, &out[2]); err != nil {
		return out, fmt.Errorf("large community %q: %w", s, err)
	}
	out[0], out[1] = uint32(global), uint32(local1)
	return out, nil
}

// UnmarshalConfig decodes an attribute map from JSON (used by tests and the
// simulator) into wire attributes.
func UnmarshalConfig(data []byte) ([]Attribute, error) {
	type raw map[string]any
	var m raw
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	var out []Attribute
	for k, v := range m {
		switch k {
		case "origin":
			out = append(out, New(Origin, OriginValue(uint8(v.(float64)))))
		case "med":
			out = append(out, New(MED, MEDValue(uint32(v.(float64)))))
		case "local-pref":
			out = append(out, New(LocalPref, LocalPrefValue(uint32(v.(float64)))))
		default:
			return nil, fmt.Errorf("unknown attribute %q", k)
		}
	}
	return out, nil
}
