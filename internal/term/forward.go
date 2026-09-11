package term

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/cuonggt/omassh/internal/sshx"
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
	// Signal names what killed it, when something did. tmux reports a signal
	// death with an empty exit status, so reading the status alone made a
	// tunnel the system killed — out of memory, a stray pkill — read exactly
	// like one stopped on purpose, which is the distinction the marks exist
	// to draw.
	Signal string
	// Args identifies the invocation actually running, which is not always
	// the one the rule now describes. Empty for a tunnel started before this
	// was recorded, which must not be read as a mismatch.
	Args string
}

// Failed reports whether a tunnel stopped because something went wrong, as
// opposed to being stopped.
func (s ForwardState) Failed() bool {
	return !s.Running && (s.Exit != 0 || s.Signal != "")
}

// argsOption is where a tunnel keeps the fingerprint of what it was started
// with. A tmux pane option: it belongs to the running thing rather than to the
// store, and it goes when the tunnel does.
const argsOption = "@omassh-args"

// ForwardFingerprint identifies one ssh invocation.
//
// A tunnel carries whatever it was started with, and nothing about editing the
// rule afterwards reaches the process already running. The session is named by
// the rule's id, so it goes on being found and reported as up — against a rule
// that now says something else entirely. Recording what is actually running is
// what lets the two be compared.
func ForwardFingerprint(sshArgs []string) string {
	sum := sha256.Sum256([]byte(strings.Join(sshArgs, "\x00")))
	return hex.EncodeToString(sum[:8])
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
	// Whatever was there goes first. A rule has one tunnel, the stopped
	// session left by a previous attempt would refuse the name, and a running
	// one is holding the very port the check below asks for — so restarting a
	// tunnel to move it failed on the port its own predecessor had not yet
	// let go of, which is the one case restarting exists for.
	if err := KillSession(name); err != nil {
		return err
	}
	if err := waitToListen(f); err != nil {
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
	// What is running, so the interface can tell it from what the rule says.
	args = append(args, ";", "set-option", "-p", "-t", name, argsOption, ForwardFingerprint(sshArgs))

	if out, err := exec.Command("tmux", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("start forward: %s", strings.TrimSpace(string(out)))
	}

	time.Sleep(forwardGrace)
	states, err := ForwardStates()
	if err != nil {
		return nil // it started; whether it is still up is the next refresh's answer
	}
	if st, ok := states[name]; ok && !st.Running {
		return errors.New(forwardFailure(name, st))
	}
	return nil
}

// portRelease is how long the near end is given to come free.
//
// The port belongs to the ssh that was just killed, and the kernel releases it
// when that process goes rather than when tmux is done asking. A tenth of a
// second is the usual answer; the rest of this is for a machine under load.
const portRelease = time.Second

// waitToListen is the pre-flight: ssh binds the port inside a session nobody
// watches, so a port that cannot be taken is worth finding out about here,
// where it can be said in words.
func waitToListen(f store.Forward) error {
	deadline := time.Now().Add(portRelease)
	for {
		err := sshx.ListenAvailable(f)
		if err == nil || time.Now().After(deadline) {
			return err
		}
		time.Sleep(50 * time.Millisecond)
	}
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
		"#{pane_dead_signal}", "#{" + argsOption + "}",
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
		if len(parts) != 5 || !strings.HasPrefix(parts[0], forwardPfx) {
			continue
		}
		st := ForwardState{Running: parts[1] == "0", Args: parts[4]}
		if !st.Running {
			st.Exit, _ = strconv.Atoi(parts[2])
			st.Signal = parts[3]
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
	var lastResort string
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		// tmux writes its own notice into a pane it is keeping. That the
		// tunnel stopped is already known; what is wanted is what ssh said
		// before it did.
		if line == "" || strings.HasPrefix(line, "Pane is dead") {
			continue
		}
		if aboutTheProxy(line) {
			if lastResort == "" {
				lastResort = line
			}
			continue
		}
		return line
	}
	return lastResort
}

// aboutTheProxy is ssh noticing that its ProxyCommand went quiet, which is
// never why anything failed.
//
// A tunnel through a jump host is two sshs, and it is the inner one that
// writes the cause — a refused key, a changed host key, a hop that is not
// listening — into the same pane. ssh then adds a line of its own about the
// pipe closing, so the *last* line of a tunnel that died at the hop named
// nothing at all: "UNKNOWN port 65535" is ssh's placeholder for a connection
// with no socket behind it. What it replaced was "Permission denied", one
// line up — the line that would have said to add the key.
//
// Only that placeholder form is skipped. "Connection closed by 10.0.0.5 port
// 22" is a host hanging up, which is a cause and says whose. And if the
// placeholder is all there is, it is still reported: an unhelpful sentence
// beats an empty one.
func aboutTheProxy(line string) bool { return strings.Contains(line, "UNKNOWN port 65535") }

// forwardFailure is what to say about a tunnel that did not stay up: what ssh
// wrote, since that names the cause, and failing that how it ended.
func forwardFailure(name string, st ForwardState) string {
	// A signal comes first, because it is the whole story: ssh wrote nothing
	// on its way out, so whatever is in the pane is left over from when it
	// started. Usually that is the known-hosts warning of a first connection,
	// and reporting it would name a line from minutes ago as the cause.
	if st.Signal != "" {
		return "ssh was killed (" + st.Signal + ")"
	}
	if reason := ForwardReason(name); reason != "" {
		return reason
	}
	return fmt.Sprintf("ssh exited %d without saying why", st.Exit)
}

// FailureReason is what to say about a stopped tunnel, for the interface.
func FailureReason(name string, st ForwardState) string { return forwardFailure(name, st) }
