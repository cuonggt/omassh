package ui

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// drag presses at one screen cell, moves to another and lets go there, and
// returns what letting go asked for.
func (h *harness) drag(ax, ay, bx, by int) tea.Cmd {
	h.t.Helper()
	h.send(tea.MouseClickMsg{X: ax, Y: ay, Button: tea.MouseLeft})
	h.send(tea.MouseMotionMsg{X: bx, Y: by, Button: tea.MouseLeft})
	return h.send(tea.MouseReleaseMsg{X: bx, Y: by, Button: tea.MouseLeft})
}

// deliver runs a command and hands its message back to the model, as the
// program would, and returns whatever that asked for in turn.
func (h *harness) deliver(cmd tea.Cmd) tea.Cmd {
	h.t.Helper()
	if cmd == nil {
		return nil
	}
	return h.send(cmd())
}

// drawnRow finds a row of the session with something on it, short of the full
// width, and says where it is on screen and what it says.
func (h *harness) drawnRow() (left, y int, drawn string) {
	h.t.Helper()
	w, ht := h.m.attached.Size()
	left = h.m.layout().side + 2 // the border, and the space inside it
	rows := strings.Split(h.screen(), "\n")
	for y := 1; y <= ht && y < len(rows); y++ {
		drawn := strings.TrimRight(ansi.Cut(rows[y], left, left+w), " ")
		if drawn != "" && ansi.StringWidth(drawn) < w {
			return left, y, drawn
		}
	}
	h.t.Skipf("the session drew nothing to select:\n%s", h.screen())
	return 0, 0, ""
}

// A drag over the session copies what is drawn under it, and the status says
// what that was — a copy cannot be seen on the clipboard, and what is worth
// checking is that it holds what was meant.
//
// An ended session, because one has no tmux left to hand the mouse to and so
// selects for itself; tmux's selection is tested where it is made.
func TestADragOverTheSessionCopiesWhatIsDrawnUnderIt(t *testing.T) {
	h := newHarness(t)
	h.deadSession("alpha")
	left, y, drawn := h.drawnRow()

	h.deliver(h.drag(left, y, left+ansi.StringWidth(drawn), y))
	if len(h.clipboard) != 1 || h.clipboard[0] != drawn {
		t.Fatalf("copied %q from a row drawn as %q", h.clipboard, drawn)
	}
	if want := "copied " + describeCopy(drawn); h.m.status != want {
		t.Errorf("status = %q, want %q", h.m.status, want)
	}
}

// A drag that wanders off the session keeps selecting up to its edge, as a
// selection in a terminal does when the pointer leaves the window. Over the
// host list it is still the session's drag, and stops at the session's first
// column rather than ending or selecting a host.
func TestADragThatLeavesTheSessionStopsAtItsEdge(t *testing.T) {
	h := newHarness(t)
	h.deadSession("alpha")
	left, y, drawn := h.drawnRow()
	k := min(4, ansi.StringWidth(drawn))

	h.deliver(h.drag(left+k, y, 2, y))
	if want := ansi.Cut(drawn, 0, k); len(h.clipboard) != 1 || h.clipboard[0] != want {
		t.Errorf("copied %q, want %q — the row from its first column to where the drag began",
			h.clipboard, want)
	}
}

// Only a press on the session itself starts a selection. One on its frame
// focuses it, as a click there always has, and one that began on the host list
// is the host list's however far it is dragged.
func TestOnlyAPressOnTheSessionStartsASelection(t *testing.T) {
	h := newHarness(t)
	h.openLiveSession("alpha")
	l := h.m.layout()

	for _, x := range []int{l.side, l.side + 1} {
		h.m.focus = panelHosts
		h.click(x, 5)
		if h.m.focus != panelSession {
			t.Errorf("a click on the session's frame at column %d did not focus it", x)
		}
		if h.m.dragPane != nil {
			t.Errorf("a press on the session's frame at column %d started a selection", x)
		}
		h.send(tea.MouseReleaseMsg{X: x, Y: 5, Button: tea.MouseLeft})
	}

	h.click(l.side+2, 5)
	if h.m.dragPane != h.m.attached {
		t.Error("a press on the session did not start a selection in it")
	}
	h.send(tea.MouseReleaseMsg{X: l.side + 2, Y: 5, Button: tea.MouseLeft})
	if h.m.dragPane != nil {
		t.Error("the selection outlived the button coming up")
	}

	if cmd := h.drag(2, 3, l.side+10, 5); cmd != nil || h.m.dragPane != nil {
		t.Error("a drag that began on the host list selected in the session")
	}
}

// What the machine's clipboard took is reported as copied, by what it was.
func TestACopyThisMachineTookSaysWhatItHolds(t *testing.T) {
	h := newHarness(t)

	if cmd := h.send(clipboardMsg{text: "bet"}); cmd != nil {
		t.Error("asked the terminal to copy what the machine's clipboard already had")
	}
	if want := `copied "bet"`; h.m.status != want {
		t.Errorf("status = %q, want %q", h.m.status, want)
	}

	h.send(clipboardMsg{text: "one\ntwo\nthree\n"})
	if want := "copied 3 lines"; h.m.status != want {
		t.Errorf("status = %q, want %q", h.m.status, want)
	}

	// A long line is named by its start, so the rest of the status survives.
	h.send(clipboardMsg{text: strings.Repeat("x", 200)})
	if w := ansi.StringWidth(h.m.status); w > 60 || !strings.Contains(h.m.status, "…") {
		t.Errorf("status = %q (%d cells), want the line cut short", h.m.status, w)
	}
}

// With no clipboard program to take it, the terminal is asked instead — and
// the status says asked, because a terminal is free to refuse and nothing
// comes back to say whether it did.
func TestACopyNothingHereTookIsAskedOfTheTerminal(t *testing.T) {
	h := newHarness(t)

	cmd := h.send(clipboardMsg{text: "one\ntwo", err: errNoClipboard})
	if want := "asked the terminal to copy 2 lines"; h.m.status != want {
		t.Errorf("status = %q, want %q", h.m.status, want)
	}
	if cmd == nil {
		t.Fatal("the terminal was never asked")
	}
	msg := cmd()
	if name := reflect.TypeOf(msg).Name(); name != "setClipboardMsg" || fmt.Sprint(msg) != "one\ntwo" {
		t.Errorf("asked the terminal with a %s of %q, want Bubble Tea's OSC 52 of %q",
			name, fmt.Sprint(msg), "one\ntwo")
	}
}

// Which program holds the clipboard is a fact about the machine: pbcopy on a
// Mac, and on Linux whatever the display server has, which is nothing at all
// without one.
func TestTheClipboardProgramIsTheMachines(t *testing.T) {
	env := func(vars map[string]string) func(string) string {
		return func(k string) string { return vars[k] }
	}
	for _, c := range []struct {
		name string
		goos string
		vars map[string]string
		want []string
	}{
		{"a Mac", "darwin", nil, []string{"pbcopy"}},
		{"Wayland", "linux", map[string]string{"WAYLAND_DISPLAY": "wayland-0"}, []string{"wl-copy"}},
		{"X", "linux", map[string]string{"DISPLAY": ":0"}, []string{"xclip", "xsel"}},
		{"Wayland with X beside it", "linux",
			map[string]string{"WAYLAND_DISPLAY": "wayland-0", "DISPLAY": ":0"}, []string{"wl-copy", "xclip", "xsel"}},
		{"no display", "linux", nil, nil},
	} {
		var got []string
		for _, argv := range clipboardPrograms(c.goos, env(c.vars)) {
			got = append(got, argv[0])
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: tries %v, want %v", c.name, got, c.want)
		}
	}
}

// Over ssh this machine's clipboard is someone else's — pbcopy on a Mac
// reached from a laptop fills the Mac's — so no program is tried, and the
// terminal at the far end of the connection is asked instead.
func TestOverSSHThisMachinesClipboardIsNotTried(t *testing.T) {
	t.Setenv("SSH_CONNECTION", "10.0.0.1 50000 10.0.0.2 22")
	if err := systemClipboard("anything"); !errors.Is(err, errNoClipboard) {
		t.Errorf("systemClipboard over ssh = %v, want errNoClipboard", err)
	}
}
