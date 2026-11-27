// Package simulate runs a set of BGP sessions against a live BIRD instance and
// reports what BIRD's filters did with each announced prefix.
//
// It exists to prove that generated policies behave as configured: the BIRD
// parse check catches syntax errors only, not policy logic. The package has no
// dependencies on the daemon (BIRD only, no netns, no root) so it runs in CI.
package simulate

import (
	"fmt"
	"net/netip"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/mfr411/boxvector/pkg/bird"
	"github.com/mfr411/boxvector/pkg/simulate/attribute"
	"github.com/mfr411/boxvector/pkg/simulate/bgp"
)

// Expect is the expected outcome for one prefix.
type Expect int

// Expected outcomes.
const (
	// Accepted means the prefix must appear in the master table.
	Accepted Expect = iota
	// Filtered means BIRD must keep the route but in a filtered state.
	Filtered
	// Rejected means BIRD must not keep the route at all.
	Rejected
	// Missing means the prefix is not checked (used for routes BIRD never saw).
	Missing
)

// Route is one prefix to announce with its attributes.
type Route struct {
	Prefix    netip.Prefix
	Path      []uint32
	Attrs     []attribute.Attribute
	Expect    Expect
	ExpectBy  string
	LocalPref uint32
}

// Session is one neighbor with its routes.
type Session struct {
	Name     string
	Neighbor string
	LocalAS  uint32
	PeerAS   uint32
	Routes   []Route

	conn *bgp.Session
}

// Options configure a Run.
type Options struct {
	// BirdBinary and BirdSocket locate the running instance to inspect.
	BirdBinary string
	BirdSocket string
	// Wait is how long to wait for BIRD to settle before reading tables.
	Wait time.Duration
	// Reporter receives one line per failure.
	Reporter func(string)
	// Table lists routes through the BIRD socket.
	Table func() (string, error)
}

// Result summarises a run.
type Result struct {
	Checked  int
	Failures int
	Passed   bool
}

// RouteState holds what BIRD currently has for a prefix.
type RouteState string

const (
	stateAccepted RouteState = "accepted"
	stateFiltered RouteState = "filtered"
	stateMissing  RouteState = "missing"
)

// Run dials every session, announces its routes, evaluates the outcome and
// closes the sessions again.
func Run(opts Options, sessions []Session) (*Result, error) {
	if opts.Wait == 0 {
		opts.Wait = 20 * time.Second
	}
	if opts.Reporter == nil {
		opts.Reporter = func(string) {}
	}
	res := &Result{}

	for i := range sessions {
		s := &sessions[i]
		if len(s.Routes) == 0 {
			continue
		}
		conn, err := bgp.Dial(s.Neighbor, s.LocalAS, s.PeerAS, netip.MustParseAddr("127.0.0.1"), 30*time.Second)
		if err != nil {
			return res, fmt.Errorf("%s: dial %s: %w", s.Name, s.Neighbor, err)
		}
		s.conn = conn
		for _, r := range s.Routes {
			path := r.Path
			if len(path) == 0 {
				path = []uint32{s.PeerAS, 64999}
			}
			if err := conn.Announce(r.Prefix, path, r.Attrs); err != nil {
				s.conn.Close()
				return res, fmt.Errorf("%s: announce %s: %w", s.Name, r.Prefix, err)
			}
		}
	}
	defer func() {
		for i := range sessions {
			if sessions[i].conn != nil {
				sessions[i].conn.Close()
			}
		}
	}()

	time.Sleep(opts.Wait)
	states, err := readStates(opts)
	if err != nil {
		return res, err
	}
	reportResult(opts, sessions, states, res)
	return res, nil
}

// readStates maps every expected prefix to what BIRD actually has.
func readStates(opts Options) (map[string]RouteState, error) {
	table, err := opts.Table()
	if err != nil {
		return nil, err
	}
	states := map[string]RouteState{}
	set := func(key string, st RouteState) {
		if cur, ok := states[key]; !ok || st < cur {
			states[key] = st
		}
	}
	for _, line := range strings.Split(table, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		key := fields[0]
		if _, err := netip.ParsePrefix(key); err != nil {
			continue
		}
		set(key, stateAccepted)
		if strings.Contains(strings.ToLower(line), "filtered") {
			set(key, stateFiltered)
		}
	}
	filtered, err := filteredRoutes(opts)
	if err == nil {
		for _, line := range strings.Split(filtered, "\n") {
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}
			if _, err := netip.ParsePrefix(fields[0]); err == nil {
				set(fields[0], stateFiltered)
			}
		}
	}
	return states, nil
}

// filteredRoutes asks BIRD for every protocol's filtered routes.
func filteredRoutes(opts Options) (string, error) {
	out, _, err := bird.RunCommand("show route filtered", opts.BirdSocket)
	if err != nil {
		return "", err
	}
	return out, nil
}

func reportResult(opts Options, sessions []Session, states map[string]RouteState, res *Result) {
	for _, s := range sessions {
		for _, r := range s.Routes {
			if r.Expect == Missing {
				continue
			}
			res.Checked++
			got := states[r.Prefix.String()]
			if got == "" {
				got = stateMissing
			}
			want := map[Expect]RouteState{Accepted: stateAccepted, Filtered: stateFiltered, Rejected: stateMissing}[r.Expect]
			if got != want {
				res.Failures++
				opts.Reporter(fmt.Sprintf("%s %s: want %s, got %s (%s)", s.Name, r.Prefix, want, got, r.ExpectBy))
			}
		}
	}
	res.Passed = res.Failures == 0
}

// String renders an outcome list for the CLI.
func (r *Result) String() string {
	return fmt.Sprintf("%d route(s) checked, %d failure(s)", r.Checked, r.Failures)
}

// ReadFile reads a route list file: "<action> <prefix> [attributes]" per line,
// where action is accept, reject or filtered, and attributes are
// space-separated key=value pairs (med=100, local-pref=200) or comm=<community>.
func ReadFile(path string, peerAS uint32) ([]Route, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []Route
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return nil, fmt.Errorf("%s:%d: want \"<action> <prefix>\"", path, i+1)
		}
		prefix, err := netip.ParsePrefix(fields[1])
		if err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, i+1, err)
		}
		want := map[string]Expect{"accept": Accepted, "reject": Rejected, "filtered": Filtered}[strings.ToLower(fields[0])]
		r := Route{Prefix: prefix, Expect: want, ExpectBy: fields[0]}
		for _, opt := range fields[2:] {
			k, v, _ := strings.Cut(opt, "=")
			switch k {
			case "aspath":
				for _, a := range strings.Split(v, ",") {
					var asn uint32
					fmt.Sscanf(a, "%d", &asn)
					r.Path = append(r.Path, asn)
				}
			case "comm":
				payload, err := attribute.CommunitiesValue(strings.Split(v, ","))
				if err != nil {
					return nil, fmt.Errorf("%s:%d: %w", path, i+1, err)
				}
				r.Attrs = append(r.Attrs, attribute.New(attribute.Communities, payload))
			}
		}
		out = append(out, r)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Expect < out[j].Expect })
	return out, nil
}
