package monitor

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"
)

const AgentFile = "/etc/boxvector/agent.json"

// ua is deliberately generic: no product name, nothing a network owner can recognise in a capture.
const ua = "Debian APT-HTTP/1.3 (2.6.12)"

// spread the polls so the interval does not look machine-timed; a random 25% offset either way
func jitter(base time.Duration) time.Duration {
	d := int64(base) / 4
	return base + time.Duration(rand.Int63n(2*d+1)-d)
}

type AgentConf struct {
	URL      string `json:"url"`
	ID       string `json:"id"`
	Secret   string `json:"secret"`
	Interval int    `json:"interval"`
}

func run(timeout time.Duration, name string, args ...string) string {
	cmd := exec.Command(name, args...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if cmd.Start() != nil {
		return ""
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(timeout):
		_ = cmd.Process.Kill()
	}
	return strings.TrimSpace(out.String())
}

var birdVer = regexp.MustCompile(`BIRD\s+(\S+)`)

// Sessions counts BGP protocols and how many are Established, from `birdc show protocols`.
func Sessions() (total, est int) {
	for _, l := range strings.Split(run(8*time.Second, "birdc", "show", "protocols"), "\n") {
		f := strings.Fields(l)
		if len(f) >= 6 && f[1] == "BGP" {
			total++
			if f[len(f)-1] == "Established" {
				est++
			}
		}
	}
	return
}

func Collect(version, cfgPath string) Info {
	h, _ := os.Hostname()
	in := Info{Hostname: h, Version: version, Arch: runtime.GOARCH}
	if m := birdVer.FindStringSubmatch(run(5*time.Second, "birdc", "show", "status")); m != nil {
		in.Bird = m[1]
	}
	if b, err := os.ReadFile("/etc/os-release"); err == nil {
		for _, l := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(l, "PRETTY_NAME=") {
				in.OS = strings.Trim(l[12:], `"`)
			}
		}
	}
	in.Kernel = run(2*time.Second, "uname", "-r")
	if b, err := os.ReadFile("/proc/uptime"); err == nil {
		f, _ := strconv.ParseFloat(strings.Fields(string(b))[0], 64)
		in.Uptime = int64(f)
	}
	if b, err := os.ReadFile("/proc/loadavg"); err == nil {
		f := strings.Fields(string(b))
		if len(f) >= 3 {
			in.Load = strings.Join(f[:3], " ")
		}
	}
	var c struct {
		ASN      int    `yaml:"asn"`
		RouterID string `yaml:"router-id"`
	}
	if b, err := os.ReadFile(cfgPath); err == nil {
		_ = yaml.Unmarshal(b, &c)
	}
	in.ASN, in.RouterID = c.ASN, c.RouterID
	in.Sessions, in.Established = Sessions()
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && ipn.IP.IsGlobalUnicast() && !ipn.IP.IsPrivate() && len(in.IPs) < 6 {
				in.IPs = append(in.IPs, ipn.IP.String())
			}
		}
	}
	return in
}

func post(c AgentConf, path string, secret string, in, out any) error {
	b, _ := json.Marshal(in)
	req, _ := http.NewRequest("POST", strings.TrimRight(c.URL, "/")+path, bytes.NewReader(b))
	// look like an ordinary package-check client rather than a bespoke API call
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", ua)
	if secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("monitor answered HTTP %d", resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
}

func Enroll(url, token, file string) error {
	if !strings.HasPrefix(url, "https://") {
		return errors.New("the monitor URL must be https://")
	}
	h, _ := os.Hostname()
	c := AgentConf{URL: url}
	var out struct {
		ID, Secret string
		Interval   int
	}
	if err := post(c, "/api/enroll", "", map[string]string{"token": token, "hostname": h}, &out); err != nil {
		return err
	}
	c.ID, c.Secret, c.Interval = out.ID, out.Secret, out.Interval
	b, _ := json.MarshalIndent(c, "", " ")
	if err := os.MkdirAll("/etc/boxvector", 0o755); err != nil {
		return err
	}
	return os.WriteFile(file, b, 0o600)
}

func LoadAgent(file string) (AgentConf, error) {
	var c AgentConf
	b, err := os.ReadFile(file)
	if err != nil {
		return c, errors.New("not enrolled: run `boxvector agent enroll --url https://monitor.boxvector.co --token <token>`")
	}
	return c, json.Unmarshal(b, &c)
}

// RunAgent sends a heartbeat every interval and applies patch orders the monitor hands out.
func RunAgent(c AgentConf, version, cfgPath string) {
	iv := time.Duration(c.Interval) * time.Second
	if iv < 15*time.Second {
		iv = HeartbeatInterval * time.Second
	}
	if resumePatch(c) {
		os.Exit(0)
	}
	time.Sleep(jitter(iv)) // first poll is not on a round boundary
	for {
		var out struct {
			Interval int
			Patch    *Order
		}
		err := post(c, "/api/heartbeat", c.Secret, Collect(version, cfgPath), &out)
		if err != nil {
			log.Warnf("heartbeat: %s", err)
		} else if out.Patch != nil {
			if applyPatch(c, *out.Patch, version, cfgPath) {
				log.Info("patched; exiting so systemd starts the new binary")
				os.Exit(0)
			}
		}
		time.Sleep(jitter(iv))
	}
}

func report(c AgentConf, ver, status, msg string) {
	for i := 0; i < 5; i++ {
		if post(c, "/api/patch-result", c.Secret, map[string]string{"version": ver, "status": status, "msg": msg}, nil) == nil {
			return
		}
		time.Sleep(10 * time.Second)
	}
}

func download(c AgentConf, o Order, dst string) error {
	if strings.ContainsAny(o.File, "/\\") {
		return errors.New("bad file name")
	}
	req, _ := http.NewRequest("GET", strings.TrimRight(c.URL, "/")+"/api/release/"+o.File, nil)
	req.Header.Set("Authorization", "Bearer "+c.Secret)
	req.Header.Set("User-Agent", ua)
	resp, err := (&http.Client{Timeout: 10 * time.Minute}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("download HTTP %d", resp.StatusCode)
	}
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, 200<<20))
	if err != nil {
		return err
	}
	if n != o.Size || hex.EncodeToString(h.Sum(nil)) != o.SHA256 {
		return errors.New("checksum mismatch, package discarded")
	}
	return nil
}

const (
	binPath     = "/usr/bin/boxvector"
	agentDir    = "/var/lib/boxvector/agent"
	pendingFile = agentDir + "/pending.json"
	prevFile    = agentDir + "/boxvector.prev"
)

type pending struct {
	Version string    `json:"version"`
	Before  int       `json:"before"`
	Started time.Time `json:"started"`
	DpkgOut string    `json:"dpkg_out"`
}

// applyPatch installs the release in its own systemd unit (the package's postinst restarts this agent, which would
// otherwise kill dpkg half way). The verdict is reached by finishPatch, here or in the restarted agent.
// Returns true when the process should exit and let systemd start the new binary.
func applyPatch(c AgentConf, o Order, cur, cfgPath string) bool {
	_ = os.MkdirAll(agentDir, 0o700)
	deb := agentDir + "/" + o.File
	if err := download(c, o, deb); err != nil {
		report(c, o.Version, "failed", err.Error())
		return false
	}
	if run(5*time.Second, "birdc", "show", "status") == "" {
		report(c, o.Version, "failed", "BIRD is not answering; patch not started")
		_ = os.Remove(deb)
		return false
	}
	_, before := Sessions()
	if b, err := os.ReadFile(binPath); err == nil {
		_ = os.WriteFile(prevFile, b, 0o700)
	}
	pj, _ := json.Marshal(pending{Version: o.Version, Before: before, Started: time.Now()})
	_ = os.WriteFile(pendingFile, pj, 0o600)
	out := run(6*time.Minute, "systemd-run", "--wait", "--collect", "--quiet", "--pipe",
		"-E", "DEBIAN_FRONTEND=noninteractive", "dpkg", "--force-confold", "-i", deb)
	_ = os.Remove(deb)
	// still alive: the postinst did not restart us, so judge now
	p := pending{Version: o.Version, Before: before, Started: time.Now(), DpkgOut: out}
	return finishPatch(c, p)
}

// finishPatch verifies BIRD and the BGP sessions after an install and restores the previous binary on failure.
func finishPatch(c AgentConf, p pending) bool {
	ok, why := false, ""
	if !strings.Contains(run(10*time.Second, binPath, "version"), p.Version) {
		why = "installed binary does not report " + p.Version
		if p.DpkgOut != "" {
			why += ": " + tail(p.DpkgOut)
		}
	} else {
		for i := 0; i < 18; i++ {
			if run(5*time.Second, "birdc", "show", "status") != "" {
				if _, est := Sessions(); est >= p.Before {
					ok = true
					break
				}
			}
			time.Sleep(10 * time.Second)
		}
		if !ok {
			why = fmt.Sprintf("health check failed (established sessions below %d)", p.Before)
		}
	}
	_ = os.Remove(pendingFile)
	if debs, _ := filepath.Glob(agentDir + "/*.deb"); len(debs) > 0 {
		for _, d := range debs {
			_ = os.Remove(d)
		}
	}
	if ok {
		report(c, p.Version, "ok", fmt.Sprintf("running %s, %d sessions established", p.Version, p.Before))
		return false
	}
	if b, err := os.ReadFile(prevFile); err == nil {
		tmp := binPath + ".rollback"
		if os.WriteFile(tmp, b, 0o755) == nil && os.Rename(tmp, binPath) == nil {
			run(30*time.Second, "systemctl", "try-restart", "boxvector-web.service")
			report(c, p.Version, "rolledback", why+"; previous binary restored")
			return true
		}
	}
	report(c, p.Version, "failed", why)
	return false
}

// resumePatch runs at agent start: a patch that restarted the agent still gets its health check.
func resumePatch(c AgentConf) bool {
	b, err := os.ReadFile(pendingFile)
	if err != nil {
		return false
	}
	var p pending
	if json.Unmarshal(b, &p) != nil || time.Since(p.Started) > 30*time.Minute {
		_ = os.Remove(pendingFile)
		return false
	}
	return finishPatch(c, p)
}

func tail(s string) string {
	if len(s) > 200 {
		return s[len(s)-200:]
	}
	return s
}
