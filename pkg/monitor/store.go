// Package monitor implements the central boxvector fleet monitor: devices send heartbeats,
// an admin approves releases per device, and agents pull and apply them with a health check.
package monitor

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

const (
	HeartbeatInterval = 60 // seconds, handed to agents
	OfflineAfter      = 3 * HeartbeatInterval * time.Second
	patchTimeout      = 20 * time.Minute
)

type Info struct {
	Hostname    string   `json:"hostname"`
	Version     string   `json:"version"`
	Arch        string   `json:"arch"`
	Bird        string   `json:"bird"`
	OS          string   `json:"os"`
	Kernel      string   `json:"kernel"`
	RouterID    string   `json:"router_id"`
	ASN         int      `json:"asn"`
	Sessions    int      `json:"sessions"`
	Established int      `json:"established"`
	Uptime      int64    `json:"uptime"`
	Load        string   `json:"load"`
	IPs         []string `json:"ips"`
}

type Patch struct {
	Version string    `json:"version"`
	Status  string    `json:"status"` // queued | running | ok | failed | rolledback
	Msg     string    `json:"msg"`
	At      time.Time `json:"at"`
}

type Device struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	TokenHash string    `json:"token_hash"`
	Created   time.Time `json:"created"`
	LastSeen  time.Time `json:"last_seen"`
	RemoteIP  string    `json:"remote_ip"`
	Info      Info      `json:"info"`
	Target    string    `json:"target"`
	Patch     Patch     `json:"patch"`
	Note      string    `json:"note"`
}

type Release struct {
	Version  string    `json:"version"`
	Arch     string    `json:"arch"`
	File     string    `json:"file"`
	SHA256   string    `json:"sha256"`
	Size     int64     `json:"size"`
	Sig      string    `json:"sig,omitempty"`
	Uploaded time.Time `json:"uploaded"`
}

type EnrollToken struct {
	ID      string    `json:"id"`
	Hash    string    `json:"hash"`
	Label   string    `json:"label"`
	Created time.Time `json:"created"`
	Revoked bool      `json:"revoked"`
	Uses    int       `json:"uses"`
}

type Event struct {
	At  time.Time `json:"at"`
	Dev string    `json:"dev"`
	Msg string    `json:"msg"`
}

type state struct {
	Devices  map[string]*Device `json:"devices"`
	Releases []Release          `json:"releases"`
	Tokens   []EnrollToken      `json:"tokens"`
	Events   []Event            `json:"events"`
}

type Store struct {
	mu   sync.Mutex
	path string
	s    state
	now  func() time.Time
}

func OpenStore(dir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, "releases"), 0o750); err != nil {
		return nil, err
	}
	st := &Store{path: filepath.Join(dir, "state.json"), now: time.Now}
	st.s.Devices = map[string]*Device{}
	b, err := os.ReadFile(st.path)
	if err == nil {
		if err := json.Unmarshal(b, &st.s); err != nil {
			return nil, errors.New("state.json is corrupt: " + err.Error())
		}
		if st.s.Devices == nil {
			st.s.Devices = map[string]*Device{}
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	return st, nil
}

func (st *Store) save() error {
	b, err := json.MarshalIndent(&st.s, "", " ")
	if err != nil {
		return err
	}
	tmp := st.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, st.path)
}

func (st *Store) event(dev, msg string) {
	st.s.Events = append(st.s.Events, Event{At: st.now(), Dev: dev, Msg: msg})
	if n := len(st.s.Events); n > 500 {
		st.s.Events = append([]Event(nil), st.s.Events[n-500:]...)
	}
}

func NewToken() (plain, hash string) {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	plain = hex.EncodeToString(b)
	return plain, HashToken(plain)
}

func HashToken(t string) string {
	h := sha256.Sum256([]byte(t))
	return hex.EncodeToString(h[:])
}

func eq(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }

// ---- enrollment ----

func (st *Store) AddEnrollToken(label string) (string, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	plain, hash := NewToken()
	id := plain[:8]
	st.s.Tokens = append(st.s.Tokens, EnrollToken{ID: id, Hash: hash, Label: label, Created: st.now()})
	st.event("", "enroll token created: "+label)
	return plain, st.save()
}

func (st *Store) RevokeEnrollToken(id string) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	for i := range st.s.Tokens {
		if st.s.Tokens[i].ID == id {
			st.s.Tokens[i].Revoked = true
			st.event("", "enroll token revoked: "+st.s.Tokens[i].Label)
			return st.save()
		}
	}
	return errors.New("unknown token")
}

// Enroll validates an enrollment token and creates a device, returning its id and secret.
func (st *Store) Enroll(enrollToken, hostname, ip string) (id, secret string, err error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	h := HashToken(enrollToken)
	ok := false
	for i := range st.s.Tokens {
		if eq(st.s.Tokens[i].Hash, h) && !st.s.Tokens[i].Revoked {
			st.s.Tokens[i].Uses++
			ok = true
		}
	}
	if !ok {
		return "", "", errors.New("invalid enroll token")
	}
	plain, hash := NewToken()
	idb := make([]byte, 6)
	_, _ = rand.Read(idb)
	id = hex.EncodeToString(idb)
	st.s.Devices[id] = &Device{ID: id, Name: hostname, TokenHash: hash, Created: st.now(), RemoteIP: ip, Info: Info{Hostname: hostname}}
	st.event(id, "enrolled: "+hostname)
	return id, plain, st.save()
}

// ---- heartbeat / patching ----

func (st *Store) auth(secret string) *Device {
	h := HashToken(secret)
	for _, d := range st.s.Devices {
		if eq(d.TokenHash, h) {
			return d
		}
	}
	return nil
}

func (st *Store) Authenticate(secret string) (string, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if d := st.auth(secret); d != nil {
		return d.ID, true
	}
	return "", false
}

type Order struct {
	Version string `json:"version"`
	File    string `json:"file"`
	SHA256  string `json:"sha256"`
	Size    int64  `json:"size"`
	Sig     string `json:"sig"`
}

func (st *Store) release(ver, arch string) *Release {
	for i := range st.s.Releases {
		r := &st.s.Releases[i]
		if r.Version == ver && r.Arch == arch {
			return r
		}
	}
	return nil
}

// Heartbeat records a device report and returns a patch order when one is due.
// Rolling rule: only one device may be patching at a time.
func (st *Store) Heartbeat(secret, ip string, in Info) (*Order, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	d := st.auth(secret)
	if d == nil {
		return nil, errors.New("unauthorized")
	}
	now := st.now()
	d.LastSeen, d.RemoteIP, d.Info = now, ip, in
	if in.Hostname != "" {
		d.Name = in.Hostname
	}
	if d.Patch.Status == "running" {
		if in.Version == d.Patch.Version {
			d.Patch.Status, d.Patch.Msg, d.Patch.At = "ok", "running "+in.Version, now
			st.event(d.ID, d.Name+": patched to "+in.Version)
		} else if now.Sub(d.Patch.At) > patchTimeout {
			d.Patch.Status, d.Patch.Msg, d.Patch.At = "failed", "no result within 20 minutes", now
			st.event(d.ID, d.Name+": patch timed out")
		}
	}
	var order *Order
	if d.Patch.Status == "queued" && d.Target != "" && d.Target != in.Version {
		busy := false
		for _, o := range st.s.Devices {
			if o.ID != d.ID && o.Patch.Status == "running" && now.Sub(o.Patch.At) <= patchTimeout {
				busy = true
			}
		}
		if r := st.release(d.Target, in.Arch); r != nil && !busy {
			order = &Order{Version: r.Version, File: r.File, SHA256: r.SHA256, Size: r.Size, Sig: r.Sig}
			d.Patch.Status, d.Patch.At, d.Patch.Msg = "running", now, "handed out"
			st.event(d.ID, d.Name+": patch to "+r.Version+" started")
		}
	}
	return order, st.save()
}

func (st *Store) PatchResult(secret, version, status, msg string) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	d := st.auth(secret)
	if d == nil {
		return errors.New("unauthorized")
	}
	if status != "ok" && status != "failed" && status != "rolledback" {
		return errors.New("bad status")
	}
	if len(msg) > 300 {
		msg = msg[:300]
	}
	d.Patch = Patch{Version: version, Status: status, Msg: msg, At: st.now()}
	st.event(d.ID, d.Name+": patch "+version+" "+status+" "+msg)
	return st.save()
}

// ---- admin actions ----

func (st *Store) Approve(ids []string, version string) (int, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	n := 0
	for _, id := range ids {
		d := st.s.Devices[id]
		if d == nil || st.release(version, d.Info.Arch) == nil || d.Info.Version == version {
			continue
		}
		if d.Patch.Status == "running" {
			continue
		}
		d.Target = version
		d.Patch = Patch{Version: version, Status: "queued", At: st.now()}
		st.event(id, d.Name+": patch to "+version+" approved")
		n++
	}
	return n, st.save()
}

func (st *Store) Cancel(ids []string) (int, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	n := 0
	for _, id := range ids {
		if d := st.s.Devices[id]; d != nil && d.Patch.Status == "queued" {
			d.Target, d.Patch = "", Patch{}
			st.event(id, d.Name+": queued patch cancelled")
			n++
		}
	}
	return n, st.save()
}

func (st *Store) DeleteDevice(id string) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	d := st.s.Devices[id]
	if d == nil {
		return errors.New("unknown device")
	}
	delete(st.s.Devices, id)
	st.event(id, "device removed: "+d.Name)
	return st.save()
}

func (st *Store) SetNote(id, note string) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	if d := st.s.Devices[id]; d != nil {
		d.Note = note
		return st.save()
	}
	return errors.New("unknown device")
}

func (st *Store) AddRelease(r Release) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.release(r.Version, r.Arch) != nil {
		return errors.New("release " + r.Version + " (" + r.Arch + ") already exists")
	}
	r.Uploaded = st.now()
	st.s.Releases = append(st.s.Releases, r)
	st.event("", "release uploaded: "+r.Version+" "+r.Arch)
	return st.save()
}

func (st *Store) DeleteRelease(version, arch string) (string, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	for i, r := range st.s.Releases {
		if r.Version == version && r.Arch == arch {
			for _, d := range st.s.Devices {
				if d.Target == version && (d.Patch.Status == "queued" || d.Patch.Status == "running") {
					return "", errors.New("release is queued or running on " + d.Name)
				}
			}
			st.s.Releases = append(st.s.Releases[:i], st.s.Releases[i+1:]...)
			st.event("", "release deleted: "+version)
			return r.File, st.save()
		}
	}
	return "", errors.New("unknown release")
}

func (st *Store) HasFile(file string) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	for _, r := range st.s.Releases {
		if r.File == file {
			return true
		}
	}
	return false
}

// ---- views ----

func (st *Store) Devices() []Device {
	st.mu.Lock()
	defer st.mu.Unlock()
	out := make([]Device, 0, len(st.s.Devices))
	for _, d := range st.s.Devices {
		out = append(out, *d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (st *Store) Device(id string) (Device, []Event, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	d := st.s.Devices[id]
	if d == nil {
		return Device{}, nil, false
	}
	var ev []Event
	for i := len(st.s.Events) - 1; i >= 0 && len(ev) < 30; i-- {
		if st.s.Events[i].Dev == id {
			ev = append(ev, st.s.Events[i])
		}
	}
	return *d, ev, true
}

func (st *Store) Releases() []Release {
	st.mu.Lock()
	defer st.mu.Unlock()
	out := append([]Release(nil), st.s.Releases...)
	sort.Slice(out, func(i, j int) bool { return out[i].Uploaded.After(out[j].Uploaded) })
	return out
}

func (st *Store) Tokens() []EnrollToken {
	st.mu.Lock()
	defer st.mu.Unlock()
	return append([]EnrollToken(nil), st.s.Tokens...)
}

func (st *Store) Events(n int) []Event {
	st.mu.Lock()
	defer st.mu.Unlock()
	var ev []Event
	for i := len(st.s.Events) - 1; i >= 0 && len(ev) < n; i-- {
		ev = append(ev, st.s.Events[i])
	}
	return ev
}

func (d Device) Online(now time.Time) bool {
	return !d.LastSeen.IsZero() && now.Sub(d.LastSeen) <= OfflineAfter
}
