package term_test

import (
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	gssh "github.com/gliderlabs/ssh"

	"github.com/cuonggt/omassh/internal/sshx"
	"github.com/cuonggt/omassh/internal/store"
	"github.com/cuonggt/omassh/internal/term"
)

// capture accumulates everything the far end receives, from the goroutine
// reading the session.
type capture struct {
	mu sync.Mutex
	sb strings.Builder
}

func (c *capture) add(b []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sb.Write(b)
}

func (c *capture) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sb.String()
}

// startCaptureServer runs an SSH server that reports whatever input reaches
// it, optionally announcing bracketed paste first — which is how an
// interactive shell says it can take a paste in one piece.
func startCaptureServer(t *testing.T, hostKey string, announce bool, got *capture) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &gssh.Server{
		PublicKeyHandler: func(gssh.Context, gssh.PublicKey) bool { return true },
		Handler: func(s gssh.Session) {
			if _, _, isPty := s.Pty(); !isPty {
				s.Exit(1)
				return
			}
			if announce {
				io.WriteString(s, "\x1b[?2004h")
			}
			// A marker the pane can be waited for, so the paste below happens
			// after the emulator has read the announcement above it.
			io.WriteString(s, "ready\r\n")

			buf := make([]byte, 4096)
			for {
				n, err := s.Read(buf)
				if n > 0 {
					got.add(buf[:n])
				}
				if err != nil {
					return
				}
			}
		},
	}
	if err := gssh.HostKeyFile(hostKey)(srv); err != nil {
		t.Fatalf("host key: %v", err)
	}
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close(); l.Close() })
	return l.Addr().(*net.TCPAddr).Port
}

func pasteHost(t *testing.T, announce bool, got *capture) store.Host {
	t.Helper()
	dir := t.TempDir()
	hk := genKey(t, dir+"/host")
	ck := genKey(t, dir+"/client")
	port := startCaptureServer(t, hk, announce, got)
	return store.Host{ID: "h1", Name: "testsrv", Addr: "127.0.0.1", Port: port,
		User: "tester", Identity: ck}
}

// waitForCapture polls until the far end has received want.
func waitForCapture(t *testing.T, got *capture, want string, d time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(d)
	var last string
	for time.Now().Before(deadline) {
		last = got.String()
		if strings.Contains(last, want) {
			return last
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("the far end never received %q; it got %q", want, last)
	return ""
}

const multiline = "set -e\nsystemctl restart nginx"

// A shell that has said it understands a paste gets the whole script in one
// piece, bracketed. Typed instead, every line would run as it arrived — a
// four-line script becoming four commands, the first of them often the one
// that makes the rest wrong.
func TestAPasteIsBracketedWhereTheRemoteAsksForIt(t *testing.T) {
	if !term.TmuxAvailable() {
		t.Skip("needs a pane")
	}
	var got capture
	p := openPasteSession(t, pasteHost(t, true, &got))

	waitFor(t, p, "ready", 15*time.Second)
	p.Paste(multiline)

	seen := waitForCapture(t, &got, "\x1b[201~", 15*time.Second)
	if !strings.Contains(seen, "\x1b[200~"+multiline+"\x1b[201~") {
		t.Errorf("the far end got %q, want the script bracketed as one paste", seen)
	}
}

// A remote that never asked gets the text as it is, which is what a real
// terminal does too. Nothing here can make that case safe; not sending a
// trailing newline is what keeps the last line from running on arrival.
func TestAPasteIsPlainWhereTheRemoteNeverAskedForIt(t *testing.T) {
	if !term.TmuxAvailable() {
		t.Skip("needs a pane")
	}
	var got capture
	p := openPasteSession(t, pasteHost(t, false, &got))

	waitFor(t, p, "ready", 15*time.Second)
	p.Paste(multiline)

	seen := waitForCapture(t, &got, "systemctl restart nginx", 15*time.Second)
	if strings.Contains(seen, "\x1b[200~") {
		t.Errorf("the far end got %q, want no bracketing from a remote that never asked", seen)
	}
}

func openPasteSession(t *testing.T, h store.Host) *term.Pane {
	t.Helper()
	// The test server's key is new every run, so ssh has to be told not to
	// consult a known_hosts file it would then write into.
	sshx.SetGlobalOptions([]string{
		"StrictHostKeyChecking=no", "UserKnownHostsFile=/dev/null", "IdentitiesOnly=yes",
	})
	t.Cleanup(func() { sshx.SetGlobalOptions(nil) })

	p, err := term.Open(h, 80, 24)
	if err != nil {
		t.Fatalf("open pane: %v", err)
	}
	t.Cleanup(func() { p.Kill() })
	return p
}
