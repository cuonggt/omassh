package term

import (
	"strings"
	"testing"

	"github.com/cuonggt/omassh/internal/store"
)

// A session's environment has to be built into the command tmux records, not
// set on the process that asks tmux to make it.
//
// The pane builds a plain ssh command first and replaces it with this one when
// tmux is available, so anything set on that first Cmd is discarded — which is
// how a password credential reached the interactive pane with no SSH_ASKPASS
// and sat at a prompt nobody could answer. The session outlives omassh anyway,
// so the environment has to travel in what tmux keeps.
func TestATmuxSessionCarriesTheEnvironmentInItsCommand(t *testing.T) {
	h := store.Host{Name: "switch", Addr: "10.0.9.1"}
	env := []string{"SSH_ASKPASS=/path/to/omassh", "SSH_ASKPASS_REQUIRE=force"}

	cmd, _, err := tmuxCommand(h, []string{"-p", "22", "admin@10.0.9.1"}, env)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(cmd.Args, " ")

	for _, want := range env {
		if !strings.Contains(got, want) {
			t.Errorf("%q is not in the command tmux was given:\n  %s", want, got)
		}
	}
	// Through env(1), and before ssh, or it is an argument to ssh instead of
	// the environment ssh runs in.
	e, s := strings.Index(got, " env "), strings.Index(got, " ssh ")
	if e < 0 || s < 0 || e > s {
		t.Errorf("env has to come before ssh:\n  %s", got)
	}
}

// With nothing to carry it is the command it always was, so every session that
// is not a password credential is unchanged.
func TestATmuxSessionWithNoEnvironmentIsUnchanged(t *testing.T) {
	h := store.Host{Name: "web", Addr: "10.0.1.1"}

	cmd, _, err := tmuxCommand(h, []string{"admin@10.0.1.1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(cmd.Args, " "); strings.Contains(got, " env ") {
		t.Errorf("an env prefix appeared with nothing to put in it:\n  %s", got)
	}
}
