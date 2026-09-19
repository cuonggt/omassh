package term_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/cuonggt/omassh/internal/sshx"
	"github.com/cuonggt/omassh/internal/term"
)

// countTmux puts a tmux in front of the real one that writes down each time it
// is run, and returns how many times that has been so far.
func countTmux(t *testing.T) func() int {
	t.Helper()
	real, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux is not installed")
	}
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	script := fmt.Sprintf("#!/bin/sh\necho \"$*\" >> %q\nexec %q \"$@\"\n", calls, real)
	if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() int {
		b, _ := os.ReadFile(calls)
		return strings.Count(string(b), "\n")
	}
}

// openPane starts a session on the in-process server and waits for its prompt.
func openPane(t *testing.T) *term.Pane {
	t.Helper()
	sshx.SetGlobalOptions([]string{
		"StrictHostKeyChecking=no", "UserKnownHostsFile=/dev/null", "IdentitiesOnly=yes",
	})
	t.Cleanup(func() { sshx.SetGlobalOptions(nil) })

	p, err := term.Open(testHost(t), 80, 24)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { p.Close() })
	waitFor(t, p, "$", 15*time.Second)
	return p
}

// Typing into a session and drawing it used to ask tmux something every time:
// twice for each key — whether the view was scrolled back, and to leave copy
// mode whether it was or not — and once for each frame, to write "scrolled"
// in the title, twenty frames a second. A launch is around seven milliseconds,
// and it ran on the event loop, so keys queued behind them: the pane was slow
// to type into by that much before the network had anything to do with it.
func TestTypingIntoASessionAndDrawingItDoNotRunTmux(t *testing.T) {
	calls := countTmux(t)
	p := openPane(t)
	if !p.Persistent() {
		t.Skip("the pane is not backed by tmux")
	}

	before := calls()
	// What the interface does for every key: see whether the view is
	// scrolled back, send the key, draw.
	for _, r := range "echo typed-$((40+2))" {
		p.ScrollOffset()
		p.SendKey(tea.KeyPressMsg{Code: r, Text: string(r)})
		p.Render()
	}
	p.SendKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	waitFor(t, p, "typed-42", 15*time.Second)

	if n := calls() - before; n != 0 {
		t.Errorf("typing a command and drawing it ran tmux %d times", n)
	}
}

// A pane says when it has something new to draw, which is what lets the
// interface draw the echo of a key as it comes back rather than at the next
// tick of a timer. And it says when there will be nothing more.
func TestAPaneSaysWhenItHasSomethingNewToDraw(t *testing.T) {
	p := openPane(t)

	// Whatever arrived before now counts as drawn.
	for drained := false; !drained; {
		select {
		case <-p.Changed():
		default:
			drained = true
		}
	}
	type_(p, "echo news-$((6*7))")
	p.SendKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	select {
	case <-p.Changed():
	case <-time.After(15 * time.Second):
		t.Fatal("output arrived and the pane never said so")
	}
	waitFor(t, p, "news-42", 15*time.Second)

	p.Close()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case _, open := <-p.Changed():
			if !open {
				return
			}
		case <-deadline:
			t.Fatal("the pane was closed and never said its output was over")
		}
	}
}
