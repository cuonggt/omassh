package sftpx

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/pkg/sftp"

	"github.com/cuonggt/omassh/internal/sshx"
	"github.com/cuonggt/omassh/internal/store"
)

// Session is a live SFTP connection to one host.
//
// Rather than dial SSH itself, Omassh runs `ssh -s <host> sftp` and speaks the
// SFTP protocol over that child's stdio. OpenSSH therefore performs the
// connection exactly as it would for an interactive session: ProxyJump chains,
// ProxyCommand, certificates, Match blocks, IdentityAgent and known_hosts all
// apply, with no second implementation of any of it to keep in step.
type Session struct {
	client *sftp.Client
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stderr *bytes.Buffer
	label  string
	home   string

	closeOnce sync.Once
}

// Connect opens an SFTP session to h.
//
// BatchMode is forced on: the child's stdin carries the SFTP protocol, so ssh
// has nowhere to prompt for a passphrase, and failing immediately with a clear
// message beats hanging on a prompt no one can see.
func Connect(h store.Host, opts ...string) (*Session, error) {
	args := sshx.SubsystemArgs(h, "sftp",
		append([]string{"-o", "BatchMode=yes", "-o", "LogLevel=ERROR"}, opts...)...)
	cmd := exec.Command("ssh", args...)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var errbuf bytes.Buffer
	cmd.Stderr = &errbuf

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start ssh: %w", err)
	}

	client, err := sftp.NewClientPipe(stdout, stdin)
	if err != nil {
		cmd.Process.Kill()
		cmd.Wait()
		return nil, connectError(err, errbuf.String())
	}

	s := &Session{client: client, cmd: cmd, stdin: stdin, stderr: &errbuf, label: h.Name}
	if wd, err := client.Getwd(); err == nil {
		s.home = wd
	} else {
		s.home = "/"
	}
	return s, nil
}

// connectError turns ssh's own diagnostics into the message, since "EOF" from
// the SFTP handshake says nothing about why the connection failed.
func connectError(err error, stderr string) error {
	msg := strings.TrimSpace(stderr)
	if msg == "" {
		return fmt.Errorf("sftp: %w", err)
	}
	if i := strings.LastIndex(msg, "\n"); i >= 0 {
		msg = strings.TrimSpace(msg[i+1:])
	}
	if strings.Contains(msg, "Permission denied") || strings.Contains(msg, "publickey") {
		msg += " — unlock the credential first (K, then u) or add a key to your agent"
	}
	return fmt.Errorf("%s", msg)
}

func (s *Session) Close() error {
	var err error
	s.closeOnce.Do(func() {
		if s.client != nil {
			err = s.client.Close()
		}
		if s.stdin != nil {
			s.stdin.Close()
		}
		if s.cmd == nil || s.cmd.Process == nil {
			return
		}
		// Closing the pipes should end ssh; insist only if it lingers.
		done := make(chan struct{})
		go func() { s.cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			s.cmd.Process.Kill()
			<-done
		}
	})
	return err
}

// --- FS ----------------------------------------------------------------

func (s *Session) Label() string                       { return s.label }
func (s *Session) Home() string                        { return s.home }
func (s *Session) Join(dir, name string) string        { return remoteJoin(dir, name) }
func (s *Session) Parent(dir string) string            { return remoteParent(dir) }
func (s *Session) Mkdir(p string) error                { return s.client.Mkdir(p) }
func (s *Session) Rename(old, neu string) error        { return s.client.Rename(old, neu) }
func (s *Session) Chmod(p string, m os.FileMode) error { return s.client.Chmod(p, m) }

func (s *Session) List(dir string) ([]Entry, error) {
	fis, err := s.client.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(fis))
	for _, fi := range fis {
		out = append(out, Entry{
			Name: fi.Name(), Size: fi.Size(), Mode: fi.Mode(),
			ModTime: fi.ModTime(), IsDir: fi.IsDir(),
		})
	}
	sortEntries(out)
	return out, nil
}

// Remove deletes a file, or a directory and everything beneath it. SFTP has no
// recursive delete, so the walk happens here.
func (s *Session) Remove(p string) error {
	fi, err := s.client.Stat(p)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return s.client.Remove(p)
	}
	entries, err := s.client.ReadDir(p)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := s.Remove(remoteJoin(p, e.Name())); err != nil {
			return err
		}
	}
	return s.client.RemoveDirectory(p)
}

func (s *Session) Open(p string) (io.ReadCloser, error) { return s.client.Open(p) }

func (s *Session) Create(p string) (io.WriteCloser, error) { return s.client.Create(p) }

func (s *Session) Stat(p string) (Entry, error) {
	fi, err := s.client.Stat(p)
	if err != nil {
		return Entry{}, err
	}
	return Entry{Name: fi.Name(), Size: fi.Size(), Mode: fi.Mode(),
		ModTime: fi.ModTime(), IsDir: fi.IsDir()}, nil
}
