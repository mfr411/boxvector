package fleet

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// SSHRunner runs scripts over SSH with a dedicated key and pinned host keys.
type SSHRunner struct {
	KeyFile string
	M       *Manager // used to pin host keys on first connect (TOFU)
}

func (r *SSHRunner) Run(ctx context.Context, h Host, script string) (string, error) {
	kb, err := os.ReadFile(r.KeyFile)
	if err != nil {
		return "", fmt.Errorf("ssh key: %w", err)
	}
	signer, err := ssh.ParsePrivateKey(kb)
	if err != nil {
		return "", fmt.Errorf("ssh key: %w", err)
	}
	cfg := &ssh.ClientConfig{
		User: h.User,
		Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: func(_ string, _ net.Addr, k ssh.PublicKey) error {
			fp := ssh.FingerprintSHA256(k)
			if h.HostKey == "" {
				if r.M != nil {
					r.M.Pin(h.Name, fp)
				}
				return nil
			}
			if h.HostKey != fp {
				return fmt.Errorf("host key mismatch for %s (pinned %s, got %s)", h.Name, h.HostKey, fp)
			}
			return nil
		},
		Timeout: 15 * time.Second,
	}
	addr := net.JoinHostPort(h.Addr, strconv.Itoa(h.Port))
	d := net.Dialer{Timeout: 15 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	c, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if err != nil {
		return "", err
	}
	cl := ssh.NewClient(c, chans, reqs)
	defer cl.Close()
	s, err := cl.NewSession()
	if err != nil {
		return "", err
	}
	defer s.Close()
	out := &syncBuf{}
	s.Stdout, s.Stderr = out, out
	s.Stdin = bytes.NewBufferString(script)
	done := make(chan error, 1)
	go func() { done <- s.Run("bash -s") }()
	select {
	case err := <-done:
		// a non-zero exit is reported through the script's own markers
		if ee, ok := err.(*ssh.ExitError); ok {
			out.WriteString(fmt.Sprintf("\nSSH_EXIT=%d\n", ee.ExitStatus()))
			err = nil
		}
		return out.String(), err
	case <-ctx.Done():
		_ = cl.Close()
		return out.String(), ctx.Err()
	}
}

// syncBuf is a mutex-guarded buffer: the ssh package copies stdout and stderr
// from two goroutines, and a plain bytes.Buffer loses output under that race.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) WriteString(x string) (int, error) { return s.Write([]byte(x)) }

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}
