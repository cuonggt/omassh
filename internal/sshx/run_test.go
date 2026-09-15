package sshx

import (
	"context"
	"errors"
	"net"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gssh "github.com/gliderlabs/ssh"

	"github.com/cuonggt/omassh/internal/store"
)

// startRunServer runs a real SSH server that executes what it is sent, so
// these tests drive the whole path: the ssh binary, the argv this package
// builds, the remote shell, and the status it comes back with.
func startRunServer(t *testing.T, hostKey string) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &gssh.Server{
		PublicKeyHandler: func(gssh.Context, gssh.PublicKey) bool { return true },
		Handler: func(s gssh.Session) {
			if s.RawCommand() == "" {
				// A run always sends one. An interactive session here would
				// mean the command never made it onto the command line.
				s.Stderr().Write([]byte("no command was sent\n"))
				s.Exit(2)
				return
			}
			cmd := exec.Command("sh", "-c", s.RawCommand())
			cmd.Stdout, cmd.Stderr = s, s.Stderr()
			var ee *exec.ExitError
			switch err := cmd.Run(); {
			case err == nil:
				s.Exit(0)
			case errors.As(err, &ee):
				s.Exit(ee.ExitCode())
			default:
				s.Exit(1)
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

func genRunKey(t *testing.T, path string) string {
	t.Helper()
	cmd := exec.Command("ssh-keygen", "-t", "ed25519", "-N", "", "-C", "test", "-f", path, "-q")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v: %s", err, out)
	}
	return path
}

// runHost is a host pointing at a server of its own, plus the options that
// keep a test off the developer's known_hosts.
func runHost(t *testing.T) (store.Host, []string) {
	t.Helper()
	if _, err := exec.LookPath("ssh"); err != nil {
		t.Skip("no ssh on this machine")
	}
	dir := t.TempDir()
	port := startRunServer(t, genRunKey(t, filepath.Join(dir, "host")))
	h := store.Host{Name: "testsrv", Addr: "127.0.0.1", Port: port,
		User: "tester", Identity: genRunKey(t, filepath.Join(dir, "client"))}
	return h, []string{
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "LogLevel=ERROR",
	}
}

// The script reaches the far end whole. ssh joins everything after the
// destination with spaces, so a script handed over as several arguments comes
// back with its newlines flattened — and a line beginning with # would then
// comment out the rest of the script rather than itself.
func TestAMultiLineScriptArrivesWithItsLinesIntact(t *testing.T) {
	h, opts := runHost(t)
	script := "echo one\n# this comments out nothing but itself\necho two\n"

	r := Run(context.Background(), h, script, opts...)
	if r.Err != nil {
		t.Fatalf("run: %v\n%s", r.Err, r.Output)
	}
	if r.ExitCode != 0 {
		t.Fatalf("exit %d: %s", r.ExitCode, r.Output)
	}
	if got := strings.Fields(r.Output); len(got) != 2 || got[0] != "one" || got[1] != "two" {
		t.Errorf("output = %q, want both lines to have run", r.Output)
	}
}

// What a script exits with is an ordinary result, not an error in omassh.
func TestAScriptThatFailsComesBackWithItsStatus(t *testing.T) {
	h, opts := runHost(t)

	r := Run(context.Background(), h, "echo trouble >&2; exit 3", opts...)
	if r.Err != nil {
		t.Fatalf("a non-zero script was reported as omassh's own failure: %v", r.Err)
	}
	if r.ExitCode != 3 {
		t.Errorf("exit = %d, want 3", r.ExitCode)
	}
	if !r.Failed() {
		t.Error("Failed() = false for a script that exited 3")
	}
	// stderr is collected too, because that is where a script says what went
	// wrong and where ssh says why it could not connect at all.
	if !strings.Contains(r.Output, "trouble") {
		t.Errorf("output = %q, want what the script wrote to stderr", r.Output)
	}
}

// The summary is the last thing said, which is what a list of results shows
// beside each host.
func TestTheSummaryIsTheLastThingSaid(t *testing.T) {
	h, opts := runHost(t)

	r := Run(context.Background(), h, "echo first; echo last; echo", opts...)
	if got := r.Summary(); got != "last" {
		t.Errorf("Summary() = %q, want the last non-empty line", got)
	}
}

// Nothing is on the child's stdin, so a script that reads from it gets end of
// file. Left attached to omassh's own, a run would take the keystrokes meant
// for the interface and wait forever for a line that never came.
func TestAScriptThatReadsStdinGetsEndOfFile(t *testing.T) {
	h, opts := runHost(t)

	done := make(chan Result, 1)
	go func() { done <- Run(context.Background(), h, "cat; echo done", opts...) }()
	select {
	case r := <-done:
		if !strings.Contains(r.Output, "done") {
			t.Errorf("output = %q, want the script to have finished", r.Output)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("a script reading stdin never finished")
	}
}

// Stopping a run is not a verdict on the script. A cancelled run that reported
// the child's status would blame the host for something done from here.
func TestAStoppedRunIsNotReportedAsTheScriptFailing(t *testing.T) {
	h, opts := runHost(t)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan Result, 1)
	go func() { done <- Run(ctx, h, "sleep 30", opts...) }()
	time.Sleep(time.Second)
	cancel()

	select {
	case r := <-done:
		if r.Err == nil {
			t.Fatalf("a stopped run came back as an ordinary result: exit %d", r.ExitCode)
		}
		if r.ExitCode != 0 {
			t.Errorf("exit = %d, want nothing said about a status nobody waited for", r.ExitCode)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("cancelling did not stop the run")
	}
}

// Output past the cap is dropped rather than kept, and the fact is recorded
// so the screen can say so instead of quietly showing part of it.
func TestOutputPastTheCapIsMarkedRatherThanHidden(t *testing.T) {
	var c capped
	first := strings.Repeat("a", maxOutput-1)
	c.Write([]byte(first))
	if c.over {
		t.Fatal("marked as over before the cap was reached")
	}
	n, err := c.Write([]byte("bbb"))
	if n != 3 || err != nil {
		// Short writes break the pipe and end the script early, which is a
		// different thing from having said more than is being kept.
		t.Errorf("Write = %d, %v — want the whole write reported as taken", n, err)
	}
	if !c.over {
		t.Error("output past the cap was dropped without saying so")
	}
	if c.buf.Len() != maxOutput {
		t.Errorf("kept %d bytes, want the cap exactly", c.buf.Len())
	}
}

// No terminal, and not merely by default: ssh keeps the first value it sees,
// so a RequestTTY in anyone's ssh_config would otherwise win.
func TestARunAsksForNoTerminal(t *testing.T) {
	args := RunArgs(store.Host{Name: "web", Addr: "10.0.0.1"}, "uptime")

	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-o RequestTTY=no") {
		t.Errorf("args = %v, want RequestTTY=no", args)
	}
	// And the script is the last argument, after the destination, whole.
	if args[len(args)-1] != "uptime" {
		t.Errorf("last argument is %q, want the script", args[len(args)-1])
	}
	if args[len(args)-2] != "10.0.0.1" {
		t.Errorf("the script does not follow the destination: %v", args)
	}
}
