package ui

import (
	"fmt"
	"os"
	"os/exec"
	"testing"

	"github.com/cuonggt/omassh/internal/term"
)

// TestMain isolates the tmux server, for the same reason internal/term does:
// these tests open real panes, which are tmux-backed, and they must not land
// among the sessions someone is actually using.
func TestMain(m *testing.M) {
	socket := fmt.Sprintf("omassh-test-ui-%d", os.Getpid())
	os.Setenv(term.SocketEnv, socket)

	code := m.Run()

	exec.Command("tmux", "-L", socket, "kill-server").Run()
	os.Exit(code)
}
