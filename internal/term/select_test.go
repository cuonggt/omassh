package term_test

import (
	"strings"
	"testing"
	"time"

	"github.com/cuonggt/omassh/internal/sshx"
	"github.com/cuonggt/omassh/internal/term"
)

// openTmuxPane opens a session to the test server in a pane tmux keeps, which
// is the pane whose selection tmux makes.
func openTmuxPane(t *testing.T, w, h int) *term.Pane {
	t.Helper()
	if !term.TmuxAvailable() {
		t.Skip("no tmux; a pane without it selects for itself, which is tested beside this")
	}
	sshx.SetGlobalOptions([]string{
		"StrictHostKeyChecking=no", "UserKnownHostsFile=/dev/null", "IdentitiesOnly=yes",
	})
	t.Cleanup(func() { sshx.SetGlobalOptions(nil) })

	p, err := term.Open(testHost(t), w, h)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { p.Kill() })
	rowStarting(t, p, "$") // the prompt, so the shell is listening
	return p
}

// rowStarting waits for a row of the pane to begin with prefix, and says which.
// The row rather than the text: a selection is made in cells.
func rowStarting(t *testing.T, p *term.Pane, prefix string) int {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	var rows []string
	for time.Now().Before(deadline) {
		rows = strings.Split(ansiRE.ReplaceAllString(p.Render(), ""), "\n")
		for i, r := range rows {
			if strings.HasPrefix(r, prefix) {
				return i
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("no row of the pane starts %q; it shows:\n%s", prefix, strings.Join(rows, "\n"))
	return 0
}

// handPace is how far apart a drag's events are sent: slower than tmux needs,
// and far quicker than a hand.
//
// tmux starts copy mode for a drag by queueing a command, and a release that
// arrives before the command has run finds no drag to end — it is dropped, and
// tmux sits in copy mode waiting for a release that has already come. A hand
// never sends the two that close together; a test sending them back to back
// did, now and then, and copied nothing.
const handPace = 100 * time.Millisecond

// dragAcross drags from one cell of the pane to another, at handPace.
func dragAcross(p *term.Pane, ax, ay, bx, by int) {
	p.Press(ax, ay)
	time.Sleep(handPace)
	p.Drag(bx, by)
	time.Sleep(handPace)
	p.Release(bx, by)
}

// waitForCopy waits for tmux to say what it copied.
func waitForCopy(t *testing.T, p *term.Pane) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s, ok := p.TakeCopy(); ok {
			return s
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("tmux never said what it copied; the pane shows:\n%s", visible(p.Render()))
	return ""
}

// A drag in a session tmux keeps is tmux's to select, and what it copied comes
// back to the pane.
func TestADragInATmuxSessionCopiesWhatItCovered(t *testing.T) {
	p := openTmuxPane(t, 60, 12)
	type_(p, `printf 'alpha beta gamma\n'`)
	p.SendKey(enter)
	r := rowStarting(t, p, "alpha beta gamma")

	dragAcross(p, 6, r, 9, r)
	if got := waitForCopy(t, p); got != "bet" {
		t.Errorf("copied %q, want %q", got, "bet")
	}
}

// A line too long for the pane is drawn on two rows, and it is still one line.
// tmux knows which rows it wrapped and joins them again; copied a row at a
// time, a long command or a URL would come out broken where the pane was
// narrow, and pasting the command would run half of it.
func TestALineThePaneWrappedIsCopiedAsOneLine(t *testing.T) {
	p := openTmuxPane(t, 40, 12)
	long := strings.Repeat("x", 30) + "WRAP" + strings.Repeat("y", 10)
	type_(p, `printf '%s\n' `+long)
	p.SendKey(enter)
	r := rowStarting(t, p, strings.Repeat("x", 30)+"WRAP")

	dragAcross(p, 30, r, 4, r+1)
	if got, want := waitForCopy(t, p), "WRAP"+strings.Repeat("y", 10); got != want {
		t.Errorf("copied %q, want %q", got, want)
	}
}

// A double click takes the word under the pointer, which tmux does by itself
// once the clicks reach it.
func TestADoubleClickInATmuxSessionCopiesAWord(t *testing.T) {
	p := openTmuxPane(t, 60, 12)
	type_(p, `printf 'alpha beta gamma\n'`)
	p.SendKey(enter)
	r := rowStarting(t, p, "alpha beta gamma")

	for range 2 {
		p.Press(7, r)
		p.Release(7, r)
	}
	if got := waitForCopy(t, p); got != "beta" {
		t.Errorf("copied %q, want %q", got, "beta")
	}
}

// A view the wheel scrolled back is in tmux's copy mode, and copying leaves
// it. The pane has to know, or its title goes on saying "scrolled" over the
// live view.
func TestACopyLeavesAScrolledBackViewLive(t *testing.T) {
	p := openTmuxPane(t, 60, 10)
	type_(p, "seq 1 60")
	p.SendKey(enter)
	waitFor(t, p, "60", 15*time.Second)

	p.ScrollUp(5)
	if off, _ := p.ScrollOffset(); off == 0 {
		t.Fatal("the view did not scroll back")
	}
	time.Sleep(handPace) // tmux draws the scrolled view after saying it has
	dragAcross(p, 0, 2, 2, 3)
	if got := waitForCopy(t, p); got == "" {
		t.Fatal("nothing was copied")
	}
	if off, _ := p.ScrollOffset(); off != 0 {
		t.Errorf("offset = %d after a copy took the view back to live", off)
	}
}

// Only a selection made in the pane reaches the clipboard. A program in the
// session can write OSC 52 itself, and tmux would pass it on with
// set-clipboard on — at which point any host could put whatever it liked on
// the clipboard of whoever was looking at it, ready to be pasted into a shell.
//
// The click comes just before the program writes, so the copy would arrive
// well inside the moment after a release when one is taken. What keeps it out
// is tmux refusing it, which is the thing being tested.
func TestWhatTheRemoteWritesToTheClipboardIsNotTaken(t *testing.T) {
	p := openTmuxPane(t, 60, 12)
	type_(p, `printf '\033]52;c;%s\007' "$(printf pwned | base64)"; echo sent`)
	p.Press(0, 0)
	p.Release(0, 0)
	p.SendKey(enter)
	rowStarting(t, p, "sent")

	time.Sleep(handPace)
	if s, ok := p.TakeCopy(); ok {
		t.Errorf("the far side put %q on the clipboard", s)
	}
}
