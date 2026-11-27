// Package fleet implements centralized, rolling OS/package patching of
// Debian/Ubuntu routers over SSH. One router at a time; stops on first failure;
// every patch records enough state to roll that router back.
package fleet

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// Host is one managed router.
type Host struct {
	Name    string `yaml:"name" json:"name"`
	Addr    string `yaml:"addr" json:"addr"`
	Port    int    `yaml:"port,omitempty" json:"port"`
	User    string `yaml:"user,omitempty" json:"user"`
	HostKey string `yaml:"host-key,omitempty" json:"host_key"` // SHA256 fingerprint, pinned on first connect
}

// Runner executes a shell script on a host (stdin of `sh -s`).
type Runner interface {
	Run(ctx context.Context, h Host, script string) (string, error)
}

// Pkg is an upgradable package.
type Pkg struct {
	Name     string `json:"name"`
	From     string `json:"from"`
	To       string `json:"to"`
	Security bool   `json:"security"`
}

// Status is the last known state of a host.
type Status struct {
	OS          string    `json:"os"`
	Kernel      string    `json:"kernel"`
	BIRD        string    `json:"bird"`
	Boxvector   string    `json:"boxvector"`
	BIRDActive  bool      `json:"bird_active"`
	BGPUp       int       `json:"bgp_up"`
	BGPTotal    int       `json:"bgp_total"`
	Upgradable  []Pkg     `json:"upgradable"`
	Reboot      bool      `json:"reboot"`
	CheckedAt   time.Time `json:"checked_at"`
	Err         string    `json:"err,omitempty"`
	ConfigValid bool      `json:"config_valid"`
}

func (s Status) Security() int {
	n := 0
	for _, p := range s.Upgradable {
		if p.Security {
			n++
		}
	}
	return n
}

// Mode selects what gets upgraded.
type Mode string

const (
	ModeSecurity Mode = "security"
	ModePackages Mode = "packages"
	ModeFull     Mode = "full"
)

// HostResult is the outcome of a job on one host.
type HostResult struct {
	Name     string            `json:"name"`
	State    string            `json:"state"` // pending running ok failed skipped rolledback
	Before   map[string]string `json:"before,omitempty"`
	After    map[string]string `json:"after,omitempty"`
	Repacked bool              `json:"repacked,omitempty"`
	Log      string            `json:"log,omitempty"`
}

// Job is one check/patch/rollback run.
type Job struct {
	ID      string       `json:"id"`
	Kind    string       `json:"kind"` // check patch rollback
	User    string       `json:"user"`
	Time    time.Time    `json:"time"`
	Mode    Mode         `json:"mode,omitempty"`
	DryRun  bool         `json:"dry_run,omitempty"`
	Pkgs    []string     `json:"pkgs,omitempty"`
	State   string       `json:"state"` // running ok failed
	Summary string       `json:"summary,omitempty"`
	Hosts   []HostResult `json:"hosts"`
}

var (
	reName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,40}$`)
	rePkg  = regexp.MustCompile(`^[a-z0-9][a-z0-9+.-]{0,60}$`)
	reVer  = regexp.MustCompile(`^[A-Za-z0-9.+:~_-]{1,80}$`)
	reUser = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
)

// Manager owns inventory, status cache and job history under dir.
type Manager struct {
	dir  string
	run  Runner
	mu   sync.Mutex // guards files + running
	busy bool
	// PollEvery / PostWait control the BGP convergence wait after a patch.
	PollEvery time.Duration
	PostWait  time.Duration
}

func New(dir string, r Runner) *Manager {
	return &Manager{dir: dir, run: r, PollEvery: 5 * time.Second, PostWait: 120 * time.Second}
}

// SetRunner sets the SSH runner after construction (it needs the manager for host-key pinning).
func (m *Manager) SetRunner(r Runner) { m.run = r }

func (m *Manager) path(f string) string { return filepath.Join(m.dir, f) }

// ---- inventory ----

func (m *Manager) Hosts() ([]Host, error) {
	b, err := os.ReadFile(m.path("fleet.yml"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var f struct {
		Hosts []Host `yaml:"hosts"`
	}
	if err := yaml.Unmarshal(b, &f); err != nil {
		return nil, err
	}
	for i := range f.Hosts {
		if f.Hosts[i].Port == 0 {
			f.Hosts[i].Port = 22
		}
		if f.Hosts[i].User == "" {
			f.Hosts[i].User = "bvpatch"
		}
	}
	return f.Hosts, nil
}

func (m *Manager) saveHosts(hs []Host) error {
	if err := os.MkdirAll(m.dir, 0o700); err != nil {
		return err
	}
	b, _ := yaml.Marshal(struct {
		Hosts []Host `yaml:"hosts"`
	}{hs})
	tmp := m.path("fleet.yml.tmp")
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, m.path("fleet.yml"))
}

func ValidateHost(h Host) error {
	if !reName.MatchString(h.Name) {
		return errors.New("name: letters, digits, . _ - (max 41)")
	}
	if ip := net.ParseIP(h.Addr); ip == nil && !regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`).MatchString(h.Addr) {
		return errors.New("addr: invalid IP or hostname")
	}
	if h.Port < 1 || h.Port > 65535 {
		return errors.New("port: 1-65535")
	}
	if !reUser.MatchString(h.User) {
		return errors.New("user: invalid")
	}
	return nil
}

func (m *Manager) AddHost(h Host) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if h.Port == 0 {
		h.Port = 22
	}
	if h.User == "" {
		h.User = "bvpatch"
	}
	if err := ValidateHost(h); err != nil {
		return err
	}
	hs, err := m.Hosts()
	if err != nil {
		return err
	}
	for _, x := range hs {
		if strings.EqualFold(x.Name, h.Name) {
			return errors.New("name already exists")
		}
	}
	return m.saveHosts(append(hs, h))
}

func (m *Manager) RemoveHost(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	hs, _ := m.Hosts()
	var out []Host
	for _, x := range hs {
		if x.Name != name {
			out = append(out, x)
		}
	}
	if len(out) == len(hs) {
		return errors.New("no such host")
	}
	return m.saveHosts(out)
}

// Pin stores the SSH host key fingerprint seen on first connect (TOFU).
func (m *Manager) Pin(name, fp string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	hs, _ := m.Hosts()
	for i := range hs {
		if hs[i].Name == name && hs[i].HostKey == "" {
			hs[i].HostKey = fp
		}
	}
	_ = m.saveHosts(hs)
}

// ---- status cache ----

func (m *Manager) Statuses() map[string]Status {
	out := map[string]Status{}
	if b, err := os.ReadFile(m.path("status.json")); err == nil {
		_ = json.Unmarshal(b, &out)
	}
	return out
}

func (m *Manager) setStatus(name string, s Status) {
	st := m.Statuses()
	st[name] = s
	b, _ := json.Marshal(st)
	_ = os.MkdirAll(m.dir, 0o700)
	_ = os.WriteFile(m.path("status.json"), b, 0o600)
}

// ---- remote scripts (only fixed text + validated tokens) ----

const pre = `S=""; [ "$(id -u)" = 0 ] || S="sudo -n"
export DEBIAN_FRONTEND=noninteractive LC_ALL=C
`

const bgpAwk = `awk '$2=="BGP"{t++; if($7=="Established")e++} END{printf "BGP=%d/%d\n",e+0,t+0}'`

func checkScript(refresh bool) string {
	s := pre + `. /etc/os-release; echo "OS=$PRETTY_NAME"
echo "KERNEL=$(uname -r)"
echo "LATESTK=$(ls /boot/vmlinuz-* 2>/dev/null | sed 's#.*/vmlinuz-##' | sort -V | tail -1)"
echo "BIRD=$(dpkg-query -W -f='${Version}' bird3 2>/dev/null || dpkg-query -W -f='${Version}' bird2 2>/dev/null)"
echo "BV=$(dpkg-query -W -f='${Version}' boxvector 2>/dev/null)"
echo "BIRDACTIVE=$(systemctl is-active bird 2>/dev/null)"
$S birdc show protocols 2>/dev/null | ` + bgpAwk + `
if $S bird -p -c /etc/bird/bird.conf >/dev/null 2>&1; then echo CONF=ok; else echo CONF=bad; fi
`
	if refresh {
		s += `$S apt-get update -qq >/dev/null 2>&1 || echo APTERR=1
`
	}
	s += `apt list --upgradable 2>/dev/null | grep 'upgradable from' | sed -E 's#^([^/]+)/([^ ]+) ([^ ]+) [^ ]+ \[upgradable from: ([^]]+)\]#PKG \1 \4 \3 \2#'
`
	return s
}

// ParseCheck turns check output into a Status.
func ParseCheck(out string) Status {
	st := Status{CheckedAt: time.Now()}
	latest := ""
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		switch {
		case strings.HasPrefix(l, "PKG "):
			f := strings.Fields(l)
			if len(f) >= 5 {
				st.Upgradable = append(st.Upgradable, Pkg{Name: f[1], From: f[2], To: f[3], Security: strings.Contains(f[4], "security")})
			}
		case strings.HasPrefix(l, "OS="):
			st.OS = l[3:]
		case strings.HasPrefix(l, "KERNEL="):
			st.Kernel = l[7:]
		case strings.HasPrefix(l, "LATESTK="):
			latest = l[8:]
		case strings.HasPrefix(l, "BIRD="):
			st.BIRD = l[5:]
		case strings.HasPrefix(l, "BV="):
			st.Boxvector = l[3:]
		case strings.HasPrefix(l, "BIRDACTIVE="):
			st.BIRDActive = l[11:] == "active"
		case strings.HasPrefix(l, "CONF="):
			st.ConfigValid = l[5:] == "ok"
		case strings.HasPrefix(l, "BGP="):
			p := strings.SplitN(l[4:], "/", 2)
			if len(p) == 2 {
				st.BGPUp, _ = strconv.Atoi(p[0])
				st.BGPTotal, _ = strconv.Atoi(p[1])
			}
		}
	}
	st.Reboot = latest != "" && st.Kernel != "" && latest != st.Kernel
	return st
}

// Check refreshes one host (apt update + list), updates the cache.
func (m *Manager) Check(ctx context.Context, h Host) Status {
	out, err := m.run.Run(ctx, h, checkScript(true))
	st := ParseCheck(out)
	if err != nil {
		st.Err = err.Error()
	}
	m.setStatus(h.Name, st)
	return st
}

// quick status without apt update (used for post-patch polling)
func (m *Manager) probe(ctx context.Context, h Host) Status {
	out, err := m.run.Run(ctx, h, checkScript(false))
	st := ParseCheck(out)
	if err != nil {
		st.Err = err.Error()
	}
	return st
}

func patchScript(jobID string, mode Mode, pkgs []string, dry bool) (string, error) {
	if !regexp.MustCompile(`^[0-9a-z-]{6,40}$`).MatchString(jobID) {
		return "", errors.New("bad job id")
	}
	for _, p := range pkgs {
		if !rePkg.MatchString(p) {
			return "", fmt.Errorf("invalid package name %q", p)
		}
	}
	sim := ""
	if dry {
		sim = "-s "
	}
	opts := `-y ` + sim + `-o Dpkg::Options::=--force-confold -o Dpkg::Options::=--force-confdef`
	var up string
	switch mode {
	case ModeFull:
		up = `$S apt-get ` + opts + ` dist-upgrade`
	case ModePackages:
		if len(pkgs) == 0 {
			return "", errors.New("no packages selected")
		}
		up = `$S apt-get ` + opts + ` install --only-upgrade ` + strings.Join(pkgs, " ")
	case ModeSecurity:
		if len(pkgs) == 0 {
			return "", errors.New("no security updates")
		}
		up = `$S apt-get ` + opts + ` install --only-upgrade ` + strings.Join(pkgs, " ")
	default:
		return "", errors.New("unknown mode")
	}
	s := pre
	if !dry {
		s += `BK=/var/lib/boxvector/patch-backup/` + jobID + `
$S mkdir -p "$BK" && $S tar czf "$BK/etc.tgz" /etc/bird /etc/boxvector 2>/dev/null
`
		// repack currently-installed critical packages so rollback does not depend on old versions staying in the repo
		s += `if ! command -v dpkg-repack >/dev/null 2>&1; then $S apt-get install -y -qq dpkg-repack >/dev/null 2>&1; fi
if command -v dpkg-repack >/dev/null 2>&1; then
  for p in $(dpkg-query -W -f='${Package}\n' 'bird*' boxvector 2>/dev/null); do
    case " ` + strings.Join(pkgs, " ") + ` " in *" $p "*) (cd "$BK" && $S dpkg-repack "$p" >/dev/null 2>&1 && echo "REPACK=$p");; esac
  done
fi
`
	}
	s += up + ` 2>&1 | tail -n 40
echo "RC=${PIPESTATUS[0]:-0}"
`
	// need bash for PIPESTATUS
	return "#!/bin/bash\n" + s, nil
}

// VersionsOf returns installed versions for pkgs.
func (m *Manager) versionsOf(ctx context.Context, h Host, pkgs []string) map[string]string {
	if len(pkgs) == 0 {
		return nil
	}
	out, _ := m.run.Run(ctx, h, `dpkg-query -W -f='VER ${Package} ${Version}\n' `+strings.Join(pkgs, " ")+` 2>/dev/null`)
	v := map[string]string{}
	for _, l := range strings.Split(out, "\n") {
		f := strings.Fields(l)
		if len(f) == 3 && f[0] == "VER" {
			v[f[1]] = f[2]
		}
	}
	return v
}

// ---- jobs ----

func (m *Manager) jobPath(id string) string { return m.path("job-" + id + ".json") }

func (m *Manager) saveJob(j *Job) {
	_ = os.MkdirAll(m.dir, 0o700)
	b, _ := json.MarshalIndent(j, "", " ")
	_ = os.WriteFile(m.jobPath(j.ID), b, 0o600)
}

func (m *Manager) Job(id string) (*Job, error) {
	if !regexp.MustCompile(`^[0-9a-z-]{6,40}$`).MatchString(id) {
		return nil, errors.New("bad id")
	}
	b, err := os.ReadFile(m.jobPath(id))
	if err != nil {
		return nil, err
	}
	var j Job
	return &j, json.Unmarshal(b, &j)
}

// Jobs lists the newest n jobs.
func (m *Manager) Jobs(n int) []Job {
	ents, _ := os.ReadDir(m.dir)
	var names []string
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), "job-") && strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	var out []Job
	for _, nm := range names {
		if len(out) >= n {
			break
		}
		if j, err := m.Job(strings.TrimSuffix(strings.TrimPrefix(nm, "job-"), ".json")); err == nil {
			out = append(out, *j)
		}
	}
	return out
}

func (m *Manager) Busy() bool { m.mu.Lock(); defer m.mu.Unlock(); return m.busy }

func newID() string {
	return time.Now().Format("20060102-150405") + "-" + strconv.Itoa(time.Now().Nanosecond()/1e6)
}

// Start begins a job asynchronously. Only one job runs at a time.
func (m *Manager) Start(user, kind string, names []string, mode Mode, pkgs []string, dry bool, fromJob string) (string, error) {
	m.mu.Lock()
	if m.busy {
		m.mu.Unlock()
		return "", errors.New("another job is running")
	}
	hs, err := m.Hosts()
	if err != nil {
		m.mu.Unlock()
		return "", err
	}
	byName := map[string]Host{}
	for _, h := range hs {
		byName[h.Name] = h
	}
	var sel []Host
	seen := map[string]bool{}
	for _, n := range names {
		h, ok := byName[n]
		if !ok || seen[n] {
			continue
		}
		seen[n] = true
		sel = append(sel, h)
	}
	if len(sel) == 0 {
		m.mu.Unlock()
		return "", errors.New("no hosts selected")
	}
	if kind == "patch" && mode == ModePackages {
		for _, p := range pkgs {
			if !rePkg.MatchString(p) {
				m.mu.Unlock()
				return "", fmt.Errorf("invalid package name %q", p)
			}
		}
	}
	j := &Job{ID: newID(), Kind: kind, User: user, Time: time.Now(), Mode: mode, DryRun: dry, Pkgs: pkgs, State: "running"}
	for _, h := range sel {
		j.Hosts = append(j.Hosts, HostResult{Name: h.Name, State: "pending"})
	}
	var src *Job
	if kind == "rollback" {
		if src, err = m.Job(fromJob); err != nil {
			m.mu.Unlock()
			return "", errors.New("source job not found")
		}
	}
	m.busy = true
	m.saveJob(j)
	m.mu.Unlock()
	go func() {
		defer func() { m.mu.Lock(); m.busy = false; m.mu.Unlock() }()
		m.exec(context.Background(), j, sel, src)
	}()
	return j.ID, nil
}

func (m *Manager) exec(ctx context.Context, j *Job, sel []Host, src *Job) {
	failed := false
	for i, h := range sel {
		hr := &j.Hosts[i]
		if failed {
			hr.State = "skipped"
			hr.Log = "skipped: previous router failed"
			m.saveJob(j)
			continue
		}
		hr.State = "running"
		m.saveJob(j)
		cctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
		switch j.Kind {
		case "check":
			st := m.Check(cctx, h)
			if st.Err != "" {
				hr.State, hr.Log, failed = "failed", st.Err, false // a failed check does not halt other checks
			} else {
				hr.State = "ok"
				hr.Log = fmt.Sprintf("%d upgradable (%d security), BGP %d/%d", len(st.Upgradable), st.Security(), st.BGPUp, st.BGPTotal)
			}
		case "patch":
			failed = !m.patchOne(cctx, j, h, hr)
		case "rollback":
			failed = !m.rollbackOne(cctx, h, hr, src)
		}
		cancel()
		m.saveJob(j)
	}
	ok, bad := 0, 0
	for _, h := range j.Hosts {
		switch h.State {
		case "ok", "rolledback":
			ok++
		case "failed":
			bad++
		}
	}
	j.State = "ok"
	if failed {
		j.State = "failed"
	}
	j.Summary = fmt.Sprintf("%d ok, %d failed, %d skipped", ok, bad, len(j.Hosts)-ok-bad)
	m.saveJob(j)
}

// patchOne returns false when the rolling run must stop.
func (m *Manager) patchOne(ctx context.Context, j *Job, h Host, hr *HostResult) bool {
	fail := func(msg string) bool { hr.State, hr.Log = "failed", strings.TrimSpace(hr.Log+"\n"+msg); return false }
	pre := m.Check(ctx, h)
	if pre.Err != "" {
		return fail("pre-check: " + pre.Err)
	}
	if !pre.BIRDActive {
		return fail("pre-check: BIRD is not active, refusing to patch")
	}
	var pkgs []string
	switch j.Mode {
	case ModeSecurity:
		for _, p := range pre.Upgradable {
			if p.Security {
				pkgs = append(pkgs, p.Name)
			}
		}
	case ModePackages:
		have := map[string]bool{}
		for _, p := range pre.Upgradable {
			have[p.Name] = true
		}
		for _, p := range j.Pkgs {
			if have[p] {
				pkgs = append(pkgs, p)
			}
		}
		if len(pkgs) == 0 {
			hr.State, hr.Log = "ok", "nothing to upgrade for the selected packages"
			return true
		}
	case ModeFull:
		for _, p := range pre.Upgradable {
			pkgs = append(pkgs, p.Name)
		}
	}
	if len(pkgs) == 0 {
		hr.State, hr.Log = "ok", "already up to date"
		return true
	}
	hr.Before = m.versionsOf(ctx, h, pkgs)
	script, err := patchScript(j.ID, j.Mode, pkgs, j.DryRun)
	if err != nil {
		return fail(err.Error())
	}
	out, err := m.run.Run(ctx, h, script)
	hr.Log = tail(out, 3000)
	hr.Repacked = strings.Contains(out, "REPACK=")
	if err != nil {
		return fail("apt: " + err.Error())
	}
	if !strings.Contains(out, "RC=0") {
		return fail("apt returned non-zero")
	}
	if j.DryRun {
		hr.State = "ok"
		hr.Log = "dry-run (apt -s), nothing changed.\n" + hr.Log
		return true
	}
	hr.After = m.versionsOf(ctx, h, pkgs)
	// post-check: BIRD active, config valid, no fewer established sessions than before
	deadline := time.Now().Add(m.PostWait)
	var post Status
	for {
		post = m.probe(ctx, h)
		if post.Err == "" && post.BIRDActive && post.ConfigValid && post.BGPUp >= pre.BGPUp {
			break
		}
		if time.Now().After(deadline) {
			return fail(fmt.Sprintf("post-check failed: bird_active=%v config_valid=%v BGP %d/%d (before %d/%d). Rolling run stopped; use Rollback for this router.",
				post.BIRDActive, post.ConfigValid, post.BGPUp, post.BGPTotal, pre.BGPUp, pre.BGPTotal))
		}
		time.Sleep(m.PollEvery)
	}
	hr.State = "ok"
	hr.Log += fmt.Sprintf("\npost-check ok: BGP %d/%d, BIRD active, config valid", post.BGPUp, post.BGPTotal)
	m.Check(ctx, h)
	return true
}

func (m *Manager) rollbackOne(ctx context.Context, h Host, hr *HostResult, src *Job) bool {
	var sr *HostResult
	for i := range src.Hosts {
		if src.Hosts[i].Name == h.Name {
			sr = &src.Hosts[i]
		}
	}
	if sr == nil || len(sr.Before) == 0 || src.DryRun {
		hr.State, hr.Log = "failed", "no rollback data for this host in the source job"
		return false
	}
	s := pre + `BK=/var/lib/boxvector/patch-backup/` + src.ID + "\n"
	var names []string
	for p := range sr.Before {
		names = append(names, p)
	}
	sort.Strings(names)
	for _, p := range names {
		v := sr.Before[p]
		if !rePkg.MatchString(p) || !reVer.MatchString(v) {
			hr.State, hr.Log = "failed", "invalid package/version in job data"
			return false
		}
		// prefer the repacked .deb; fall back to the exact old version from apt
		s += `if ls "$BK"/` + p + `_*.deb >/dev/null 2>&1; then $S dpkg -i --force-confold "$BK"/` + p + `_*.deb; else $S apt-get install -y --allow-downgrades -o Dpkg::Options::=--force-confold ` + p + `=` + v + `; fi
`
	}
	s += `$S systemctl restart bird 2>&1; echo RC=$?
`
	out, err := m.run.Run(ctx, h, s)
	hr.Log = tail(out, 3000)
	if err != nil || !strings.Contains(out, "RC=0") {
		hr.State = "failed"
		return false
	}
	hr.After = m.versionsOf(ctx, h, names)
	for _, p := range names {
		if hr.After[p] != sr.Before[p] {
			hr.State = "failed"
			hr.Log += fmt.Sprintf("\n%s is %s, expected %s", p, hr.After[p], sr.Before[p])
			return false
		}
	}
	deadline := time.Now().Add(m.PostWait)
	for {
		post := m.probe(ctx, h)
		if post.Err == "" && post.BIRDActive && post.ConfigValid {
			hr.State = "rolledback"
			hr.Log += fmt.Sprintf("\nrolled back; BIRD active, BGP %d/%d", post.BGPUp, post.BGPTotal)
			m.Check(ctx, h)
			return true
		}
		if time.Now().After(deadline) {
			hr.State = "failed"
			hr.Log += "\nBIRD did not come back healthy after rollback"
			return false
		}
		time.Sleep(m.PollEvery)
	}
}

// Reboot reboots one host (explicit action, not part of patching).
func (m *Manager) Reboot(ctx context.Context, name string) error {
	hs, _ := m.Hosts()
	for _, h := range hs {
		if h.Name == name {
			_, err := m.run.Run(ctx, h, pre+"$S systemctl reboot --no-block\n")
			return err
		}
	}
	return errors.New("no such host")
}

func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return "…" + s[len(s)-n:]
	}
	return s
}
