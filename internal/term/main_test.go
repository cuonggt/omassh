package term_test

import (
	"fmt"
	"os"
	"os/exec"
	"testing"

	"github.com/cuonggt/omassh/internal/term"
)

// TestMain puts every tmux server these tests touch on a socket of its own.
//
// The tests kill servers wholesale, and they used to do it on the socket real
// sessions live on, so running the suite destroyed whatever the developer was
// working in — the opposite of the promise persistent sessions make. The name
// carries the pid because `go test ./...` runs packages concurrently, and two
// binaries sharing one socket would kill each other's fixtures.
func TestMain(m *testing.M) {
	socket := fmt.Sprintf("omassh-test-term-%d", os.Getpid())
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
