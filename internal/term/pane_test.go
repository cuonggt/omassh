package term_test

import (
	"fmt"
	"io"
	"net"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	cpty "github.com/creack/pty"
	gssh "github.com/gliderlabs/ssh"

	"github.com/cuonggt/omassh/internal/keys"
	"github.com/cuonggt/omassh/internal/sshx"
	"github.com/cuonggt/omassh/internal/store"
	"github.com/cuonggt/omassh/internal/term"
)

// startShellServer runs an SSH server that honours pty requests and gives each
// session a real shell, so the pane is driving a genuine interactive session
// rather than something that merely echoes.
func startShellServer(t *testing.T, hostKey string) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &gssh.Server{
		PublicKeyHandler: func(gssh.Context, gssh.PublicKey) bool { return true },
		Handler: func(s gssh.Session) {
			ptyReq, winCh, isPty := s.Pty()
			if !isPty {
				io.WriteString(s, "no pty requested\n")
				s.Exit(1)
				return
			}
			cmd := exec.Command("sh", "-i")
			cmd.Env = append(cmd.Environ(), "TERM="+ptyReq.Term, "PS1=$ ")
			f, err := cpty.Start(cmd)
			if err != nil {
				s.Exit(1)
				return
			}
			defer f.Close()
			go func() {
				for w := range winCh {
					cpty.Setsize(f, &cpty.Winsize{Rows: uint16(w.Height), Cols: uint16(w.Width)})
				}
			}()
			go func() { io.Copy(f, s) }()
			io.Copy(s, f)
			cmd.Wait()
			s.Exit(0)
		},
	}
	if err := gssh.HostKeyFile(hostKey)(srv); err != nil {
		t.Fatal(err)
	}
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close(); l.Close() })
	return l.Addr().(*net.TCPAddr).Port
}

func testHost(t *testing.T) store.Host {
	t.Helper()
	dir := t.TempDir()
	hk, err := keys.Generate(filepath.Join(dir, "host"), "host", "")
	if err != nil {
		t.Fatal(err)
	}
	ck, err := keys.Generate(filepath.Join(dir, "client"), "client", "")
	if err != nil {
		t.Fatal(err)
	}
	port := startShellServer(t, hk.Path)
	return store.Host{ID: "h1", Name: "testsrv", Addr: "127.0.0.1", Port: port,
		User: "tester", Identity: ck.Path}
}

// waitFor polls the pane's rendered screen until it contains want.
func waitFor(t *testing.T, p *term.Pane, want string, d time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(d)
	var last string
	for time.Now().Before(deadline) {
		// Match against visible text only. Searching the styled render lets a
		// query like "80" match digits inside an escape sequence, which passes
		// while the screen shows nothing of the sort.
		last = visible(p.Render())
		if strings.Contains(last, want) {
			return last
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("pane never showed %q; screen was:\n%s", want, last)
	return ""
}

// ansiRE matches the escape sequences the emulator emits, so failure output is
// readable. The previous version stripped from ESC to the next "m", which any
// cursor-positioning sequence swallowed whole — printing an empty screen and
// hiding the very thing being debugged.
var ansiRE = regexp.MustCompile(`\x1b\][^\x07\x1b]*(\x07|\x1b\\)|\x1b[\[\]][0-9;?]*[a-zA-Z]|\x1b[()][B0]|\x1b[=>]`)

func visible(s string) string {
	out := ansiRE.ReplaceAllString(s, "")
	var lines []string
	for _, l := range strings.Split(out, "\n") {
		if t := strings.TrimRight(l, " "); t != "" {
			lines = append(lines, t)
		}
	}
	return strings.Join(lines, "\n")
}

func TestPaneRunsAnInteractiveSession(t *testing.T) {
	sshx.SetGlobalOptions([]string{
		"StrictHostKeyChecking=no", "UserKnownHostsFile=/dev/null", "IdentitiesOnly=yes",
	})
	t.Cleanup(func() { sshx.SetGlobalOptions(nil) })

	h := testHost(t)
	p, err := term.Open(h, 80, 24)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { p.Close() })

	if !p.Alive() {
		t.Fatal("pane is not alive immediately after opening")
	}
	if w, ht := p.Size(); w != 80 || ht != 24 {
		t.Errorf("Size = %dx%d, want 80x24", w, ht)
	}

	t.Run("keystrokes reach the remote shell", func(t *testing.T) {
		type_(p, "echo pane-works")
		p.SendKey(tea.KeyPressMsg{Code: tea.KeyEnter})
		waitFor(t, p, "pane-works", 15*time.Second)
	})

	t.Run("the remote sees the pane size", func(t *testing.T) {
		type_(p, "stty size")
		p.SendKey(tea.KeyPressMsg{Code: tea.KeyEnter})
		waitFor(t, p, "24 80", 10*time.Second)
	})

	// A resize must reach the far side, or full-screen programs draw into the
	// wrong geometry.
	t.Run("resize propagates to the remote", func(t *testing.T) {
		p.Resize(100, 30)
		if w, ht := p.Size(); w != 100 || ht != 30 {
			t.Fatalf("Size = %dx%d after resize, want 100x30", w, ht)
		}
		time.Sleep(300 * time.Millisecond)
		type_(p, "stty size")
		p.SendKey(tea.KeyPressMsg{Code: tea.KeyEnter})
		waitFor(t, p, "30 100", 10*time.Second)
	})

	t.Run("control keys are encoded, not dropped", func(t *testing.T) {
		// ctrl+c at a prompt should interrupt, leaving the shell usable.
		p.SendKey(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
		time.Sleep(300 * time.Millisecond)
		type_(p, "echo after-interrupt")
		p.SendKey(tea.KeyPressMsg{Code: tea.KeyEnter})
		waitFor(t, p, "after-interrupt", 10*time.Second)
	})

	t.Run("closing ends the session", func(t *testing.T) {
		if err := p.Close(); err != nil {
			t.Logf("Close returned %v", err)
		}
		select {
		case <-p.Done():
		case <-time.After(5 * time.Second):
			t.Fatal("session did not end after Close")
		}
		if p.Alive() {
			t.Error("pane still reports alive after Close")
		}
		t.Logf("final status: %s", p.Status())
	})
}

func TestOpenRejectsTinyPanes(t *testing.T) {
	if _, err := term.Open(store.Host{Addr: "127.0.0.1"}, 1, 1); err == nil {
		t.Error("Open accepted a 1x1 pane")
	}
}

func type_(p *term.Pane, s string) {
	for _, r := range s {
		p.SendKey(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	fmt.Fprint(io.Discard, "")
}
