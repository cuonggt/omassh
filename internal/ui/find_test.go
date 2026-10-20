package ui

import (
	"strings"
	"testing"
)

// A session's keys belong to the remote, so the box that searches its output
// has to take them while it is open — otherwise a search for `echo` would run
// one on the far side instead.
func TestTheFindBoxTakesWhatIsTypedRatherThanTheRemote(t *testing.T) {
	h := newHarness(t)
	h.openLiveSession("alpha")
	h.m.focus = panelSession

	h.press("prefix", "/")
	if h.m.mode != modeFind {
		t.Fatalf("mode = %d after the prefix and /, want the find box", h.m.mode)
	}
	h.type_("error")
	if got := h.m.find.Value(); got != "error" {
		t.Errorf("the box holds %q, want what was typed", got)
	}
	h.mustContain("error")
}

// esc leaves the box without searching, and the session keeps the keyboard.
func TestEscapeLeavesTheFindBoxWithTheSessionStillFocused(t *testing.T) {
	h := newHarness(t)
	h.openLiveSession("alpha")
	h.m.focus = panelSession

	h.press("prefix", "/")
	h.type_("x")
	h.press("esc")

	if h.m.mode != modeBrowse {
		t.Errorf("mode = %d after esc, want back to the session", h.m.mode)
	}
	if h.m.focus != panelSession {
		t.Error("leaving the find box took the keyboard off the session")
	}
}

// A search with nothing to find names what it looked for. The alternative is a
// view that did not move and no word about why.
func TestASearchWithNothingToFindNamesWhatItLookedFor(t *testing.T) {
	h := newHarness(t)
	h.openLiveSession("alpha")
	h.m.focus = panelSession

	h.press("prefix", "/")
	h.type_("not-in-this-session")
	h.press("enter")

	if h.m.mode != modeBrowse {
		t.Errorf("mode = %d after enter, want back to the session", h.m.mode)
	}
	if want := "no match for not-in-this-session"; !strings.Contains(h.m.status, want) {
		t.Errorf("status = %q, want %q", h.m.status, want)
	}
}

// The box opens empty every time, because an empty box is what asks for the
// last search to be run again: words left in it would find the same line over
// and over.
func TestTheFindBoxOpensEmpty(t *testing.T) {
	h := newHarness(t)
	h.openLiveSession("alpha")
	h.m.focus = panelSession

	h.press("prefix", "/")
	h.type_("first time")
	h.press("esc")

	h.press("prefix", "/")
	if got := h.m.find.Value(); got != "" {
		t.Errorf("the box opened holding %q, so enter would find that again", got)
	}
}

// The cheatsheet names the key, since one nothing mentions is one nobody
// finds. Not the prefix hints: those give way to the status from the right, so
// what they show at any moment is a question about the width of the terminal
// and not about the keys.
func TestTheCheatsheetNamesTheFindKey(t *testing.T) {
	h := newHarness(t)

	rows := strings.Join(h.m.helpLines(), "\n")
	if !strings.Contains(rows, "prefix /") {
		t.Errorf("the cheatsheet does not name the find key:\n%s", rows)
	}
}
