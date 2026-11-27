package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// store keeps config snapshots and the apply history under stateDir.
type store struct {
	configFile string
	stateDir   string
	binary     string // boxvector executable used for validate/apply
	mu         sync.Mutex
}

// Event is one line of history.jsonl.
type Event struct {
	Time     time.Time `json:"time"`
	User     string    `json:"user"`
	Action   string    `json:"action"` // validate, apply, rollback
	OK       bool      `json:"ok"`
	Snapshot string    `json:"snapshot,omitempty"`
	Summary  string    `json:"summary"`
	Output   string    `json:"output,omitempty"`
}

var reANSI = regexp.MustCompile(`\x1b\[[0-9;]*m`)

const keepSnapshots = 50

func (s *store) snapDir() string { return filepath.Join(s.stateDir, "snapshots") }

func (s *store) current() (string, error) {
	b, err := os.ReadFile(s.configFile)
	return string(b), err
}

// run executes "boxvector generate" on a config file and returns its output.
func (s *store) run(cfg string, dryRun bool) (string, bool) {
	args := []string{"generate", "-c", cfg}
	if dryRun {
		args = append(args, "--dry-run")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, s.binary, args...)
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	txt := reANSI.ReplaceAllString(out.String(), "")
	var keep []string
	for _, l := range strings.Split(txt, "\n") {
		if strings.HasPrefix(l, "****") || strings.Contains(l, "development build") || strings.Contains(l, "submit any bugs") {
			continue
		}
		keep = append(keep, l)
	}
	txt = strings.TrimSpace(strings.Join(keep, "\n"))
	if len(txt) > 64<<10 {
		txt = txt[len(txt)-64<<10:]
	}
	return txt, err == nil
}

// Validate writes the candidate to a private temp file and dry-runs it.
func (s *store) Validate(candidate string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := filepath.Join(s.stateDir, "candidate.yml")
	if err := os.WriteFile(f, []byte(candidate), 0o600); err != nil {
		return err.Error(), false
	}
	defer os.Remove(f)
	return s.run(f, true)
}

// Apply snapshots the running config, installs the candidate and generates.
// On failure the previous config is restored so the file never stays broken.
func (s *store) Apply(user, candidate, action string) Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	ev := Event{Time: time.Now(), User: user, Action: action}
	old, err := s.current()
	if err != nil {
		ev.Summary = "read config: " + err.Error()
		s.log(ev)
		return ev
	}
	snap, err := s.snapshot(old)
	if err != nil {
		ev.Summary = "snapshot: " + err.Error()
		s.log(ev)
		return ev
	}
	ev.Snapshot = snap
	if err := writeAtomic(s.configFile, candidate); err != nil {
		ev.Summary = "write config: " + err.Error()
		s.log(ev)
		return ev
	}
	out, ok := s.run(s.configFile, false)
	ev.Output, ev.OK = out, ok
	if ok {
		ev.Summary = summarize(Diff(old, candidate, 0))
	} else {
		_ = writeAtomic(s.configFile, old)
		ev.Summary = "generate failed, previous config restored"
	}
	s.log(ev)
	return ev
}

func summarize(d []DiffLine) string {
	add, del := 0, 0
	for _, l := range d {
		switch l.Op {
		case '+':
			add++
		case '-':
			del++
		}
	}
	if add+del == 0 {
		return "no changes"
	}
	return fmt.Sprintf("+%d / -%d lines", add, del)
}

func writeAtomic(path, content string) error {
	mode := os.FileMode(0o640)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	tmp := path + ".bvtmp"
	if err := os.WriteFile(tmp, []byte(content), mode); err != nil {
		// the service may only be allowed to write the file itself (e.g. /etc/boxvector.yml),
		// not to create new files next to it: write in place instead
		return os.WriteFile(path, []byte(content), mode)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return os.WriteFile(path, []byte(content), mode)
	}
	return nil
}

func (s *store) snapshot(content string) (string, error) {
	if err := os.MkdirAll(s.snapDir(), 0o700); err != nil {
		return "", err
	}
	name := time.Now().Format("20060102-150405.000") + ".yml"
	if err := os.WriteFile(filepath.Join(s.snapDir(), name), []byte(content), 0o600); err != nil {
		return "", err
	}
	snaps, _ := s.Snapshots()
	for i := keepSnapshots; i < len(snaps); i++ {
		_ = os.Remove(filepath.Join(s.snapDir(), snaps[i]))
	}
	return name, nil
}

// Snapshots lists snapshot names, newest first.
func (s *store) Snapshots() ([]string, error) {
	ents, err := os.ReadDir(s.snapDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range ents {
		if reSnap.MatchString(e.Name()) {
			out = append(out, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(out)))
	return out, nil
}

var reSnap = regexp.MustCompile(`^\d{8}-\d{6}\.\d{3}\.yml$`)

// ReadSnapshot only accepts names produced by snapshot() (no path traversal).
func (s *store) ReadSnapshot(name string) (string, error) {
	if !reSnap.MatchString(name) {
		return "", fmt.Errorf("invalid snapshot name")
	}
	b, err := os.ReadFile(filepath.Join(s.snapDir(), name))
	return string(b), err
}

func (s *store) log(ev Event) {
	b, _ := json.Marshal(ev)
	f, err := os.OpenFile(filepath.Join(s.stateDir, "history.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(b, '\n'))
}

// History returns the newest n events.
func (s *store) History(n int) []Event {
	b, err := os.ReadFile(filepath.Join(s.stateDir, "history.jsonl"))
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	var out []Event
	for i := len(lines) - 1; i >= 0 && len(out) < n; i-- {
		var ev Event
		if json.Unmarshal([]byte(lines[i]), &ev) == nil {
			out = append(out, ev)
		}
	}
	return out
}
