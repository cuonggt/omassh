package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// The chrome follows Omarchy's design language, and these are the parts of it
// a reader of the drawing code cannot check for themselves: that the gap is
// really there and really gives way, that a cursor survives losing focus, and
// that reserving the cursor's column actually keeps the columns straight.

// sidebarRow is the line of the frame whose sidebar draws text, escapes still
// on it so the style in front of something can be read.
//
// The sidebar only, because a host's name is drawn twice: once in the list and
// again as the title of the pane describing it, and the title is the one a
// search of the whole line finds first.
func sidebarRow(t *testing.T, h *harness, text string) string {
	t.Helper()
	side := h.m.layout().side
	for _, l := range strings.Split(h.m.View().Content, "\n") {
		plain := []rune(ansiRE.ReplaceAllString(l, ""))
		if strings.Contains(string(plain[:min(side, len(plain))]), text) {
			return l
		}
	}
	t.Fatalf("nothing in the sidebar draws %q:\n%s", text, h.screen())
	return ""
}

// Panels are tiled windows with a gap between them, not boxes whose borders
// touch — two touching borders draw a divider two cells thick where the design
// asks for a hairline. The gap is blank: the column beside the sidebar and the
// row between Groups and Hosts have nothing in them at all.
func TestPanelsHaveABlankCellBetweenThemRatherThanBordersThatTouch(t *testing.T) {
	h := newHarness(t)
	h.addHost("web-01", "10.0.0.1")

	l := h.m.layout()
	if l.gapX != 1 || l.gapY != 1 {
		t.Fatalf("the gaps are %d across and %d down at %dx%d, want one of each",
			l.gapX, l.gapY, testW, testH)
	}

	rows := strings.Split(h.screen(), "\n")
	for y := range l.content {
		if got := []rune(rows[y])[l.side]; got != ' ' {
			t.Errorf("row %d has %q in the gap beside the sidebar, want a blank", y, got)
		}
	}
	if sidebar := strings.TrimRight(string([]rune(rows[l.groupsH])[:l.side]), " "); sidebar != "" {
		t.Errorf("the gap row under Groups draws %q, want a blank", sidebar)
	}
}

// The gap comes out of the content, so where there is nothing to spare it is
// the first thing to go. A row spent on it when Hosts has none left would draw
// a frame one line taller than the terminal, which is the one thing the
// renderer may never do — and clamp hands Groups its floor rather than its
// ceiling on a short frame, so there is a real size where this happens.
func TestTheGapGivesWayBeforeTheFrameOverflows(t *testing.T) {
	h := newHarness(t)
	h.addHost("web-01", "10.0.0.1")

	for _, size := range [][2]int{{60, 6}, {40, 8}, {30, 6}, {24, 5}} {
		h.send(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		l := h.m.layout()

		if l.groupsH+l.gapY+l.hostsH != l.content {
			t.Errorf("%dx%d: %d rows of panel and %d of gap in %d of content",
				size[0], size[1], l.groupsH+l.hostsH, l.gapY, l.content)
		}
		if got := len(strings.Split(h.screen(), "\n")); got != size[1] {
			t.Errorf("%dx%d rendered %d lines, want %d", size[0], size[1], got, size[1])
		}
	}
}

// Omarchy keeps the cursor and which panel has focus apart, so a panel that
// loses focus keeps showing where its cursor is — the border says where the
// keys are going. It used to draw no cursor at all once focus left, which
// meant tabbing away lost your place in the list, and the wheel, which moves
// whatever list it is over, moved a cursor nothing was drawing.
func TestAPanelThatLosesFocusKeepsShowingWhereItsCursorIs(t *testing.T) {
	h := newHarness(t)
	h.addHost("web-01", "10.0.0.1")
	h.addHost("db-01", "10.0.0.2")

	h.press("2", "j")
	sel, ok := h.m.selectedHost()
	if !ok {
		t.Fatal("no host is selected")
	}
	want := sel.Name
	focused := sidebarRow(t, h, want)

	h.press("1")
	if got, _ := h.m.selectedHost(); got.Name != want {
		t.Fatalf("the selection moved to %q when focus left the list", got.Name)
	}
	unfocused := sidebarRow(t, h, want)

	for _, l := range []string{focused, unfocused} {
		if !strings.Contains(l, cursorBar) {
			t.Errorf("the cursor's row draws no bar: %q", l)
		}
	}
	// Same bar, different colour: accent while the keys are going there,
	// muted once they are not. Colour is the only thing that may differ,
	// since the bar holds the column open on every row either way.
	if styleAt(focused, cursorBar) == styleAt(unfocused, cursorBar) {
		t.Errorf("the cursor is drawn in %q whether or not its panel has focus",
			styleAt(focused, cursorBar))
	}
}

// The cursor's cell is reserved on every row, not just the one it is on, so
// arriving at a row does not move it. A bar prefixed to the cursor's row alone
// shifted that row's text one cell right of all the others.
func TestEveryRowInAListStartsItsTextInTheSameColumn(t *testing.T) {
	h := newHarness(t)
	for _, n := range []string{"web-01", "db-01", "cache-01"} {
		h.addHost(n, "10.0.0.1")
	}
	h.press("2", "j")

	// The sidebar's own columns: the selected host's name is drawn again as
	// the title of the pane beside the list, which starts somewhere else
	// entirely and is not a row of anything.
	side := h.m.layout().side
	cols := map[string]int{}
	for _, name := range []string{"web-01", "db-01", "cache-01"} {
		for _, l := range strings.Split(h.screen(), "\n") {
			runes := []rune(l)
			if c := column(string(runes[:min(side, len(runes))]), name); c >= 0 {
				cols[name] = c
				break
			}
		}
	}
	if len(cols) != 3 {
		t.Fatalf("only %d of the three hosts are drawn:\n%s", len(cols), h.screen())
	}
	for name, c := range cols {
		if c != cols["web-01"] {
			t.Errorf("%s starts at column %d, and web-01 at %d", name, c, cols["web-01"])
		}
	}
}
