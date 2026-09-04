package term

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cuonggt/omassh/internal/store"
)

// Sessions are backed by tmux so they outlive Omassh. A pty whose master fd
// belongs to this process dies with it; handing ownership to a tmux server
// means closing the UI detaches rather than disconnects.
//
// This is the same trade Omassh makes everywhere else: drive the real tool
// rather than reimplement it. A private server socket keeps these sessions out
// of the user's own tmux, so `tmux ls` in their shell is unaffected.
const (
	defaultSocket = "omassh"
	sessionPfx    = "omassh-"
	attachGrace   = 3 * time.Second
)

// SocketEnv overrides the server socket. The tests set it so they can kill a
// server wholesale without destroying the sessions someone is actually working
// in — the two used to share one socket, which made `go test ./...` wipe every
// live session on the machine.
const SocketEnv = "OMASSH_TMUX_SOCKET"

func tmuxSocket() string {
	if s := os.Getenv(SocketEnv); s != "" {
		return s
	}
	return defaultSocket
}

// TmuxAvailable reports whether persistent sessions are possible.
func TmuxAvailable() bool {
	_, err := exec.LookPath("tmux")
	return err == nil
}

// SessionName is the tmux session for a host: stable across restarts so
// reconnecting reattaches, and readable in `tmux -L omassh ls`.
func SessionName(h store.Host) string {
	return sessionPfx + sanitize(h.Name) + "-" + sanitize(h.StatKey())
}

// sanitize strips what tmux's target syntax treats specially.
func sanitize(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "host"
	}
	return out
}

// confPath writes the server's configuration, which is read only when the
// server starts. The status bar is off because the pane is already framed by
// Omassh, and a second one would waste a row and confuse the geometry.
func confPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir = filepath.Join(dir, "omassh")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	p := filepath.Join(dir, "tmux.conf")
	body := "# Written by Omassh for its private tmux server.\n" +
		"set -g status off\n" +
		"set -g history-limit 10000\n" +
		"set -g escape-time 10\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		return "", err
	}
	return p, nil
}

// tmuxCommand wraps an ssh invocation in a persistent tmux session, attaching
// to the existing one if it is already running.
func tmuxCommand(h store.Host, sshArgs []string) (*exec.Cmd, string, error) {
	conf, err := confPath()
	if err != nil {
		return nil, "", err
	}
	name := SessionName(h)
	args := []string{"-L", tmuxSocket(), "-f", conf, "new-session", "-A", "-s", name, "ssh"}
	args = append(args, sshArgs...)
	return exec.Command("tmux", args...), name, nil
}

// LiveSession is a persistent session as tmux reports it.
type LiveSession struct {
	Name     string
	Attached bool
	Created  time.Time
}

// fieldSep separates fields in tmux's format output. Not a tab: tmux rewrites
// tabs in format output to underscores, which silently collapses every field
// into one. A pipe survives, and cannot occur in a sanitised session name.
const fieldSep = "|"

// LiveSessions lists Omassh's persistent sessions.
func LiveSessions() ([]LiveSession, error) {
	if !TmuxAvailable() {
		return nil, nil
	}
	format := strings.Join([]string{
		"#{session_name}", "#{session_attached}", "#{session_created}",
	}, fieldSep)

	cmd := exec.Command("tmux", "-L", tmuxSocket(), "ls", "-F", format)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		// No server means no sessions, which is ordinary. Anything else is a
		// real failure and must not be reported as "nothing running".
		if strings.Contains(stderr.String(), "no server running") {
			return nil, nil
		}
		return nil, fmt.Errorf("list sessions: %s", strings.TrimSpace(stderr.String()))
	}

	var sessions []LiveSession
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.Split(line, fieldSep)
		if len(parts) != 3 || !strings.HasPrefix(parts[0], sessionPfx) {
			continue
		}
		s := LiveSession{Name: parts[0], Attached: parts[1] != "0"}
		if secs, err := strconv.ParseInt(parts[2], 10, 64); err == nil {
			s.Created = time.Unix(secs, 0)
		}
		sessions = append(sessions, s)
	}
	return sessions, nil
}

// HasLiveSession reports whether a host has a session waiting to be reattached.
func HasLiveSession(h store.Host) bool {
	want := SessionName(h)
	live, _ := LiveSessions()
	for _, s := range live {
		if s.Name == want {
			return true
		}
	}
	return false
}

// KillSession ends a persistent session and everything running in it.
func KillSession(name string) error {
	if !strings.HasPrefix(name, sessionPfx) {
		return fmt.Errorf("refusing to kill %q: not an omassh session", name)
	}
	out, err := exec.Command("tmux", "-L", tmuxSocket(), "kill-session", "-t", name).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		// Already gone is the outcome asked for, not a failure.
		if strings.Contains(msg, "find session") || strings.Contains(msg, "no server running") {
			return nil
		}
		return fmt.Errorf("kill session: %s", msg)
	}
	return nil
}

// Scrolling a tmux-backed pane means driving tmux's copy mode, not our own
// buffer. tmux redraws the whole screen on every change, so nothing ever
// scrolls off the emulator and its scrollback stays empty — the history lives
// in the tmux server, which is also where it survives a restart.
func tmuxCopyScroll(session string, up bool) error {
	cmd := "page-up"
	if !up {
		cmd = "page-down"
	}
	// Entering copy mode is idempotent; -e leaves it automatically when
	// scrolled back to the bottom.
	exec.Command("tmux", "-L", tmuxSocket(), "copy-mode", "-e", "-t", session).Run()
	return exec.Command("tmux", "-L", tmuxSocket(), "send-keys", "-t", session, "-X", cmd).Run()
}

// tmuxCopyCancel leaves copy mode, returning to the live view.
func tmuxCopyCancel(session string) error {
	return exec.Command("tmux", "-L", tmuxSocket(), "send-keys", "-t", session, "-X", "cancel").Run()
}

// tmuxScrollPosition reports how far back the pane is scrolled, and how much
// history exists.
func tmuxScrollPosition(session string) (offset, available int) {
	out, err := exec.Command("tmux", "-L", tmuxSocket(), "display-message", "-p", "-t", session,
		"-F", "#{scroll_position}"+fieldSep+"#{history_size}").Output()
	if err != nil {
		return 0, 0
	}
	parts := strings.Split(strings.TrimSpace(string(out)), fieldSep)
	if len(parts) != 2 {
		return 0, 0
	}
	offset, _ = strconv.Atoi(parts[0])
	available, _ = strconv.Atoi(parts[1])
	return offset, available
}
