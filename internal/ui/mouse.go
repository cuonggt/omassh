package ui

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

// Mouse support is click-to-select only: put the cursor on a group, a host, or
// the session pane. Everything remains reachable from the keyboard, so this
// adds a way in rather than a dependency.
//
// The layout is derived rather than recorded. browserBody computes the same
// geometry from the same inputs each frame, so hit-testing recomputes it
// instead of keeping a parallel copy that could drift out of step with what is
// actually drawn.

// sidebarLayout is where the browser's boxes sit in the frame.
type sidebarLayout struct {
	side    int // sidebar width; the main pane starts here
	groupsH int // rows given to the Groups box, borders included
	hostsH  int // rows given to the Hosts box
	content int // rows above the status bar
}

func (m Model) layout() sidebarLayout {
	content := m.h - statusHeight
	groupsH := clamp(len(m.d.tree)+2, 4, content/3)
	return sidebarLayout{
		side:    clamp(sidebarWidth, 20, m.w/2),
		groupsH: groupsH,
		hostsH:  content - groupsH,
		content: content,
	}
}

// handleMouseClick moves the selection to whatever was clicked.
func (m Model) handleMouseClick(e tea.Mouse) (tea.Model, tea.Cmd) {
	if m.mode == modeSFTP {
		return m.clickFilePane(e)
	}
	// A dialog owns the screen while it is open. Clicking the list behind it
	// would act on something the dialog is covering.
	if m.mode != modeBrowse && m.mode != modeFilter {
		return m, nil
	}
	l := m.layout()
	if e.Y < 0 || e.Y >= l.content || m.w < minWidth || l.content < minHeight {
		return m, nil // the status bar, or a frame too small to have a layout
	}

	// The main pane: a live session takes it, otherwise it is host detail and
	// there is nothing to focus.
	if e.X >= l.side {
		if m.attached != nil {
			m.focus = panelSession
		}
		return m, nil
	}

	switch {
	case e.Y < l.groupsH:
		// A list that scrolls no longer starts at its first entry, so the row
		// is an offset into the window rather than into the list itself.
		start, _ := listWindow(m.groupIdx, len(m.d.tree), max(l.groupsH-2, 1))
		if i, ok := rowIndex(e.Y, 0, l.groupsH, len(m.d.tree), start); ok {
			m.focus = panelGroups
			m.groupIdx = i
			m.hostIdx = 0
		}
	default:
		// The search box and the blank line under it push the list down.
		offset := m.searchLines()
		hosts := m.visibleHosts()
		rows := max(l.hostsH-2-offset, 1)
		start, _ := listWindow(m.hostIdx, len(hosts), rows)
		if i, ok := rowIndex(e.Y, l.groupsH+offset, l.hostsH-offset, len(hosts), start); ok {
			m.focus = panelHosts
			m.hostIdx = i
		}
	}
	return m, nil
}

// rowIndex maps a screen row to a list index, given the box's top row and
// height and the entry the visible window starts at. It reports false for the
// borders and for empty space past the end of the list, so clicking those
// changes nothing rather than selecting the nearest row.
func rowIndex(y, top, height, n, start int) (int, bool) {
	i := y - top - 1 // the box's top border
	if i < 0 || i >= height-2 {
		return 0, false
	}
	i += start
	if i >= n {
		return 0, false
	}
	return i, true
}

// clickFilePane selects the file under the pointer, in whichever pane it is.
//
// Clicking the other pane focuses it as well as selecting, so one click does
// what tab and a walk down the list would otherwise take.
func (m Model) clickFilePane(e tea.Mouse) (tea.Model, tea.Cmd) {
	content := m.h - statusHeight
	body := content - 1 // the transfer strip
	if m.w < minWidth || content < minHeight || e.Y < 0 || e.Y >= body {
		return m, nil
	}

	i := 0
	if e.X >= m.w/2 {
		i = 1
	}
	p := m.panes[i]

	rows := max(body-2, 1)
	start, _ := listWindow(p.idx, len(p.entries), rows)
	j, ok := rowIndex(e.Y, 0, body, len(p.entries), start)
	if !ok {
		return m, nil
	}

	// A second click on the same row opens it, the way a file browser does.
	// The first click has already selected it, so this only has to act.
	double := m.lastClick.repeats(e, i)
	m.lastClick = clickAt{x: e.X, y: e.Y, pane: i, at: time.Now()}

	m.paneFocus = i
	m.panes[i].idx = j
	if double {
		m.panes[i].enterSelected()
	}
	return m, nil
}

// clickAt is where and when the pointer last went down.
type clickAt struct {
	x, y int
	pane int
	at   time.Time
}

// doubleClickWithin is how close together two presses have to be to count as
// one double click. Long enough to be comfortable, short enough that two
// deliberate clicks on the same file are not read as an open.
const doubleClickWithin = 500 * time.Millisecond

// repeats reports whether a press continues the previous one into a double
// click: the same cell, the same pane, and soon enough after it.
func (c clickAt) repeats(e tea.Mouse, pane int) bool {
	return !c.at.IsZero() && c.pane == pane && c.x == e.X && c.y == e.Y &&
		time.Since(c.at) < doubleClickWithin
}
