package sftpx

import (
	"errors"
	"strings"
	"testing"

	"github.com/cuonggt/omassh/internal/sshx"
	"github.com/cuonggt/omassh/internal/store"
)

// ssh's half of a refusal is long — "cuonggt@10.0.0.1: Permission denied
// (publickey,keyboard-interactive)." is 70 cells before omassh adds anything —
// and the status bar is one row that cannot wrap. So the half omassh writes
// has to be short enough that the whole thing still fits a common terminal,
// because what gets cut is the end, which is the part saying what to do.
func TestTheRefusalRemedyLeavesRoomForSshsOwnWords(t *testing.T) {
	const sshSaid = "cuonggt@10.0.0.1: Permission denied (publickey,keyboard-interactive)."
	got := connectError(errors.New("EOF"), sshSaid).Error()

	if !strings.Contains(got, "ssh-add") {
		t.Errorf("the remedy is missing: %q", got)
	}
	if !strings.Contains(got, "Permission denied") {
		t.Errorf("ssh's own words were dropped: %q", got)
	}
	// 100 columns is an ordinary terminal; the message has to survive one.
	if n := len([]rune(got)); n > 100 {
		t.Errorf("the message is %d cells, which a 100-column bar would cut: %q", n, got)
	}
}

// Only the last line of ssh's chatter is the reason; the rest is warnings on
// the way in.
func TestOnlyTheLastLineOfSshsOutputIsTheReason(t *testing.T) {
	stderr := "Warning: Permanently added '[10.0.0.1]:22' (ED25519) to the list of known hosts.\n" +
		"cuonggt@10.0.0.1: Permission denied (publickey)."
	got := connectError(errors.New("EOF"), stderr).Error()

	if strings.Contains(got, "Warning: Permanently added") {
		t.Errorf("a startup warning was reported as the reason: %q", got)
	}
	if !strings.Contains(got, "Permission denied") {
		t.Errorf("the reason was lost: %q", got)
	}
}

// A host whose sshd serves no sftp says so, rather than naming a channel.
//
// ssh answers this with "subsystem request failed on channel 0", which went
// straight to the bar: the channel is bookkeeping inside a protocol the person
// pressing s never asked about, and the line never says that the far side has
// no sftp to give, nor that the fix is on that machine and not this one.
func TestAHostWithNoSftpSaysSoRatherThanNamingAChannel(t *testing.T) {
	got := connectError(errors.New("EOF"), "subsystem request failed on channel 0").Error()

	if strings.Contains(got, "channel") {
		t.Errorf("the channel is still in the message: %q", got)
	}
	if !strings.Contains(got, "sftp is not enabled") {
		t.Errorf("the message does not say what is wrong: %q", got)
	}
	if !strings.Contains(got, "sshd_config") {
		t.Errorf("the message does not say what to do about it: %q", got)
	}
	// One row of a 100-column bar, like the refusal beside it.
	if n := len([]rune(got)); n > 100 {
		t.Errorf("the message is %d cells, which a 100-column bar would cut: %q", n, got)
	}
}

// The answer counts wherever ssh puts it. Taking the last line alone would let
// anything printed afterwards — a connection closing — bury it.
func TestTheMissingSubsystemIsFoundBehindALaterLine(t *testing.T) {
	stderr := "subsystem request failed on channel 0\n" +
		"Connection to 10.0.0.1 closed.\n"
	got := connectError(errors.New("EOF"), stderr).Error()

	if !strings.Contains(got, "sftp is not enabled") {
		t.Errorf("a later line hid the reason: %q", got)
	}
}

// BatchMode is what stops an sftp connection stopping at a prompt nobody can
// answer: the child's stdin is carrying the protocol. ssh keeps the first
// value it is given, so a single `-o BatchMode=no` handed to omassh itself
// was enough to undo it — the same way a forward could be undone before that
// was guarded.
func TestSFTPForcesBatchModeBeyondTheReachOfAnOption(t *testing.T) {
	sshx.SetGlobalOptions([]string{"BatchMode=no", "LogLevel=DEBUG"})
	defer sshx.SetGlobalOptions(nil)

	got := strings.Join(connectArgs(store.Host{Name: "web", Addr: "10.0.0.1"}, nil), " ")
	yes, no := strings.Index(got, "BatchMode=yes"), strings.Index(got, "BatchMode=no")
	if yes < 0 || no < 0 || yes > no {
		t.Errorf("BatchMode=yes has to come first:\n  %s", got)
	}
	// A preference stays a preference.
	if d, e := strings.Index(got, "LogLevel=DEBUG"), strings.Index(got, "LogLevel=ERROR"); d < 0 || e < 0 || d > e {
		t.Errorf("LogLevel should still be raisable from the command line:\n  %s", got)
	}
}
