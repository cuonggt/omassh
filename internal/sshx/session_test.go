package sshx

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"

	"github.com/cuonggt/omassh/internal/store"
)

// ssh puts the reason on the last line; what comes before it is warnings and
// banners printed on the way.
func TestTheTailKeepsTheLastThingSaid(t *testing.T) {
	cases := map[string]struct{ wrote, want string }{
		"one line": {"ssh: connect to host h port 1: Connection refused\n",
			"ssh: connect to host h port 1: Connection refused"},
		"after a warning": {"Warning: Permanently added 'h' to the list of known hosts.\n" +
			"nobody@h: Permission denied (publickey).\n",
			"nobody@h: Permission denied (publickey)."},
		"trailing blank lines": {"Host key verification failed.\n\n\n", "Host key verification failed."},
		"no newline at all":    {"Connection timed out", "Connection timed out"},
		"nothing":              {"", ""},
		"only blanks":          {"\n \n\t\n", ""},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			var tl tail
			tl.Write([]byte(c.wrote))
			if got := tl.last(); got != c.want {
				t.Errorf("last() = %q, want %q", got, c.want)
			}
		})
	}
}

// An interactive session can write a great deal to stderr, and only the end of
// it can be a diagnosis.
func TestTheTailIsBounded(t *testing.T) {
	var tl tail
	for range 200 {
		tl.Write([]byte(strings.Repeat("x", 1000) + "\n"))
	}
	if len(tl.buf) > tailBytes {
		t.Errorf("kept %d bytes of stderr, want no more than %d", len(tl.buf), tailBytes)
	}
	tl.Write([]byte("\nthe last word\n"))
	if got := tl.last(); got != "the last word" {
		t.Errorf("last() = %q after a great deal of output", got)
	}
}

// Writing is what the terminal sees too, so it must pass everything through
// unchanged and report the full length.
func TestTheTailPassesEverythingOn(t *testing.T) {
	var tl tail
	body := []byte("some output\n")
	n, err := tl.Write(body)
	if err != nil || n != len(body) {
		t.Errorf("Write() = %d, %v, want %d, nil", n, err, len(body))
	}
}

// run drives a child the way tea.Exec does: the terminal's writers go in, the
// child runs, and how it finished comes back.
func run(t *testing.T, script string) (SessionEndedMsg, string) {
	t.Helper()
	c := &timedCmd{Cmd: exec.Command("sh", "-c", script)}
	// Separate buffers: exec gives each stream its own goroutine, and one
	// bytes.Buffer shared between them is a race that loses what it is asked
	// to hold.
	var out, terminal bytes.Buffer
	c.SetStdin(strings.NewReader(""))
	c.SetStdout(&out)
	c.SetStderr(&terminal)
	err := c.Run()
	return ended(store.Host{ID: "h1", Name: "web"}, c, err), terminal.String()
}

// ssh writes the reason to the terminal, which the interface paints over. It
// is kept as well, so a failure can say more than its exit code.
func TestAFailedConnectionKeepsWhatSSHSaid(t *testing.T) {
	msg, terminal := run(t, `echo "Warning: Permanently added 'h'" >&2
echo "deploy@h: Permission denied (publickey)." >&2
exit 255`)

	// The terminal still gets everything, unchanged.
	if !strings.Contains(terminal, "Permission denied (publickey)") {
		t.Errorf("the terminal no longer sees stderr: %q", terminal)
	}
	if msg.ExitCode != ConnectionFailed {
		t.Fatalf("exit code = %d, want %d", msg.ExitCode, ConnectionFailed)
	}
	if msg.Detail != "deploy@h: Permission denied (publickey)." {
		t.Errorf("detail = %q, want the last thing ssh said", msg.Detail)
	}
}

// A remote command exiting non-zero is its own business; its stderr says
// nothing about the connection.
func TestAnOrdinaryExitKeepsNoDetail(t *testing.T) {
	msg, _ := run(t, `echo "make: *** [build] Error 1" >&2; exit 2`)
	if msg.ExitCode != 2 {
		t.Fatalf("exit code = %d, want 2", msg.ExitCode)
	}
	if msg.Detail != "" {
		t.Errorf("detail = %q, want nothing for a remote command's own exit", msg.Detail)
	}
}

// A session that simply ends carries neither.
func TestASessionThatEndsWellSaysNothingExtra(t *testing.T) {
	msg, _ := run(t, `echo hello; exit 0`)
	if msg.ExitCode != 0 || msg.Err != nil || msg.Detail != "" {
		t.Errorf("got %+v, want a clean end", msg)
	}
	if msg.Duration <= 0 {
		t.Errorf("duration = %v, want the session to have been timed", msg.Duration)
	}
}
