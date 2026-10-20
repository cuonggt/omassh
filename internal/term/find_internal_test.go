package term

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/vt"
)

// paneShowing is a pane with nothing behind it, holding lines and no more: no
// ssh and no pty, which is the whole of what searching its own output needs.
func paneShowing(t *testing.T, w, h int, lines ...string) *Pane {
	t.Helper()
	p := &Pane{em: vt.NewSafeEmulator(w, h), w: w, h: h}
	p.em.SetScrollbackSize(scrollback)
	for _, l := range lines {
		if _, err := p.em.Write([]byte(l + "\r\n")); err != nil {
			t.Fatalf("writing %q into the emulator: %v", l, err)
		}
	}
	return p
}

// numbered is a run of lines with something of its own at one of them.
func numbered(n int, at int, what string) []string {
	out := make([]string, 0, n)
	for i := 1; i <= n; i++ {
		if i == at {
			out = append(out, what)
			continue
		}
		out = append(out, fmt.Sprintf("line-%d", i))
	}
	return out
}

// A pane with no tmux behind it searches what it kept itself, and puts the
// match at the top of the view.
func TestAPaneWithoutTmuxFindsWhatItKept(t *testing.T) {
	p := paneShowing(t, 40, 10, numbered(60, 20, "the needle")...)

	if strings.Contains(p.Render(), "the needle") {
		t.Fatal("the needle is on the live screen, so finding it would prove nothing")
	}
	if !p.Find("the needle") {
		t.Fatal("Find said there was nothing to find")
	}
	if !strings.Contains(p.Render(), "the needle") {
		t.Errorf("the view does not show the match:\n%s", p.Render())
	}
	if off, _ := p.ScrollOffset(); off == 0 {
		t.Error("the view says it is live, with the match forty lines behind it")
	}
}

// Without regard to case, and as a pattern, because that is what tmux's copy
// mode makes of what it is given: a pane that has lost its tmux must not
// quietly start answering a different question.
func TestAPaneWithoutTmuxFindsTheWayTmuxWould(t *testing.T) {
	for what, pattern := range map[string]string{
		"a word in another case": "THE NEEDLE",
		"a pattern":              "the ne+dle",
		"a pattern anchored":     "^the needle$",
	} {
		p := paneShowing(t, 40, 10, numbered(60, 20, "the needle")...)
		if !p.Find(pattern) {
			t.Errorf("%s (%q) found nothing", what, pattern)
			continue
		}
		if !strings.Contains(p.Render(), "the needle") {
			t.Errorf("%s (%q) left the view elsewhere", what, pattern)
		}
	}
}

// A pattern that will not compile is taken literally. Someone searching for
// `a(b` meant those three characters, and an error about an unclosed group
// would be an error about something they did not do.
func TestAPatternThatWillNotCompileIsTakenLiterally(t *testing.T) {
	p := paneShowing(t, 40, 10, numbered(60, 20, "a(b broken")...)

	if !p.Find("a(b") {
		t.Error("a pattern that cannot compile found nothing, rather than itself")
	}
	if !strings.Contains(p.Render(), "a(b broken") {
		t.Errorf("the view does not show the match:\n%s", p.Render())
	}
}

// Searching again goes further back, to the match before the one in view,
// which is how a scrollback is walked without a key of its own for it.
func TestFindingAgainGoesFurtherBack(t *testing.T) {
	lines := numbered(90, 70, "needle nearer")
	lines[19] = "needle further"
	p := paneShowing(t, 40, 10, lines...)

	if !p.Find("needle") {
		t.Fatal("the first search found nothing")
	}
	if !strings.Contains(p.Render(), "needle nearer") {
		t.Fatalf("the first match is not the nearest one:\n%s", p.Render())
	}
	if !p.FindAgain() {
		t.Fatal("searching again found nothing, with a match further back")
	}
	if !strings.Contains(p.Render(), "needle further") {
		t.Errorf("searching again did not reach the match further back:\n%s", p.Render())
	}
	if p.FindAgain() {
		t.Error("searching again past the last match found a third one")
	}
}

// A search that finds nothing leaves the view where it was, rather than
// scrolling somewhere to show nothing.
func TestASearchThatFindsNothingLeavesTheView(t *testing.T) {
	p := paneShowing(t, 40, 10, numbered(60, 20, "the needle")...)
	p.ScrollUp(5)
	before, _ := p.ScrollOffset()

	if p.Find("nothing like this is here") {
		t.Fatal("Find claimed a match that is not there")
	}
	if off, _ := p.ScrollOffset(); off != before {
		t.Errorf("the view moved to %d from %d for a search that found nothing", off, before)
	}
}

// Searching again before anything has been searched for has nothing to look
// for, and says so rather than finding the first line it meets.
func TestFindingAgainWithNothingSearchedForYetFindsNothing(t *testing.T) {
	p := paneShowing(t, 40, 10, numbered(60, 20, "the needle")...)

	if p.FindAgain() {
		t.Error("searching again found something with no search behind it")
	}
	if off, _ := p.ScrollOffset(); off != 0 {
		t.Errorf("the view moved to %d for a search that was never made", off)
	}
}
