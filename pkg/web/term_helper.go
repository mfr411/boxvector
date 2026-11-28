//go:build linux

package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// The terminal helper is the only root part of boxvector. It listens on a unix socket that only
// root and the boxvector group can open, starts login shells on a PTY and nothing else. The web
// service (sandboxed, not root) authenticates users and proxies to it.

const (
	termRing    = 256 << 10
	termIdle    = 15 * time.Minute
	termMax     = 4
	termPollMax = 25 * time.Second
)

type termSess struct {
	mu     sync.Mutex
	id     string
	pty    *os.File
	cmd    *exec.Cmd
	buf    []byte // last termRing bytes
	base   int    // absolute offset of buf[0]
	done   bool
	wake   chan struct{}
	active time.Time
}

type termHelper struct {
	mu    sync.Mutex
	m     map[string]*termSess
	fails map[string][]time.Time // user -> recent bad passwords
}

func ioctl(fd uintptr, req, arg uintptr) error {
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, req, arg); e != 0 {
		return e
	}
	return nil
}

func setWinsize(f *os.File, cols, rows int) {
	if cols < 2 || rows < 2 || cols > 1000 || rows > 1000 {
		return
	}
	ws := [4]uint16{uint16(rows), uint16(cols), 0, 0}
	_ = ioctl(f.Fd(), syscall.TIOCSWINSZ, uintptr(unsafe.Pointer(&ws[0])))
}

func openPTY() (*os.File, *os.File, error) {
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, nil, err
	}
	var unlock int32
	if err := ioctl(m.Fd(), syscall.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); err != nil {
		m.Close()
		return nil, nil, err
	}
	var n uint32
	if err := ioctl(m.Fd(), syscall.TIOCGPTN, uintptr(unsafe.Pointer(&n))); err != nil {
		m.Close()
		return nil, nil, err
	}
	s, err := os.OpenFile("/dev/pts/"+strconv.Itoa(int(n)), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		m.Close()
		return nil, nil, err
	}
	return m, s, nil
}

func (t *termHelper) newSess(cols, rows int) (*termSess, error) {
	t.mu.Lock()
	live := 0
	for _, s := range t.m {
		if !s.isDone() {
			live++
		}
	}
	if live >= termMax {
		t.mu.Unlock()
		return nil, errors.New("too many open terminals")
	}
	t.mu.Unlock()
	m, sl, err := openPTY()
	if err != nil {
		return nil, err
	}
	sh := "/bin/bash"
	if _, err := os.Stat(sh); err != nil {
		sh = "/bin/sh"
	}
	cmd := exec.Command(sh, "-l")
	cmd.Dir = "/root"
	cmd.Env = []string{"TERM=xterm-256color", "HOME=/root", "USER=root", "LOGNAME=root", "SHELL=" + sh,
		"LANG=C.UTF-8", "PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = sl, sl, sl
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		m.Close()
		sl.Close()
		return nil, err
	}
	sl.Close()
	setWinsize(m, cols, rows)
	s := &termSess{id: randHex(16), pty: m, cmd: cmd, wake: make(chan struct{}), active: time.Now()}
	t.mu.Lock()
	t.m[s.id] = s
	t.mu.Unlock()
	go s.pump()
	go func() { _ = cmd.Wait(); s.finish() }()
	return s, nil
}

func (s *termSess) isDone() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.done }

func (s *termSess) notify() { close(s.wake); s.wake = make(chan struct{}) } // caller holds mu

func (s *termSess) pump() {
	b := make([]byte, 8192)
	for {
		n, err := s.pty.Read(b)
		if n > 0 {
			s.mu.Lock()
			s.buf = append(s.buf, b[:n]...)
			if over := len(s.buf) - termRing; over > 0 {
				s.buf = append([]byte(nil), s.buf[over:]...)
				s.base += over
			}
			s.notify()
			s.mu.Unlock()
		}
		if err != nil {
			s.finish()
			return
		}
	}
}

func (s *termSess) finish() {
	s.mu.Lock()
	if !s.done {
		s.done = true
		s.notify()
	}
	s.mu.Unlock()
	s.pty.Close()
}

func (s *termSess) kill() {
	if s.cmd.Process != nil {
		_ = syscall.Kill(-s.cmd.Process.Pid, syscall.SIGHUP)
		_ = s.cmd.Process.Kill()
	}
	s.finish()
}

func (t *termHelper) get(id string) *termSess {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.m[id]
}

// RunTermHelper serves the root PTY helper on a unix socket until it fails.
func RunTermHelper(sock string) error {
	if os.Geteuid() != 0 {
		return errors.New("the terminal helper must run as root")
	}
	_ = os.Remove(sock)
	old := syscall.Umask(0o117)
	ln, err := net.Listen("unix", sock)
	syscall.Umask(old)
	if err != nil {
		return err
	}
	if g, err := user.LookupGroup("boxvector"); err == nil {
		gid, _ := strconv.Atoi(g.Gid)
		_ = os.Chown(sock, 0, gid)
	}
	_ = os.Chmod(sock, 0o660)
	t := &termHelper{m: map[string]*termSess{}, fails: map[string][]time.Time{}}
	go func() { // idle / finished cleanup
		for range time.Tick(30 * time.Second) {
			t.mu.Lock()
			for id, s := range t.m {
				s.mu.Lock()
				idle := time.Since(s.active) > termIdle
				gone := s.done && time.Since(s.active) > time.Minute
				s.mu.Unlock()
				if idle {
					s.kill()
				}
				if gone || idle {
					delete(t.m, id)
				}
			}
			t.mu.Unlock()
		}
	}()
	mux := http.NewServeMux()
	jerr := func(w http.ResponseWriter, code int, msg string) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
	}
	mux.HandleFunc("POST /new", func(w http.ResponseWriter, r *http.Request) {
		// The helper (root) checks the account itself, so a compromised web process cannot
		// open a root shell without knowing the password of root or a sudo user.
		if code, msg := t.authorize(r.FormValue("user"), r.FormValue("password")); code != 0 {
			jerr(w, code, msg)
			return
		}
		c, _ := strconv.Atoi(r.FormValue("cols"))
		ro, _ := strconv.Atoi(r.FormValue("rows"))
		s, err := t.newSess(c, ro)
		if err != nil {
			jerr(w, 503, err.Error())
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"id": s.id})
	})
	mux.HandleFunc("GET /out", func(w http.ResponseWriter, r *http.Request) {
		s := t.get(r.URL.Query().Get("id"))
		if s == nil {
			jerr(w, 404, "no such terminal")
			return
		}
		off, _ := strconv.Atoi(r.URL.Query().Get("off"))
		deadline := time.Now().Add(termPollMax)
		for {
			s.mu.Lock()
			if off < s.base {
				off = s.base // fell behind the ring: skip ahead
			}
			end := s.base + len(s.buf)
			if off < end || s.done || time.Now().After(deadline) {
				data := append([]byte(nil), s.buf[off-s.base:]...)
				done := s.done && off >= end
				s.mu.Unlock()
				w.Header().Set("Content-Type", "application/octet-stream")
				w.Header().Set("X-Next", strconv.Itoa(end))
				if done {
					w.Header().Set("X-Done", "1")
				}
				_, _ = w.Write(data)
				return
			}
			wake := s.wake
			s.mu.Unlock()
			select {
			case <-wake:
			case <-time.After(time.Until(deadline)):
			case <-r.Context().Done():
				return
			}
		}
	})
	mux.HandleFunc("POST /in", func(w http.ResponseWriter, r *http.Request) {
		s := t.get(r.FormValue("id"))
		if s == nil || s.isDone() {
			jerr(w, 404, "no such terminal")
			return
		}
		s.mu.Lock()
		s.active = time.Now()
		s.mu.Unlock()
		if _, err := s.pty.WriteString(r.FormValue("d")); err != nil {
			jerr(w, 500, err.Error())
			return
		}
		w.WriteHeader(204)
	})
	mux.HandleFunc("POST /resize", func(w http.ResponseWriter, r *http.Request) {
		if s := t.get(r.FormValue("id")); s != nil && !s.isDone() {
			c, _ := strconv.Atoi(r.FormValue("cols"))
			ro, _ := strconv.Atoi(r.FormValue("rows"))
			setWinsize(s.pty, c, ro)
		}
		w.WriteHeader(204)
	})
	mux.HandleFunc("POST /close", func(w http.ResponseWriter, r *http.Request) {
		if s := t.get(r.FormValue("id")); s != nil {
			s.kill()
		}
		w.WriteHeader(204)
	})
	fmt.Fprintf(os.Stderr, "boxvector terminal helper on %s\n", sock)
	return (&http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}).Serve(ln)
}
