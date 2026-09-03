package sshx

import (
	"errors"
	"io"
	"os/exec"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/cuonggt/omassh/internal/store"
)

// SessionEndedMsg is delivered once an interactive session exits and the TUI
// has taken the terminal back.
type SessionEndedMsg struct {
	HostID   string
	HostName string
	// Key identifies the host in session history; config-sourced hosts have
	// no id of their own, so this is not always HostID.
	Key      string
	ExitCode int
	Duration time.Duration
	Err      error
}

// timedCmd implements tea.ExecCommand so we can time the session precisely:
// Run is called by Bubble Tea after it has released the terminal, which is
// the moment the session actually starts.
//
// The SetStd* methods only fill in streams we haven't already set, which is
// what lets Bubble Tea hand the child the real TTY.
type timedCmd struct {
	*exec.Cmd
	start time.Time
	end   time.Time
}

func (c *timedCmd) Run() error {
	c.start = time.Now()
	err := c.Cmd.Run()
	c.end = time.Now()
	return err
}

func (c *timedCmd) SetStdin(r io.Reader) {
	if c.Stdin == nil {
		c.Stdin = r
	}
}

func (c *timedCmd) SetStdout(w io.Writer) {
	if c.Stdout == nil {
		c.Stdout = w
	}
}

func (c *timedCmd) SetStderr(w io.Writer) {
	if c.Stderr == nil {
		c.Stderr = w
	}
}

// Connect hands the real terminal to an OpenSSH session for h and restores
// the TUI when it exits.
//
// This is the load-bearing mechanism of the whole client: tea.Exec releases
// the terminal, gives the child genuine stdin/stdout/stderr, and restores
// afterwards. The session is real OpenSSH on a real TTY, so scrollback,
// SIGWINCH, mouse reporting and full-screen remote programs all behave
// exactly as they would without omassh in the picture.
func Connect(h store.Host) tea.Cmd {
	c := &timedCmd{Cmd: exec.Command("ssh", Build(h)...)}

	return tea.Exec(c, func(err error) tea.Msg {
		msg := SessionEndedMsg{
			HostID:   h.ID,
			HostName: h.Name,
			Key:      h.StatKey(),
			Duration: c.end.Sub(c.start),
		}
		// A non-zero exit is ordinary (the remote shell exited 1, the
		// connection dropped); it is session data, not an error in omassh.
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			msg.ExitCode = ee.ExitCode()
		} else {
			msg.Err = err
		}
		return msg
	})
}
