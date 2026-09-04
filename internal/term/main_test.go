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

	code := m.Run()

	exec.Command("tmux", "-L", socket, "kill-server").Run()
	os.Exit(code)
}
