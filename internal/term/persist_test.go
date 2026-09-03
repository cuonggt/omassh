package term_test

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/cuonggt/omassh/internal/sshx"
	"github.com/cuonggt/omassh/internal/store"
	"github.com/cuonggt/omassh/internal/term"
)

// killServer removes every session this test created, so a test run never
// leaves sessions behind on the developer's machine.
func killServer(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		exec.Command("tmux", "-L", "omassh", "kill-server").Run()
	})
}

// The whole point: a session must outlive the pane that opened it, and
// reconnecting must return to it rather than starting again.
func TestSessionSurvivesThePaneAndReattaches(t *testing.T) {
	if !term.TmuxAvailable() {
		t.Skip("tmux not installed; sessions are ephemeral")
	}
	killServer(t)
	sshx.SetGlobalOptions([]string{
		"StrictHostKeyChecking=no", "UserKnownHostsFile=/dev/null", "IdentitiesOnly=yes",
	})
	t.Cleanup(func() { sshx.SetGlobalOptions(nil) })

	h := testHost(t)
	h.ID, h.Name = "persist-1", "persistbox"

	if term.HasLiveSession(h) {
		t.Fatal("a session exists before anything opened one")
	}

	p, err := term.Open(h, 80, 20)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !p.Persistent() {
		t.Fatal("pane is not persistent though tmux is available")
	}

	// Leave a distinctive mark in the session's scrollback.
	type_(p, "echo MARKER-abc123")
	p.SendKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	waitFor(t, p, "MARKER-abc123", 20*time.Second)

	// Closing the pane detaches; the session keeps running.
	if err := p.Close(); err != nil {
		t.Logf("Close: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !term.HasLiveSession(h) {
		time.Sleep(100 * time.Millisecond)
	}
	if !term.HasLiveSession(h) {
		live, _ := term.LiveSessions()
		t.Fatalf("session did not survive closing the pane; live sessions: %+v", live)
	}

	t.Run("reconnecting reattaches with the screen intact", func(t *testing.T) {
		p2, err := term.Open(h, 80, 20)
		if err != nil {
			t.Fatalf("reopen: %v", err)
		}
		t.Cleanup(func() { p2.Close() })
		// The mark from before is still on screen, so this is the same session
		// rather than a fresh one.
		waitFor(t, p2, "MARKER-abc123", 20*time.Second)
	})

	t.Run("a second host gets its own session", func(t *testing.T) {
		other := h
		other.ID, other.Name = "persist-2", "otherbox"
		if term.SessionName(other) == term.SessionName(h) {
			t.Fatal("two hosts share a session name")
		}
	})

	t.Run("killing ends it for good", func(t *testing.T) {
		p3, err := term.Open(h, 80, 20)
		if err != nil {
			t.Fatal(err)
		}
		if err := p3.Kill(); err != nil {
			t.Fatalf("Kill: %v", err)
		}
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) && term.HasLiveSession(h) {
			time.Sleep(100 * time.Millisecond)
		}
		if term.HasLiveSession(h) {
			t.Error("session survived Kill")
		}
	})
}

func TestSessionNameIsStableAndSafe(t *testing.T) {
	h := store.Host{ID: "abc123", Name: "web-01.prod:eu"}
	got := term.SessionName(h)

	if got != term.SessionName(h) {
		t.Error("SessionName is not stable")
	}
	// tmux treats "." and ":" as target syntax; they must not survive.
	if strings.ContainsAny(got, ".: ") {
		t.Errorf("SessionName %q contains characters tmux treats specially", got)
	}
	if !strings.HasPrefix(got, "omassh-") {
		t.Errorf("SessionName %q lacks the omassh prefix", got)
	}
	// A config-sourced host has no id of its own but must still be distinct.
	cfg := store.Host{Name: "web-01.prod:eu", Source: store.SourceSSHConfig}
	if term.SessionName(cfg) == got {
		t.Error("a config host collides with a stored host of the same name")
	}
}

// Killing must never reach a session Omassh does not own.
func TestKillRefusesForeignSessions(t *testing.T) {
	if err := term.KillSession("my-important-work"); err == nil {
		t.Error("KillSession accepted a session outside the omassh namespace")
	}
}

// tmux rewrites tabs in format output to underscores, which would collapse
// every field into one and make every session invisible.
func TestLiveSessionsParsesTmuxOutput(t *testing.T) {
	if !term.TmuxAvailable() {
		t.Skip("tmux not installed")
	}
	killServer(t)
	exec.Command("tmux", "-L", "omassh", "new-session", "-d", "-s", "omassh-parse-test", "sleep", "60").Run()

	live, err := term.LiveSessions()
	if err != nil {
		t.Fatalf("LiveSessions: %v", err)
	}
	var found *term.LiveSession
	for i := range live {
		if live[i].Name == "omassh-parse-test" {
			found = &live[i]
		}
	}
	if found == nil {
		t.Fatalf("session not found in %+v", live)
	}
	if found.Attached {
		t.Error("session reported as attached with no client")
	}
	if found.Created.IsZero() {
		t.Error("creation time did not parse — the fields ran together")
	}
}

// "No server running" is ordinary; anything else must not be reported as
// "nothing is running", which would hide live sessions.
func TestLiveSessionsWithNoServer(t *testing.T) {
	if !term.TmuxAvailable() {
		t.Skip("tmux not installed")
	}
	exec.Command("tmux", "-L", "omassh", "kill-server").Run()
	live, err := term.LiveSessions()
	if err != nil {
		t.Errorf("no server should not be an error, got %v", err)
	}
	if len(live) != 0 {
		t.Errorf("got %+v, want none", live)
	}
}
