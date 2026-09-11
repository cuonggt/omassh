package sshx

import (
	"errors"
	"io"
	"os/exec"
	"strings"
	"sync"
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
	// Detail is the last thing ssh wrote to stderr, kept because it is the
	// only place the reason for a failure appears. ssh prints it to the
	// terminal, which the interface then paints over, so it was visible only
	// after quitting — every failure read as "exited 255" until then.
	Detail string
}

// NeverConnected reports that ssh gave up rather than reaching the host.
//
// 255 is the status OpenSSH exits with for its own failures — a refused
// connection, a rejected key, a host key that did not match — as distinct from
// the remote command's status, which is anything else. A remote command can
// exit 255 of its own accord, and one that does is a session counted as none;
// that is the cheaper mistake by far, and the rarer.
func (m SessionEndedMsg) NeverConnected() bool { return m.Err != nil || m.ExitCode == 255 }

// ConnectionFailed is the code ssh exits with when the fault is its own:
// refused, timed out, rejected, a host key that changed. A remote command
// exiting non-zero is its own business, and nothing on its stderr says
// anything about the connection.
const ConnectionFailed = 255

// tailBytes is how much of a session's stderr is kept. Only the end of it can
// be a diagnosis, and an interactive session may write a great deal.
const tailBytes = 4096

// tail keeps the last of what is written through it.
type tail struct {
	mu  sync.Mutex
	buf []byte
}

func (t *tail) Write(b []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, b...)
	if len(t.buf) > tailBytes {
		t.buf = t.buf[len(t.buf)-tailBytes:]
	}
	return len(b), nil
}

// last is the final non-empty line, which is where ssh puts the reason: the
// lines before it are warnings and banners it printed on the way.
func (t *tail) last() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	lines := strings.Split(string(t.buf), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if s := strings.TrimSpace(lines[i]); s != "" {
			return s
		}
	}
	return ""
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
	said  tail
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

// SetStderr keeps a copy of what the child says on its way to the terminal.
//
// The terminal still gets everything, unchanged; this only listens in, so that
// a failure can be reported here instead of scrolling past behind the
// interface. It does mean the child's stderr is a pipe rather than the
// inherited file — ssh's diagnostics do not depend on that, and its passphrase
// and host-key prompts go to /dev/tty rather than stderr.
func (c *timedCmd) SetStderr(w io.Writer) {
	if c.Stderr == nil {
		c.Stderr = io.MultiWriter(w, &c.said)
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

	return tea.Exec(c, func(err error) tea.Msg { return ended(h, c, err) })
}

// ended turns how the child finished into what the interface reports. It is a
// function of its own so that it can be exercised without a terminal to hand
// over, which is the only part of this path a test can reach.
func ended(h store.Host, c *timedCmd, err error) SessionEndedMsg {
	msg := SessionEndedMsg{
		HostID:   h.ID,
		HostName: h.Name,
		Key:      h.StatKey(),
		Duration: c.end.Sub(c.start),
	}
	// A non-zero exit is ordinary (the remote shell exited 1, the connection
	// dropped); it is session data, not an error in omassh.
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		msg.ExitCode = ee.ExitCode()
		if msg.ExitCode == ConnectionFailed {
			msg.Detail = c.said.last()
		}
		return msg
	}
	msg.Err = err
	return msg
}
