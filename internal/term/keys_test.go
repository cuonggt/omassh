package term_test

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

var (
	ctrlB = tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl}
	enter = tea.KeyPressMsg{Code: tea.KeyEnter}
)

// sendCtrlBToTheFarSide runs a program that prints the bytes it is given,
// hands it ctrl+b, and waits to see them printed.
//
// od is started behind a marker, so what is typed is known to reach it rather
// than the shell's line editor: the command line reads rea""dy, and only its
// output reads ready. 02 0a is ctrl+b and the newline after it; had tmux kept
// the ctrl+b for itself, od would see nothing but the end of its input.
func sendCtrlBToTheFarSide(t *testing.T, p interface {
	SendKey(tea.KeyPressMsg)
	Render() string
}) {
	t.Helper()
	for _, r := range `echo rea""dy; od -A n -t x1` {
		p.SendKey(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	p.SendKey(enter)
	waitForScreen(t, p, "ready", 15*time.Second)
	p.SendKey(ctrlB)
	p.SendKey(enter)
	p.SendKey(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl}) // end od's input
	waitForScreen(t, p, "02 0a", 15*time.Second)
}

// waitForScreen is waitFor for anything that can be rendered, with runs of
// spaces counted as one: od pads its columns differently from one system to
// the next.
func waitForScreen(t *testing.T, p interface{ Render() string }, want string, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	var last string
	for time.Now().Before(deadline) {
		last = visible(p.Render())
		if strings.Contains(strings.Join(strings.Fields(last), " "), want) {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("the pane never showed %q; the screen was:\n%s", want, last)
}

// Every key typed into a session belongs to it, ctrl+b included. omassh's
// tmux server kept tmux's own prefix, so ctrl+b was taken as the start of a
// tmux command and never reached the far side: not readline's
// back-a-character, not vim's page-up, not a tmux running there.
func TestCtrlBReachesTheProgramOnTheFarSide(t *testing.T) {
	p := openPane(t)
	if !p.Persistent() {
		t.Skip("the pane is not backed by tmux")
	}
	sendCtrlBToTheFarSide(t, p)
}

// ctrl+b then d was tmux's detach. It took the pane's own client off the
// session, so the pane reported as ended a session that was still running on
// the server — and anything typed next went nowhere.
func TestCtrlBThenDDoesNotDetachTheSession(t *testing.T) {
	p := openPane(t)
	if !p.Persistent() {
		t.Skip("the pane is not backed by tmux")
	}
	p.SendKey(ctrlB)
	p.SendKey(tea.KeyPressMsg{Code: 'd', Text: "d"})
	p.SendKey(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl}) // clear what reached the shell
	type_(p, "echo still-$((20+22))")
	p.SendKey(enter)
	waitFor(t, p, "still-42", 15*time.Second)
	if !p.Alive() {
		t.Error("the pane's client has gone from a session that is still there")
	}
}

// tmux reads its config only when the server starts, so a server an older
// omassh started is still running with tmux's prefix on it, and would have gone
// on taking ctrl+b until the machine restarted. Opening a session on it switches
// the prefix off there too.
func TestASessionOpenedOnAnOlderServerStillGetsCtrlB(t *testing.T) {
	sock := testSocket(t)
	exec.Command("tmux", "-L", sock, "kill-server").Run()
	killServer(t)
	// Started the way an older omassh started it: tmux's defaults, prefix and
	// all.
	if out, err := exec.Command("tmux", "-L", sock, "-f", "/dev/null",
		"new-session", "-d", "-s", "older", "sleep 300").CombinedOutput(); err != nil {
		t.Fatalf("starting a server with tmux's defaults: %v\n%s", err, out)
	}
	if out, _ := exec.Command("tmux", "-L", sock, "show-options", "-g", "prefix").Output(); !strings.Contains(string(out), "C-b") {
		t.Fatalf("the older server does not have tmux's prefix to begin with: %q", out)
	}

	p := openPane(t)
	if !p.Persistent() {
		t.Skip("the pane is not backed by tmux")
	}
	sendCtrlBToTheFarSide(t, p)
}
