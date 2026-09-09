package ui

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/cuonggt/omassh/internal/term"
)

// TestMain isolates the tmux server, for the same reason internal/term does:
// these tests open real panes, which are tmux-backed, and they must not land
// among the sessions someone is actually using.
func TestMain(m *testing.M) {
	socket := fmt.Sprintf("omassh-test-ui-%d", os.Getpid())
	os.Setenv(term.SocketEnv, socket)

	// tmux keeps its sockets under TMUX_TMPDIR and leaves the file behind when
	// a server is killed, so a day of test runs left hundreds of dead sockets
	// in the directory the real ones live in. Pointing it at a directory of
	// this run's own means the run clears up after itself — and cannot reach
	// the socket someone's own sessions are on even if the names collided.
	//
	// Under /tmp rather than the default temporary directory: a unix socket
	// path is capped near 104 bytes, and macOS puts TMPDIR deep enough under
	// /var/folders that the socket would sit on the edge of it.
	dir, err := os.MkdirTemp("/tmp", "omassh-tmux-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "isolating tmux for the tests:", err)
		os.Exit(1)
	}
	os.Setenv("TMUX_TMPDIR", dir)

	code := m.Run()

	exec.Command("tmux", "-L", socket, "kill-server").Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// The suite has to clear up after itself. These tests open real panes, which
// are tmux-backed, so the run needs both a socket name of its own and a
// directory to leave the socket file in — tmux does not remove it when a
// server is killed, and without this the suite piled dead sockets into the
// directory real sessions live in.
func TestTheSuiteLeavesNothingBehind(t *testing.T) {
	if s := os.Getenv(term.SocketEnv); s == "" || s == "omassh" {
		t.Fatalf("%s = %q — TestMain must point the tests at their own socket", term.SocketEnv, s)
	}
	dir := os.Getenv("TMUX_TMPDIR")
	if dir == "" {
		t.Fatal("TMUX_TMPDIR is unset — TestMain must give the run a directory of its own")
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		t.Fatalf("TMUX_TMPDIR = %q, which is not a directory: %v", dir, err)
	}
	if real := fmt.Sprintf("/tmp/tmux-%d", os.Getuid()); strings.HasPrefix(dir, real) {
		t.Errorf("TMUX_TMPDIR = %q is where real sockets live", dir)
	}
}
