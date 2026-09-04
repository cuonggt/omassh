package ui

import (
	"fmt"
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

// tabBarHeight is the row the tab bar occupies.
const tabBarHeight = 1

type paneTickMsg struct{}

func paneTick() tea.Cmd {
	return tea.Tick(paneFPS, func(time.Time) tea.Msg { return paneTickMsg{} })
}

// tab is one entry in the tab bar. The first tab holds no pane and shows the
// host browser; every other tab is one live session filling the frame. One
// session per tab is the whole model — tabs are how you work on several hosts,
// so a tab never needs to be subdivided.
type tab struct {
	pane *term.Pane
}

func (t tab) isBrowser() bool { return t.pane == nil }

// label is what the tab bar shows.
func (t tab) label() string {
	if t.isBrowser() {
		return "hosts"
	}
	return t.pane.Host.Name
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
	if i := m.tabForHost(h); i > 0 {
		m.activeTab = i - 1
		m.setStatus("switched to " + h.Name)
		return m, paneTick()
	}

	pw, ph := paneInner(m.contentSize())
	p, err := term.Open(m.d.resolver.Resolve(h).Host, pw, ph)
	if err != nil {
		m.setErr(fmt.Errorf("could not open a session to %s: %w", h.Name, err))
		return m, nil
	}

	m.tabs = append(m.tabs, tab{pane: p})
	m.activeTab = len(m.tabs) - 1
	m.prefixArmed = false
	m.setStatus(fmt.Sprintf("%s — %s n/p to switch tabs", h.Name, prefixKey))
	return m, paneTick()
}

// tabForHost reports the 1-based tab number a host is already open in, or 0.
// Tabs are authoritative for what is live right now, unlike data.live, which
// is only as fresh as the last reload.
func (m Model) tabForHost(h store.Host) int {
	for i, t := range m.tabs {
		if !t.isBrowser() && t.pane.Host.StatKey() == h.StatKey() && t.pane.Alive() {
			return i + 1
		}
	}
	return 0
}

// contentSize is the area below the tab bar and above the status bar.
func (m Model) contentSize() (int, int) {
	return max(m.w, 20), max(m.h-statusHeight-tabBarHeight, 6)
}

// paneInner is the room left inside a session's border.
func paneInner(w, h int) (int, int) { return max(w-4, 10), max(h-2, 3) }

// --- keys --------------------------------------------------------------

func (m Model) activeIsSession() bool {
	return m.activeTab > 0 && m.activeTab < len(m.tabs) && !m.tabs[m.activeTab].isBrowser()
}

// active is the pane of the current tab, nil on the browser tab.
func (m Model) active() *term.Pane {
	if m.activeTab < 0 || m.activeTab >= len(m.tabs) {
		return nil
	}
	return m.tabs[m.activeTab].pane
}

// handleSessionTabKey routes a key to the live session, except for the prefix
// which introduces the commands the remote would otherwise swallow.
func (m Model) handleSessionTabKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	if m.prefixArmed {
		return m.handlePrefix(key, msg)
	}
	if key == prefixKey {
		m.prefixArmed = true
		return m, nil
	}
	if p := m.active(); p != nil && p.Alive() {
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
		if p := m.active(); p != nil {
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
	case "k", "up", "pgup":
		m.scrollActive(-1)
		m.prefixArmed = true // stay armed so repeated presses page through
	case "j", "down", "pgdown":
		m.scrollActive(1)
		m.prefixArmed = true
	case "G", "end":
		if p := m.active(); p != nil {
			p.ScrollToBottom()
			m.setStatus("live view")
		}
	case "r":
		// ctrl+l belongs to the remote shell, so the redraw lives here.
		return m, tea.ClearScreen
	}
	return m, nil
}

func (m *Model) scrollActive(d int) {
	p := m.active()
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

// closeTab detaches from the current tab's session, or ends it when kill is
// set. The browser tab is never closed — it is the way back to everything.
func (m Model) closeTab(kill bool) (tea.Model, tea.Cmd) {
	if m.activeTab == 0 || m.activeTab >= len(m.tabs) {
		return m, nil
	}
	t := m.tabs[m.activeTab]
	name := t.label()

	m.recordPaneSession(t.pane)
	persistent := false
	if kill {
		t.pane.Kill()
	} else {
		persistent = t.pane.Persistent()
		t.pane.Close()
	}

	m.tabs = append(m.tabs[:m.activeTab], m.tabs[m.activeTab+1:]...)
	m.activeTab = clamp(m.activeTab-1, 0, len(m.tabs)-1)
	m.prefixArmed = false
	m.reload()

	switch {
	case kill:
		m.setStatus("ended " + name)
	case persistent:
		m.setStatus("closed " + name + " — its session is still running")
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
	pw, ph := paneInner(w, h)
	for _, t := range m.tabs {
		if t.isBrowser() {
			continue
		}
		live = true
		t.pane.Resize(pw, ph)
	}
	if !live {
		return m, nil
	}
	return m, paneTick()
}

// closePanesForExit releases every session, for teardown from main.
func (m Model) closePanesForExit() {
	for _, t := range m.tabs {
		if !t.isBrowser() {
			t.pane.Close()
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

// sessionView renders the active tab's session, filling the frame.
func (m Model) sessionView(content int) string {
	p := m.active()
	return box(m.sessionPaneTitle(p), true, m.w, content, p.Render())
}

func (m Model) sessionPaneTitle(p *term.Pane) string {
	if !p.Alive() {
		return p.Host.Name + "  " + p.Status()
	}
	return p.Host.Name + "  " + theme.Dim.Render(prefixKey+" w for hosts") + scrollIndicator(p)
}

// scrollIndicator labels a pane that is not showing live output.
func scrollIndicator(p *term.Pane) string {
	off, avail := p.ScrollOffset()
	if off == 0 {
		return ""
	}
	return "  " + theme.Fg(theme.Yellow).Render(fmt.Sprintf("scrolled %d/%d", off, avail))
}

// sessionCursor puts the real cursor where the remote put it.
func (m Model) sessionCursor() *tea.Cursor {
	if !m.activeIsSession() {
		return nil
	}
	p := m.active()
	if p == nil || !p.Alive() {
		return nil
	}
	x, y := p.CursorPosition()
	return tea.NewCursor(x+2, y+1+tabBarHeight)
}
