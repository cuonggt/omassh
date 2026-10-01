package term_test

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// topRow waits for the pane's first row to read want.
func topRow(t *testing.T, p interface{ Render() string }, want string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var top string
	for time.Now().Before(deadline) {
		top = strings.TrimRight(strings.Split(ansiRE.ReplaceAllString(p.Render(), ""), "\n")[0], " ")
		if top == want {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("the top row reads %q, want %q", top, want)
}

// The wheel over less scrolls less. It used to scroll the history behind it —
// which is the shell's from before less started — so a notch stacked old
// shell output over less's page and less never moved.
//
// LESS is emptied for the run, since the suite's own environment reaches the
// test server's shell, and a LESS with -X in it keeps less off the alternate
// screen this is about.
func TestTheWheelOverLessScrollsLessNotTheShellBehindIt(t *testing.T) {
	p := openTmuxPane(t, 40, 8)
	type_(p, "seq 1 30; seq 101 200 | LESS= less")
	p.SendKey(enter)
	topRow(t, p, "101")

	if !p.Wheel(0, 0, false, 3) {
		t.Fatal("less did not take a notch of the wheel")
	}
	topRow(t, p, "104")
	if off, _ := p.ScrollOffset(); off != 0 {
		t.Errorf("the view is %d lines back into the history, over less", off)
	}

	if !p.Wheel(0, 0, true, 3) {
		t.Fatal("less did not take a notch of the wheel back")
	}
	topRow(t, p, "101")
	type_(p, "q")
}

// A program that asked for the mouse gets the wheel itself, up and down, at the
// cell under the pointer. od prints what reached it once its input ends.
func TestTheWheelReachesAProgramThatAskedForTheMouse(t *testing.T) {
	p := openTmuxPane(t, 60, 12)
	type_(p, `printf '\033[?1000h\033[?1006h'; od -A n -c; printf '\033[?1000l\033[?1006l'`)
	p.SendKey(enter)

	// Until tmux has heard the program ask, the notch is the shell's history
	// and is not sent; the first one sent is the first one taken.
	deadline := time.Now().Add(10 * time.Second)
	for !p.Wheel(5, 3, true, 3) {
		if time.Now().After(deadline) {
			t.Fatal("the program asked for the mouse and never got the wheel")
		}
		time.Sleep(25 * time.Millisecond)
	}
	time.Sleep(handPace)
	if !p.Wheel(5, 3, false, 3) {
		t.Fatal("a notch down did not reach the program")
	}
	time.Sleep(handPace)
	p.SendKey(enter)
	p.SendKey(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl}) // end od's input

	// SGR reports at the cell, counted from one: 64 is the wheel up, 65 down.
	// od breaks its lines every sixteen bytes, wherever that falls, so what
	// it printed is read with the lines run together.
	waitUntil(t, p, "both wheel reports, as od printed them", func(screen string) bool {
		s := strings.Join(strings.Fields(screen), " ")
		return strings.Contains(s, "< 6 4 ; 6 ; 4 M") && strings.Contains(s, "< 6 5 ; 6 ; 4 M")
	}, 15*time.Second)
}
