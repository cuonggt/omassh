package term_test

import (
	"strings"
	"testing"
	"time"
)

// The tmux-backed pane hands the search to tmux, which looks through the
// history tmux holds — the whole of what the session has said, where this
// pane's emulator has only what it was shown.
func TestATmuxPaneFindsThroughTmuxsHistory(t *testing.T) {
	p := openTmuxPane(t, 60, 10)
	p.SendText("printf 'the-needle\\n'; seq 1 200\n")
	waitFor(t, p, "200", 15*time.Second)

	if strings.Contains(p.Render(), "the-needle") {
		t.Fatal("the needle is still on the screen, so finding it would prove nothing")
	}
	if !p.Find("the-needle") {
		t.Fatal("tmux found nothing, with the needle two hundred lines back")
	}
	// tmux redraws in its own time, and what it draws arrives here as output
	// like any other: the screen has to be waited for, not read.
	waitFor(t, p, "the-needle", 10*time.Second)
}

// A search that finds nothing leaves the live view, and leaves tmux out of
// copy mode with it. The proof is that typing still reaches the shell: in copy
// mode tmux would take the keys as commands of its own.
func TestATmuxSearchThatFindsNothingLeavesTheLiveView(t *testing.T) {
	p := openTmuxPane(t, 60, 10)
	p.SendText("printf 'something\\n'\n")
	waitFor(t, p, "something", 15*time.Second)

	if p.Find("no-such-text-anywhere") {
		t.Fatal("tmux claimed a match that is not there")
	}
	if off, _ := p.ScrollOffset(); off != 0 {
		t.Errorf("the view is %d lines back after a search that found nothing", off)
	}
	p.SendText("printf 'still-listening\\n'\n")
	waitFor(t, p, "still-listening", 10*time.Second)
}

// A match on the screen already in view scrolls nothing, and tmux is in copy
// mode all the same. Typing has to leave it, and how far the pane thinks it is
// scrolled back cannot be what decides that: it is zero here.
func TestTypingAfterAMatchOnTheScreenStillReachesTheShell(t *testing.T) {
	p := openTmuxPane(t, 60, 10)
	p.SendText("printf 'on-this-screen\\n'\n")
	waitFor(t, p, "on-this-screen", 15*time.Second)

	if !p.Find("on-this-screen") {
		t.Fatal("tmux found nothing on the screen in front of it")
	}
	if off, _ := p.ScrollOffset(); off != 0 {
		t.Fatalf("the match was %d lines back, so this is not the case being tested", off)
	}
	p.SendText("printf 'still-listening\\n'\n")
	waitFor(t, p, "still-listening", 10*time.Second)
}

// Searching again goes further back through tmux's history, to the match
// before the one in view.
func TestFindingAgainWalksBackThroughTmuxsHistory(t *testing.T) {
	p := openTmuxPane(t, 60, 10)
	p.SendText("printf 'needle-further\\n'; seq 1 100; printf 'needle-nearer\\n'; seq 101 200\n")
	waitFor(t, p, "200", 15*time.Second)

	if !p.Find("needle-") {
		t.Fatal("tmux found neither needle")
	}
	waitFor(t, p, "needle-nearer", 10*time.Second)

	if !p.FindAgain() {
		t.Fatal("searching again found nothing, with a needle further back")
	}
	waitFor(t, p, "needle-further", 10*time.Second)
}
