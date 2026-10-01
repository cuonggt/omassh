package term

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
)

// showing is a pane with nothing behind it — no pty, no tmux — displaying what
// it is given. That is the pane a selection is made in by omassh itself: one
// with no tmux to hand the mouse to.
func showing(w, h int, output string) *Pane {
	p := &Pane{em: vt.NewSafeEmulator(w, h), w: w, h: h,
		done: make(chan struct{}), changed: make(chan struct{}, 1)}
	p.em.SetScrollbackSize(scrollback)
	p.em.Write([]byte(output))
	return p
}

// drag presses, moves and lets go, and returns what was copied.
func drag(p *Pane, ax, ay, bx, by int) string {
	p.Press(ax, ay)
	p.Drag(bx, by)
	return p.Release(bx, by)
}

// A drag covers the cells from where it began up to where it ended, that one
// not included, whichever way it went. That is how tmux counts its own
// selection, so the two kinds of pane copy the same text for the same drag.
func TestADragCopiesFromWhereItBeganUpToWhereItEnded(t *testing.T) {
	p := showing(30, 4, "alpha beta gamma")

	if got := drag(p, 6, 0, 9, 0); got != "bet" {
		t.Errorf("left to right copied %q, want %q", got, "bet")
	}
	if got := drag(p, 9, 0, 6, 0); got != "bet" {
		t.Errorf("right to left copied %q, want %q", got, "bet")
	}
}

// Across rows each row is a line, and the spaces that fill a row out to the
// width of the pane are not part of it.
func TestADragAcrossRowsCopiesARowToALine(t *testing.T) {
	p := showing(30, 4, "alpha beta gamma\r\nsecond line")

	if got, want := drag(p, 6, 0, 3, 1), "beta gamma\nsec"; got != want {
		t.Errorf("copied %q, want %q", got, want)
	}
}

// A click is not a selection. It focuses the pane, which is what it did before
// there was any selecting, and leaves both the screen and the clipboard alone.
func TestAClickSelectsNothing(t *testing.T) {
	p := showing(30, 4, "alpha beta gamma")
	before := p.Render()

	p.Press(3, 0)
	if p.Render() != before {
		t.Error("pressing the button changed what the pane draws")
	}
	if got := p.Release(3, 0); got != "" {
		t.Errorf("a click copied %q", got)
	}
}

// reversed is the text drawn in reverse video in a rendered row.
var reversed = regexp.MustCompile(`\x1b\[7m([^\x1b]*)`)

// The selection is drawn as it is made, so that what is about to be copied can
// be seen before the button comes up.
func TestTheSelectionIsDrawnWhileItIsMade(t *testing.T) {
	p := showing(30, 4, "alpha beta gamma")

	p.Press(6, 0)
	p.Drag(9, 0)
	row := strings.Split(p.Render(), "\n")[0]
	if m := reversed.FindStringSubmatch(row); m == nil || m[1] != "bet" {
		t.Errorf("row drawn as %q; want exactly \"bet\" in reverse video", row)
	}

	p.Release(9, 0)
	if strings.Contains(p.Render(), "\x1b[7m") {
		t.Error("the selection is still drawn after the button came up")
	}
}

// What is copied is what was on screen when the drag began. Output arriving
// meanwhile would otherwise slide under the highlight, and the text copied
// would be text nobody had chosen.
func TestWhatIsCopiedIsWhatWasOnScreenWhenTheDragBegan(t *testing.T) {
	p := showing(30, 4, "the first thing said")

	p.Press(0, 0)
	p.em.Write([]byte("\x1b[H\x1b[2Jsomething else entirely"))
	p.Drag(9, 0)
	if got := p.Release(9, 0); got != "the first" {
		t.Errorf("copied %q, want what was there when the button went down", got)
	}
	if !strings.Contains(p.Render(), "something else") {
		t.Error("the output that arrived meanwhile is not shown once the drag ends")
	}
}

// A wide character takes two cells and is one character. It is copied once,
// and only when the cell it starts in is selected.
func TestAWideCharacterIsCopiedOnce(t *testing.T) {
	p := showing(30, 4, "日本語 ok")

	if got := drag(p, 0, 0, 6, 0); got != "日本語" {
		t.Errorf("copied %q, want %q", got, "日本語")
	}
	if got := drag(p, 1, 0, 6, 0); got != "本語" {
		t.Errorf("starting in the right half of a character copied %q, want %q", got, "本語")
	}
}

// Scrolled back, a selection is of what the pane is showing, which is the
// scrollback rather than the live screen.
func TestScrolledBackASelectionIsOfWhatIsShown(t *testing.T) {
	var out []string
	for i := 1; i <= 20; i++ {
		out = append(out, "line-"+strconv.Itoa(i))
	}
	// Twenty lines in five rows: 16 to 20 on screen, and five back from there
	// starts at 11.
	p := showing(30, 5, strings.Join(out, "\r\n"))
	p.ScrollUp(5)

	if got, want := drag(p, 0, 0, 0, 1), "line-11\n"; got != want {
		t.Errorf("copied %q, want %q — the top row on screen and the start of the next", got, want)
	}
}

// Text the remote spaced out by moving the cursor — a tab, or a program that
// skips over what is already blank — leaves cells it never wrote. They are
// spaces on screen, and in what is copied.
func TestAGapTheRemoteMovedAcrossIsCopiedAsSpaces(t *testing.T) {
	p := showing(30, 4, "name\tsize\r\na\x1b[5Cb")

	if got, want := drag(p, 0, 0, 7, 1), "name    size\na     b"; got != want {
		t.Errorf("copied %q, want %q", got, want)
	}
}

// Scrolling while a selection is being made drops it: the cells it was made
// on are no longer the ones on screen, so whatever it went on to copy would be
// text that was not under the highlight when the button came up.
func TestScrollingDropsASelectionMadeHere(t *testing.T) {
	var out []string
	for i := 1; i <= 20; i++ {
		out = append(out, "line-"+strconv.Itoa(i))
	}
	p := showing(30, 5, strings.Join(out, "\r\n"))

	p.Press(0, 0)
	p.Drag(4, 0)
	p.ScrollUp(3)
	if got := p.Release(4, 0); got != "" {
		t.Errorf("copied %q from a view that had scrolled away underneath it", got)
	}
}

// tmux says what it copied as OSC 52, and only a copy asked for here is taken:
// one arriving long after the button last came up was made in another window
// showing the same session.
func TestOnlyACopyAskedForHereIsTaken(t *testing.T) {
	p := showing(30, 4, "")
	p.session = "omassh-test" // a tmux pane, as far as copies are concerned

	p.tmuxCopied([]byte("52;;YmV0")) // "bet", with no release to answer
	if s, ok := p.TakeCopy(); ok {
		t.Errorf("took %q, which nothing here asked for", s)
	}

	p.mu.Lock()
	p.released = time.Now()
	p.tmuxOffset = 12
	p.mu.Unlock()
	p.tmuxCopied([]byte("52;;YmV0"))
	if s, ok := p.TakeCopy(); !ok || s != "bet" {
		t.Errorf("took %q, %v; want \"bet\"", s, ok)
	}
	if off, _ := p.ScrollOffset(); off != 0 {
		t.Errorf("offset = %d after tmux copied and left copy mode", off)
	}
	if _, ok := p.TakeCopy(); ok {
		t.Error("the same copy was taken twice")
	}
}

// A question for the clipboard is never answered, and an empty copy — a drag
// that came back to where it began — would only empty the clipboard.
func TestNeitherAQuestionNorAnEmptyCopyIsTaken(t *testing.T) {
	p := showing(30, 4, "")
	p.mu.Lock()
	p.released = time.Now()
	p.mu.Unlock()

	for _, osc := range []string{"52;c;?", "52;;", "52;c;not base64!"} {
		p.tmuxCopied([]byte(osc))
		if s, ok := p.TakeCopy(); ok {
			t.Errorf("OSC %q was taken as a copy of %q", osc, s)
		}
	}
}

// visibleRows strips the styling from a render, keeping its rows in place.
func visibleRows(s string) string {
	return regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]`).ReplaceAllString(s, "")
}
