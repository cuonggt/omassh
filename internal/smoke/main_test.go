package smoke

import (
	"fmt"
	"os"
	"os/exec"
	"testing"
)

// socket is the tmux server these tests drive omassh on, and driveSocket the
// one they drive the terminal with. Both carry the pid, because `go test ./...`
// runs packages at the same time and two binaries sharing a socket would kill
// each other's fixtures.
var (
	socket      = fmt.Sprintf("omassh-smoke-%d", os.Getpid())
	driveSocket = fmt.Sprintf("omassh-drive-%d", os.Getpid())
)

// TestMain isolates tmux, for the reason internal/term's does: the suite kills
// servers wholesale, and once did it on the socket real sessions live on.
//
// Under /tmp rather than the default temporary directory, because a unix
// socket path is capped near 104 bytes and macOS puts TMPDIR deep enough under
// /var/folders that the socket would sit on the edge of it.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("/tmp", "omassh-smoke-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "isolating tmux for the smoke tests:", err)
		os.Exit(1)
	}
	os.Setenv("TMUX_TMPDIR", dir)

	code := m.Run()

	for _, s := range []string{socket, driveSocket} {
		exec.Command("tmux", "-L", s, "kill-server").Run()
	}
	os.RemoveAll(dir)
	os.Exit(code)
}
