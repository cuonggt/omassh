package term

import (
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/cuonggt/omassh/internal/store"
)

// A port forward is an ssh that connects and then does nothing visible, and
// the whole of its value is that it keeps running. So it runs where sessions
// run: a detached session on Omassh's own tmux server, which outlives the
// interface that started it.
//
// remain-on-exit keeps the session after ssh has gone, which is what lets a
// tunnel that stopped say why. Without it a forward that failed simply was not
// there, and "not running" is the one thing the interface could already see
// for itself.

// forwardPfx namespaces a tunnel inside the omassh- prefix that KillSession
// and LiveSessions already gate on.
//
// The second dash is what keeps the two kinds of session apart for good. A
// host's session is omassh-<name>-<id>, and sanitize trims dashes from the
// ends of the name, so nothing it produces can follow "omassh-" with another
// dash. Without it a host named "fwd" described exactly the same shape, and
// only the ids being different kept one from stopping the other.
const forwardPfx = sessionPfx + "-fwd-"

// forwardGrace is how long StartForward waits to see whether ssh stayed up.
//
// Long enough for a refused connection or a rejected key to come back from a
// host on the other side of the world, short enough not to hold the interface
// while it does. A tunnel that takes longer than this to fail is reported as
// started, and shows as stopped at the next refresh.
const forwardGrace = 1200 * time.Millisecond

// ForwardState is what tmux says has become of one tunnel.
type ForwardState struct {
	Running bool
	// Exit is ssh's exit code, once it has stopped.
	Exit int
}

// ForwardSessionName is the tmux session a rule's tunnel runs in.
//
// The rule's id and nothing else. A name built from the host's would be
// orphaned by renaming it: the tunnel would still be holding its port with
// nothing in the interface able to find it, and starting the rule again would
// fail on a port already in use, blaming something else.
func ForwardSessionName(f store.Forward) string { return forwardPfx + sanitize(f.ID) }

// ErrNoTmux is why forwarding is unavailable.
//
// Sessions degrade without tmux — they become ephemeral, which is visibly
// what they are. A tunnel has nothing to see, so one that quietly died with
// the interface would be indistinguishable from one that is working, which is
// worse than not offering it.
var ErrNoTmux = errors.New("port forwarding needs tmux: a tunnel that died with omassh would still look like one that is up")

// StartForward runs one rule's ssh in a detached session, and waits long
// enough to see whether it stayed up.
func StartForward(f store.Forward, sshArgs []string) error {
	if !TmuxAvailable() {
		return ErrNoTmux
	}
	name := ForwardSessionName(f)
	// Whatever was there is gone. A rule has one tunnel, and the stopped
	// session left by a previous attempt would refuse the name.
	if err := KillSession(name); err != nil {
		return err
	}
	conf, err := confPath()
	if err != nil {
		return err
	}

	args := []string{"-L", tmuxSocket(), "-f", conf, "new-session", "-d", "-s", name, "ssh"}
	args = append(args, sshArgs...)
	// Set in the same command sequence as the session is created, so the
	// option is in force before the server can act on the child exiting —
	// which for a port already taken is a matter of milliseconds.
	args = append(args, ";", "set-option", "-t", name, "remain-on-exit", "on")

	if out, err := exec.Command("tmux", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("start forward: %s", strings.TrimSpace(string(out)))
	}

	time.Sleep(forwardGrace)
	states, err := ForwardStates()
	if err != nil {
		return nil // it started; whether it is still up is the next refresh's answer
	}
	if st, ok := states[name]; ok && !st.Running {
		return errors.New(forwardFailure(name, st.Exit))
	}
	return nil
}

// StopForward ends a tunnel, and clears away a stopped one.
func StopForward(f store.Forward) error { return KillSession(ForwardSessionName(f)) }

// ForwardStates reports what has become of every tunnel, keyed by session
// name.
//
// Panes rather than sessions: remain-on-exit keeps the session after ssh has
// gone, so a session existing no longer means its tunnel is up. Only the pane
// knows that.
func ForwardStates() (map[string]ForwardState, error) {
	if !TmuxAvailable() {
		return nil, nil
	}
	format := strings.Join([]string{
		"#{session_name}", "#{pane_dead}", "#{pane_dead_status}",
	}, fieldSep)

	cmd := exec.Command("tmux", "-L", tmuxSocket(), "list-panes", "-a", "-F", format)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		// No server means no tunnels, which is ordinary. Anything else must
		// not be reported as "nothing is running".
		if noServer(stderr.String()) {
			return nil, nil
		}
		return nil, fmt.Errorf("list forwards: %s", strings.TrimSpace(stderr.String()))
	}

	states := map[string]ForwardState{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.Split(line, fieldSep)
		if len(parts) != 3 || !strings.HasPrefix(parts[0], forwardPfx) {
			continue
		}
		st := ForwardState{Running: parts[1] == "0"}
		if !st.Running {
			st.Exit, _ = strconv.Atoi(parts[2])
		}
		states[parts[0]] = st
	}
	return states, nil
}

// ForwardReason is the last thing ssh said in a stopped tunnel's pane.
//
// -J rejoins what the pane wrapped, so a message wider than the pane comes
// back whole rather than as its last fragment, and -S - reaches the
// scrollback, which is where the beginning of such a message ends up.
func ForwardReason(name string) string {
	out, err := exec.Command("tmux", "-L", tmuxSocket(),
		"capture-pane", "-p", "-J", "-S", "-", "-t", name).Output()
	if err != nil {
		return ""
	}
	lines := strings.Split(string(out), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		// tmux writes its own notice into a pane it is keeping. That the
		// tunnel stopped is already known; what is wanted is what ssh said
		// before it did.
		if line == "" || strings.HasPrefix(line, "Pane is dead") {
			continue
		}
		return line
	}
	return ""
}

// forwardFailure is what to say about a tunnel that did not stay up: what ssh
// wrote, since that names the cause, and the exit code when it wrote nothing.
func forwardFailure(name string, code int) string {
	if reason := ForwardReason(name); reason != "" {
		return reason
	}
	return fmt.Sprintf("ssh exited %d without saying why", code)
}
