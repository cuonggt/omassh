package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
	gssh "github.com/gliderlabs/ssh"

	"github.com/cuonggt/omassh/internal/sshx"
	"github.com/cuonggt/omassh/internal/store"
)

// TestMain lets this test binary be the askpass helper, run the way ssh runs
// omassh: with the prompt as its only argument and the credential in the
// environment. The helper's answer is only worth testing as ssh reads it — a
// function that returns "no" says nothing about a connection that ends.
func TestMain(m *testing.M) {
	if os.Getenv(sshx.EnvCredential) != "" {
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// hostKeyQuestion is ssh's own, as OpenSSH 10 asks it.
const hostKeyQuestion = "The authenticity of host '[127.0.0.1]:42299 ([127.0.0.1]:42299)' can't be established.\n" +
	"ED25519 key fingerprint is: SHA256:J5RyLcerto95ePluryuFYZrQDQEHSEal1mHa31Thmk0\n" +
	"This key is not known by any other names.\n" +
	"Are you sure you want to continue connecting (yes/no/[fingerprint])? "

func TestTheHelperTellsAQuestionFromAPasswordPrompt(t *testing.T) {
	for _, c := range []struct {
		prompt   string
		question bool
	}{
		{hostKeyQuestion, true},
		{"Please type 'yes', 'no' or the fingerprint: ", true},
		{"Please type 'yes' or 'no': ", true},
		{"Accept updated hostkeys? (yes/no): ", true},
		{"tester@127.0.0.1's password: ", false},
		{"(tester@127.0.0.1) Password: ", false},
		// A server's own prompt, in its own words, is still the password.
		{"(tester@127.0.0.1) Passwort: ", false},
	} {
		if got := isQuestion(c.prompt); got != c.question {
			t.Errorf("isQuestion(%q) = %v, want %v", c.prompt, got, c.question)
		}
	}
}

// With nobody at the connection a question is answered no, and the keychain is
// never asked: the credential here has no password stored, so reaching for one
// would have failed.
func TestAQuestionIsAnsweredNoWhenNobodyIsThere(t *testing.T) {
	t.Setenv(sshx.EnvAttended, "")
	var out bytes.Buffer
	if err := askpass("no-such-credential", hostKeyQuestion, &out); err != nil {
		t.Fatalf("askpass: %v", err)
	}
	if got := out.String(); got != "no\n" {
		t.Errorf("answered %q, want no", got)
	}
}

// A password host whose key ssh has not seen, reached where nobody is there to
// be asked, is refused at once rather than asked forever. ssh puts every
// question to the helper under SSH_ASKPASS_REQUIRE=force, and the helper
// answered this one with the password — not yes, no or a fingerprint, so ssh
// asked again, several hundred times a second, and the tunnel, sftp session or
// snippet run never ended.
func TestAnUnknownHostKeyIsRefusedWhereNobodyIsThereToAsk(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	h := passwordHostAt(t, startOpenServer(t))
	cmd, known := sshTo(ctx, t, h, sshx.Unattended(h), sshx.Env(h))

	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("ssh was still asking after 15s:\n%s", out)
	}
	if err == nil {
		t.Fatalf("ssh connected to a host whose key nobody accepted:\n%s", out)
	}
	if !strings.Contains(string(out), "Host key verification failed") {
		t.Errorf("ssh did not refuse the key:\n%s", out)
	}
	if b, _ := os.ReadFile(known); len(b) > 0 {
		t.Errorf("a key nobody accepted was recorded as trusted:\n%s", b)
	}
}

// Someone at the session is asked on their own terminal, as ssh alone would ask
// them, and what they answer is what ssh hears: yes keeps the key, and the
// connection goes on.
func TestAnUnknownHostKeyIsPutToWhoeverIsAtTheSession(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	h := passwordHostAt(t, startOpenServer(t))
	cmd, known := sshTo(ctx, t, h, nil, sshx.AttendedEnv(h))

	tty, err := pty.Start(cmd)
	if err != nil {
		t.Fatalf("start ssh on a terminal: %v", err)
	}
	defer tty.Close()
	var screen syncBuffer
	go io.Copy(&screen, tty)

	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(screen.String(), "continue connecting") {
		if time.Now().After(deadline) {
			t.Fatalf("the question never reached the terminal; it showed:\n%s", screen.String())
		}
		time.Sleep(25 * time.Millisecond)
	}
	io.WriteString(tty, "yes\n")

	err = cmd.Wait()
	if ctx.Err() != nil {
		t.Fatalf("ssh was still waiting after 15s:\n%s", screen.String())
	}
	if err != nil {
		t.Fatalf("ssh failed after the key was accepted: %v\n%s", err, screen.String())
	}
	if b, _ := os.ReadFile(known); len(b) == 0 {
		t.Error("the key that was accepted was not kept")
	}
}

// startOpenServer is an SSH server that lets anyone in and runs nothing. Its
// host key is made afresh each time, so ssh has never seen it — which is the
// point — and since it asks for no password, the keychain is never reached.
func startOpenServer(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &gssh.Server{Handler: func(s gssh.Session) { s.Exit(0) }}
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close() })
	return l.Addr().(*net.TCPAddr).Port
}

// passwordHostAt is a host that logs in with a password credential, which is
// what puts the askpass helper in front of ssh at all.
func passwordHostAt(t *testing.T, port int) store.Host {
	t.Helper()
	if _, err := exec.LookPath("ssh"); err != nil {
		t.Skip("ssh is not installed")
	}
	return store.Host{Name: "legacy", Addr: "127.0.0.1", Port: port, User: "tester",
		Cred: &store.Credential{ID: "no-such-credential", Name: "Legacy",
			Kind: store.CredentialPassword, User: "tester"}}
}

// sshTo is the command omassh runs for h, apart from the known_hosts file: a
// fresh one, so the server's key is unknown, and -F /dev/null so that nothing
// in the config of whoever runs this accepts the key on ssh's behalf.
func sshTo(ctx context.Context, t *testing.T, h store.Host, fixed, env []string) (*exec.Cmd, string) {
	t.Helper()
	known := filepath.Join(t.TempDir(), "known_hosts")
	args := append([]string{"-F", "/dev/null", "-o", "UserKnownHostsFile=" + known, "-o", "LogLevel=ERROR"},
		sshx.BuildWith(fixed, h)...)
	cmd := exec.CommandContext(ctx, "ssh", append(args, "true")...)
	cmd.Env = append(os.Environ(), env...)
	// A helper still waiting would hold ssh's output open past its death.
	cmd.WaitDelay = time.Second
	return cmd, known
}

// syncBuffer is a buffer one goroutine writes while another reads it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
