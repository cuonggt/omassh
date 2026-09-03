package ui

import (
	"fmt"
	"math"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/cuonggt/omassh/internal/store"
	"github.com/cuonggt/omassh/internal/term"
	"github.com/cuonggt/omassh/internal/ui/theme"
)

// paneFPS is how often open panes redraw. The emulator cannot say "something
// changed", so this polls; 20 a second is smooth for a terminal and cheap
// enough that idle panes cost nothing noticeable.
const paneFPS = 50 * time.Millisecond

// prefixKey introduces a workspace command. Once panes are live every
// keystroke belongs to a remote — including ctrl+c — so the commands need a
// namespace of their own, the way tmux uses ctrl+b.
const prefixKey = "ctrl+\\"

// maxPanes bounds a split. Past this each pane is too small to be a terminal,
// and every one is a live ssh connection to somebody's server.
const maxPanes = 9

type paneTickMsg struct{}

func paneTick() tea.Cmd {
	return tea.Tick(paneFPS, func(time.Time) tea.Msg { return paneTickMsg{} })
}

// rect is a pane's outer box within the workspace area.
type rect struct{ x, y, w, h int }

// tile lays n panes out in as square a grid as fits, filling row by row.
func tile(n, W, H int) []rect {
	if n <= 0 {
		return nil
	}
	cols := int(math.Ceil(math.Sqrt(float64(n))))
	rows := int(math.Ceil(float64(n) / float64(cols)))

	out := make([]rect, 0, n)
	placed := 0
	y := 0
	for r := 0; r < rows; r++ {
		// The last row takes whatever is left rather than leaving a gap.
		inRow := cols
		if remaining := n - placed; remaining < cols {
			inRow = remaining
		}
		h := H / rows
		if r == rows-1 {
			h = H - y // absorb the rounding
		}
		x := 0
		for c := 0; c < inRow; c++ {
			w := W / inRow
			if c == inRow-1 {
				w = W - x
			}
			out = append(out, rect{x: x, y: y, w: w, h: h})
			x += w
			placed++
		}
		y += h
	}
	return out
}

// --- opening -----------------------------------------------------------

func (m Model) openPane() (tea.Model, tea.Cmd) {
	h, ok := m.selectedHost()
	if !ok {
		return m, nil
	}
	return m.openPanes([]store.Host{h})
}

// openPaneGroup splits across every host in the selected group.
func (m Model) openPaneGroup() (tea.Model, tea.Cmd) {
	g, ok := m.currentGroup()
	if !ok {
		return m, nil
	}
	hosts := m.d.hostsIn(g.ID)
	if len(hosts) == 0 {
		m.setStatus("no hosts in " + g.Name)
		return m, nil
	}
	if len(hosts) > maxPanes {
		m.setErr(fmt.Errorf("%s has %d hosts — %d panes is the limit", g.Name, len(hosts), maxPanes))
		return m, nil
	}
	return m.openPanes(hosts)
}

func (m Model) openPanes(hosts []store.Host) (tea.Model, tea.Cmd) {
	rects := tile(len(hosts), m.workspaceWidth(), m.workspaceHeight())

	var opened []*term.Pane
	var failed []string
	for i, h := range hosts {
		resolved := m.d.resolver.Resolve(h).Host
		w, ht := paneInner(rects[i])
		p, err := term.Open(resolved, w, ht)
		if err != nil {
			failed = append(failed, h.Name)
			continue
		}
		opened = append(opened, p)
	}
	if len(opened) == 0 {
		m.setErr(fmt.Errorf("could not open any pane"))
		return m, nil
	}

	m.termPanes = opened
	m.termFocus = 0
	m.broadcast = false
	m.prefixArmed = false
	m.mode = modePane

	msg := fmt.Sprintf("%d pane%s — %s then d to detach", len(opened), plural(len(opened)), prefixKey)
	if len(failed) > 0 {
		msg = fmt.Sprintf("%s (failed: %s)", msg, strings.Join(failed, ", "))
	}
	m.setStatus(msg)
	return m, paneTick()
}

func (m Model) workspaceWidth() int  { return max(m.w, 20) }
func (m Model) workspaceHeight() int { return max(m.h-statusHeight, 6) }

// paneInner is the emulator size inside a box's borders and padding.
func paneInner(r rect) (int, int) { return max(r.w-4, 10), max(r.h-2, 3) }

// --- keys --------------------------------------------------------------

func (m Model) handlePaneKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if len(m.termPanes) == 0 {
		m.mode = modeBrowse
		return m, nil
	}
	key := msg.String()

	if m.prefixArmed {
		m.prefixArmed = false
		return m.handlePrefixCommand(key, msg)
	}
	if key == prefixKey {
		m.prefixArmed = true
		return m, nil
	}

	// Broadcast sends to every live pane; otherwise only the focused one.
	if m.broadcast {
		for _, p := range m.termPanes {
			if p.Alive() {
				p.SendKey(msg)
			}
		}
		return m, nil
	}
	if p := m.focusedPane(); p != nil && p.Alive() {
		p.SendKey(msg)
	}
	return m, nil
}

func (m Model) handlePrefixCommand(key string, msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch key {
	case prefixKey:
		// Pressed twice: the remote wanted it after all.
		if p := m.focusedPane(); p != nil {
			p.SendKey(msg)
		}
	case "d", "esc":
		return m.closeAllPanes("detached — sessions closed")
	case "o", "tab":
		m.termFocus = (m.termFocus + 1) % len(m.termPanes)
	case "b":
		m.broadcast = !m.broadcast
		if m.broadcast {
			m.setStatus("broadcast ON — keys go to all " + fmt.Sprint(len(m.termPanes)) + " panes")
		} else {
			m.setStatus("broadcast off")
		}
	case "x":
		return m.closeFocusedPane()
	}
	return m, nil
}

func (m Model) focusedPane() *term.Pane {
	if len(m.termPanes) == 0 {
		return nil
	}
	return m.termPanes[clamp(m.termFocus, 0, len(m.termPanes)-1)]
}

func (m Model) closeFocusedPane() (tea.Model, tea.Cmd) {
	p := m.focusedPane()
	if p == nil {
		return m, nil
	}
	name := p.Host.Name
	m.recordPaneSession(p)
	p.Close()

	rest := make([]*term.Pane, 0, len(m.termPanes)-1)
	for i, x := range m.termPanes {
		if i != m.termFocus {
			rest = append(rest, x)
		}
	}
	m.termPanes = rest
	if len(m.termPanes) == 0 {
		return m.closeAllPanes("closed " + name + " — no panes left")
	}
	m.termFocus = clamp(m.termFocus, 0, len(m.termPanes)-1)
	m.setStatus("closed " + name)
	return m, nil
}

func (m Model) closeAllPanes(reason string) (tea.Model, tea.Cmd) {
	for _, p := range m.termPanes {
		m.recordPaneSession(p)
		p.Close()
	}
	m.termPanes = nil
	m.broadcast = false
	m.prefixArmed = false
	m.mode = modeBrowse
	m.reload()
	m.setStatus(reason)
	return m, nil
}

// recordPaneSession keeps pane sessions in the same history as handed-off
// ones; a host you just worked on should not read as never connected.
func (m Model) recordPaneSession(p *term.Pane) {
	if p == nil {
		return
	}
	m.st.RecordSession(p.Host.StatKey(), time.Now())
}

func (m Model) handlePaneTick() (tea.Model, tea.Cmd) {
	if len(m.termPanes) == 0 || m.mode != modePane {
		return m, nil
	}
	rects := tile(len(m.termPanes), m.workspaceWidth(), m.workspaceHeight())
	for i, p := range m.termPanes {
		w, h := paneInner(rects[i])
		p.Resize(w, h)
	}
	return m, paneTick()
}

// --- rendering ---------------------------------------------------------

func (m Model) paneView(content int) string {
	if len(m.termPanes) == 0 {
		return ""
	}
	rects := tile(len(m.termPanes), m.w, content)

	// Render each pane, then stitch rows together in layout order.
	boxes := make([]string, len(m.termPanes))
	for i, p := range m.termPanes {
		boxes[i] = box(m.termPaneTitle(i, p), i == m.termFocus, rects[i].w, rects[i].h, p.Render())
	}

	var rows []string
	i := 0
	for i < len(boxes) {
		y := rects[i].y
		j := i
		for j < len(boxes) && rects[j].y == y {
			j++
		}
		rows = append(rows, lipgloss.JoinHorizontal(lipgloss.Top, boxes[i:j]...))
		i = j
	}
	return lipgloss.JoinVertical(lipgloss.Left, rows...)
}

func (m Model) termPaneTitle(i int, p *term.Pane) string {
	name := p.Host.Name
	if !p.Alive() {
		return name + "  " + p.Status()
	}
	if i == m.termFocus {
		if m.broadcast {
			return name + "  " + theme.Fg(theme.Red).Render("BROADCAST")
		}
		if m.prefixArmed {
			return name + "  " + theme.Fg(theme.Yellow).Render("prefix…")
		}
		return name
	}
	if m.broadcast {
		return name + "  " + theme.Fg(theme.Red).Render("broadcast")
	}
	return name
}

// paneCursor puts the real cursor where the focused remote put it, so the
// session looks like a terminal rather than a picture of one.
func (m Model) paneCursor() *tea.Cursor {
	p := m.focusedPane()
	if p == nil || !p.Alive() {
		return nil
	}
	rects := tile(len(m.termPanes), m.w, m.h-statusHeight)
	r := rects[clamp(m.termFocus, 0, len(rects)-1)]
	x, y := p.CursorPosition()
	return tea.NewCursor(r.x+x+2, r.y+y+1)
}

// Close releases every pane, for teardown from main.
func (m Model) closePanesForExit() {
	for _, p := range m.termPanes {
		p.Close()
	}
}
