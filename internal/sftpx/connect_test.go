package sftpx

import (
	"errors"
	"strings"
	"testing"
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
