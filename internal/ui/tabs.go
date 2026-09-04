package ui

import (
	"fmt"
	"math"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/cuonggt/omassh/internal/store"
	"github.com/cuonggt/omassh/internal/term"
	"github.com/cuonggt/omassh/internal/ui/theme"
)

// paneFPS is how often live panes redraw. The emulator cannot say "something
// changed", so this polls; 20 a second is smooth for a terminal and cheap
// enough that idle sessions cost nothing noticeable.
const paneFPS = 50 * time.Millisecond

// prefixKey introduces a workspace command. Inside a session every keystroke
// belongs to the remote — ctrl+c included — so the commands need a namespace
// of their own, the way tmux uses ctrl+b.
const prefixKey = "ctrl+\\"

// maxPanesPerTab bounds a split. Past this each pane is too small to be a
// terminal, and every one is a live connection to somebody's server.
const maxPanesPerTab = 9

// tabBarHeight is the row the tab bar occupies.
const tabBarHeight = 1

type paneTickMsg struct{}

func paneTick() tea.Cmd {
	return tea.Tick(paneFPS, func(time.Time) tea.Msg { return paneTickMsg{} })
}

// tab is one entry in the tab bar. The first tab holds no panes and shows the
// host browser; every other tab holds one or more live sessions. Keeping panes
// inside tabs means a split is an arrangement within a tab rather than a
// separate mode, so there is one session model instead of three.
type tab struct {
	panes     []*term.Pane
	focus     int
	broadcast bool
}

func (t tab) isBrowser() bool { return len(t.panes) == 0 }

func (t tab) focused() *term.Pane {
	if len(t.panes) == 0 {
		return nil
	}
	return t.panes[clamp(t.focus, 0, len(t.panes)-1)]
}

// label is what the tab bar shows.
func (t tab) label() string {
	switch {
	case t.isBrowser():
		return "hosts"
	case len(t.panes) == 1:
		return t.panes[0].Host.Name
	default:
		return fmt.Sprintf("%s +%d", t.panes[0].Host.Name, len(t.panes)-1)
	}
}

// --- opening -----------------------------------------------------------

// openSessionTab connects to the selected host in a tab of its own.
func (m Model) openSessionTab() (tea.Model, tea.Cmd) {
	h, ok := m.selectedHost()
	if !ok {
		return m, nil
	}
	// Reconnecting to a host already open just goes there, rather than
	// stacking a second tab onto the same session.
	for i, t := range m.tabs {
		if len(t.panes) == 1 && t.panes[0].Host.StatKey() == h.StatKey() {
			m.activeTab = i
			m.setStatus("switched to " + h.Name)
			return m, paneTick()
		}
	}
	return m.openTabFor([]store.Host{h})
}

// tabForHost reports the 1-based tab number a host is already open in, or 0.
// Tabs are authoritative for what is live right now, unlike data.live, which
// is only as fresh as the last reload.
func (m Model) tabForHost(h store.Host) int {
	for i, t := range m.tabs {
		for _, p := range t.panes {
			if p.Host.StatKey() == h.StatKey() && p.Alive() {
				return i + 1
			}
		}
	}
	return 0
}

// openGroupTab splits every host in the selected group into one tab.
func (m Model) openGroupTab() (tea.Model, tea.Cmd) {
	g, ok := m.currentGroup()
	if !ok {
		return m, nil
	}
	hosts := m.d.hostsIn(g.ID)
	if len(hosts) == 0 {
		m.setStatus("no hosts in " + g.Name)
		return m, nil
	}
	if len(hosts) > maxPanesPerTab {
		m.setErr(fmt.Errorf("%s has %d hosts — %d panes is the limit", g.Name, len(hosts), maxPanesPerTab))
		return m, nil
	}
	return m.openTabFor(hosts)
}

func (m Model) openTabFor(hosts []store.Host) (tea.Model, tea.Cmd) {
	w, h := m.contentSize()
	rects := tile(len(hosts), w, h)

	var opened []*term.Pane
	var failed []string
	for i, host := range hosts {
		pw, ph := paneInner(rects[i])
		p, err := term.Open(m.d.resolver.Resolve(host).Host, pw, ph)
		if err != nil {
			failed = append(failed, host.Name)
			continue
		}
		opened = append(opened, p)
	}
	if len(opened) == 0 {
		m.setErr(fmt.Errorf("could not open a session"))
		return m, nil
	}

	m.tabs = append(m.tabs, tab{panes: opened})
	m.activeTab = len(m.tabs) - 1
	m.prefixArmed = false

	msg := fmt.Sprintf("%s — %s n/p to switch tabs", opened[0].Host.Name, prefixKey)
	if len(opened) > 1 {
		msg = fmt.Sprintf("%d sessions — %s n/p to switch tabs", len(opened), prefixKey)
	}
	if len(failed) > 0 {
		msg += " (failed: " + strings.Join(failed, ", ") + ")"
	}
	m.setStatus(msg)
	return m, paneTick()
}

// contentSize is the area below the tab bar and above the status bar.
func (m Model) contentSize() (int, int) {
	return max(m.w, 20), max(m.h-statusHeight-tabBarHeight, 6)
}

func paneInner(r rect) (int, int) { return max(r.w-4, 10), max(r.h-2, 3) }

// --- tiling ------------------------------------------------------------

type rect struct{ x, y, w, h int }

// tile lays n panes out in as square a grid as fits, filling row by row.
func tile(n, W, H int) []rect {
	if n <= 0 {
		return nil
	}
	cols := int(math.Ceil(math.Sqrt(float64(n))))
	rows := int(math.Ceil(float64(n) / float64(cols)))

	out := make([]rect, 0, n)
	placed, y := 0, 0
	for r := 0; r < rows; r++ {
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

// --- keys --------------------------------------------------------------

func (m Model) activeIsSession() bool {
	return m.activeTab > 0 && m.activeTab < len(m.tabs) && !m.tabs[m.activeTab].isBrowser()
}

// handleSessionTabKey routes a key to the live session, except for the prefix
// which introduces the commands the remote would otherwise swallow.
func (m Model) handleSessionTabKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	t := m.tabs[m.activeTab]
	key := msg.String()

	if m.prefixArmed {
		return m.handlePrefix(key, msg)
	}
	if key == prefixKey {
		m.prefixArmed = true
		return m, nil
	}
	if t.broadcast {
		for _, p := range t.panes {
			if p.Alive() {
				p.SendKey(msg)
			}
		}
		return m, nil
	}
	if p := t.focused(); p != nil && p.Alive() {
		p.SendKey(msg)
	}
	return m, nil
}

// handlePrefix runs a workspace command. It is reachable from every tab, so
// switching tabs works the same way wherever you are.
func (m Model) handlePrefix(key string, msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.prefixArmed = false

	switch key {
	case prefixKey:
		if p := m.tabs[m.activeTab].focused(); p != nil {
			p.SendKey(msg) // pressed twice: the remote wanted it
		}
	case "n", "]", "tab":
		m.activeTab = (m.activeTab + 1) % len(m.tabs)
	case "p", "[", "shift+tab":
		m.activeTab = (m.activeTab + len(m.tabs) - 1) % len(m.tabs)
	case "w":
		m.activeTab = 0
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		if i := int(key[0] - '1'); i < len(m.tabs) {
			m.activeTab = i
		}
	case "x":
		return m.closeTab(false)
	case "X":
		return m.closeTab(true)
	case "o":
		if t := &m.tabs[m.activeTab]; len(t.panes) > 1 {
			t.focus = (t.focus + 1) % len(t.panes)
		}
	case "b":
		if t := &m.tabs[m.activeTab]; !t.isBrowser() {
			t.broadcast = !t.broadcast
			if t.broadcast {
				m.setStatus(fmt.Sprintf("broadcast ON — keys go to all %d panes", len(t.panes)))
			} else {
				m.setStatus("broadcast off")
			}
		}
	case "k", "up", "pgup":
		m.scrollFocused(-1)
		m.prefixArmed = true // stay armed so repeated presses page through
	case "j", "down", "pgdown":
		m.scrollFocused(1)
		m.prefixArmed = true
	case "G", "end":
		if p := m.tabs[m.activeTab].focused(); p != nil {
			p.ScrollToBottom()
			m.setStatus("live view")
		}
	case "r":
		// ctrl+l belongs to the remote shell, so the redraw lives here.
		return m, tea.ClearScreen
	}
	return m, nil
}

func (m *Model) scrollFocused(d int) {
	p := m.tabs[m.activeTab].focused()
	if p == nil {
		return
	}
	_, h := p.Size()
	page := max(h-1, 1)
	if d < 0 {
		p.ScrollUp(page)
	} else {
		p.ScrollDown(page)
	}
	if off, avail := p.ScrollOffset(); off > 0 {
		m.setStatus(fmt.Sprintf("scrolled back %d of %d lines — %s G for live", off, avail, prefixKey))
	} else {
		m.setStatus("live view")
	}
}

// closeTab detaches from the current tab's sessions, or ends them when kill is
// set. The browser tab is never closed — it is the way back to everything.
func (m Model) closeTab(kill bool) (tea.Model, tea.Cmd) {
	if m.activeTab == 0 || m.activeTab >= len(m.tabs) {
		return m, nil
	}
	t := m.tabs[m.activeTab]
	name := t.label()

	persistent := 0
	for _, p := range t.panes {
		m.recordPaneSession(p)
		if kill {
			p.Kill()
			continue
		}
		if p.Persistent() {
			persistent++
		}
		p.Close()
	}

	m.tabs = append(m.tabs[:m.activeTab], m.tabs[m.activeTab+1:]...)
	m.activeTab = clamp(m.activeTab-1, 0, len(m.tabs)-1)
	m.prefixArmed = false
	m.reload()

	switch {
	case kill:
		m.setStatus("ended " + name)
	case persistent > 0:
		m.setStatus(fmt.Sprintf("closed %s — %d session%s still running", name, persistent, plural(persistent)))
	default:
		m.setStatus("closed " + name)
	}
	return m, nil
}

// recordPaneSession keeps session history the same for panes as for handed-off
// sessions; a host you just worked on should not read as never connected.
func (m Model) recordPaneSession(p *term.Pane) {
	if p != nil {
		m.st.RecordSession(p.Host.StatKey(), time.Now())
	}
}

func (m Model) handlePaneTick() (tea.Model, tea.Cmd) {
	live := false
	w, h := m.contentSize()
	for _, t := range m.tabs {
		if t.isBrowser() {
			continue
		}
		live = true
		rects := tile(len(t.panes), w, h)
		for i, p := range t.panes {
			pw, ph := paneInner(rects[i])
			p.Resize(pw, ph)
		}
	}
	if !live {
		return m, nil
	}
	return m, paneTick()
}

// closePanesForExit releases every session, for teardown from main.
func (m Model) closePanesForExit() {
	for _, t := range m.tabs {
		for _, p := range t.panes {
			p.Close()
		}
	}
}

// --- rendering ---------------------------------------------------------

// tabBar is the row of tabs across the top.
func (m Model) tabBar() string {
	var parts []string
	for i, t := range m.tabs {
		label := fmt.Sprintf(" %d %s ", i+1, t.label())
		switch {
		case i == m.activeTab && m.prefixArmed:
			parts = append(parts, theme.Selected.Render(label))
		case i == m.activeTab:
			parts = append(parts, lipgloss.NewStyle().
				Foreground(theme.TextBrt).Background(theme.SelBg).Bold(true).Render(label))
		case t.isBrowser():
			parts = append(parts, theme.Dim.Render(label))
		default:
			parts = append(parts, theme.Fg(theme.Green).Render(label))
		}
	}
	bar := strings.Join(parts, theme.Dim.Render("│"))
	if m.prefixArmed {
		bar += theme.Fg(theme.Yellow).Render("  prefix…")
	}
	if w := ansi.StringWidth(bar); w < m.w {
		bar += strings.Repeat(" ", m.w-w)
	}
	return ansi.Truncate(bar, m.w, "…")
}

// sessionView renders the panes of the active tab.
func (m Model) sessionView(content int) string {
	t := m.tabs[m.activeTab]
	rects := tile(len(t.panes), m.w, content)

	boxes := make([]string, len(t.panes))
	for i, p := range t.panes {
		boxes[i] = box(m.sessionPaneTitle(t, i, p), i == t.focus, rects[i].w, rects[i].h, p.Render())
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

func (m Model) sessionPaneTitle(t tab, i int, p *term.Pane) string {
	name := p.Host.Name
	if !p.Alive() {
		return name + "  " + p.Status()
	}
	if t.broadcast {
		if i == t.focus {
			return name + "  " + theme.Fg(theme.Red).Render("BROADCAST") + scrollIndicator(p)
		}
		return name + "  " + theme.Fg(theme.Red).Render("broadcast") + scrollIndicator(p)
	}
	if i == t.focus && len(t.panes) == 1 {
		return name + "  " + theme.Dim.Render(prefixKey+" w for hosts") + scrollIndicator(p)
	}
	return name + scrollIndicator(p)
}

// scrollIndicator labels a pane that is not showing live output.
func scrollIndicator(p *term.Pane) string {
	off, avail := p.ScrollOffset()
	if off == 0 {
		return ""
	}
	return "  " + theme.Fg(theme.Yellow).Render(fmt.Sprintf("scrolled %d/%d", off, avail))
}

// sessionCursor puts the real cursor where the focused remote put it.
func (m Model) sessionCursor(content int) *tea.Cursor {
	if !m.activeIsSession() {
		return nil
	}
	t := m.tabs[m.activeTab]
	p := t.focused()
	if p == nil || !p.Alive() {
		return nil
	}
	rects := tile(len(t.panes), m.w, content)
	r := rects[clamp(t.focus, 0, len(rects)-1)]
	x, y := p.CursorPosition()
	return tea.NewCursor(r.x+x+2, r.y+y+1+tabBarHeight)
}
