package ui

import (
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/cuonggt/omassh/internal/keymap"
	"github.com/cuonggt/omassh/internal/store"
	"github.com/cuonggt/omassh/internal/term"
	"github.com/cuonggt/omassh/internal/ui/theme"
)

// paneFPS is how often a live session redraws. The emulator cannot say
// "something changed", so this polls; 20 a second is smooth for a terminal and
// cheap enough that an idle session costs nothing noticeable.
const paneFPS = 50 * time.Millisecond

// prefixKey introduces a session command. While a session has focus every
// keystroke belongs to the remote — ctrl+c included — so the commands need a
// namespace of their own, the way tmux uses ctrl+b.
const prefixKey = "ctrl+\\"

type paneTickMsg struct{}

func paneTick() tea.Cmd {
	return tea.Tick(paneFPS, func(time.Time) tea.Msg { return paneTickMsg{} })
}

// --- opening -----------------------------------------------------------

// attachSession connects to the selected host in the main pane, leaving the
// host list usable beside it.
func (m Model) attachSession() (tea.Model, tea.Cmd) {
	h, ok := m.selectedHost()
	if !ok {
		return m, nil
	}
	// Connecting somewhere else replaces the session rather than silently
	// accumulating connections the user cannot see.
	if m.attached != nil {
		m.recordPaneSession(m.attached)
		m.attached.Close()
		m.attached = nil
		// The host we just left keeps its session, so the list has to say so
		// straight away rather than at whatever reload happens next.
		m.reload()
	}

	w, ht := m.sessionArea()
	p, err := term.Open(m.d.resolver.Resolve(h).Host, w, ht)
	if err != nil {
		m.setErr(err)
		return m, nil
	}
	m.attached = p
	m.focus = panelSession
	m.prefixArmed = false
	m.setStatus("connected to " + h.Name + " — " + prefixKey + " w for the host list")
	return m, paneTick()
}

// sessionArea is the emulator size inside the main pane's border.
func (m Model) sessionArea() (int, int) {
	side := clamp(sidebarWidth, 20, m.w/2)
	return max(m.w-side-4, 20), max(m.h-statusHeight-2, 5)
}

// attachedTo reports whether the live session belongs to this host, so the
// list can mark it. This is always current, unlike data.live, which is only as
// fresh as the last reload.
func (m Model) attachedTo(h store.Host) bool {
	return m.attached != nil && m.attached.Alive() &&
		m.attached.Host.StatKey() == h.StatKey()
}

// --- keys --------------------------------------------------------------

// handleSessionKey routes a key to the remote, except for the prefix, which
// introduces the commands the remote would otherwise swallow.
func (m Model) handleSessionKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	if m.prefixArmed {
		m.prefixArmed = false
		switch key {
		case prefixKey:
			m.attached.SendKey(msg) // pressed twice: the remote wanted it
		case "w", "esc":
			m.attached.ScrollToBottom()
			m.focus = panelHosts
			m.setStatus("host list — " + m.attached.Host.Name + " still connected")
		case "d":
			return m.detachSession(m.detachMessage())
		case "X":
			return m.killSession()
		case "k", "up", "pgup":
			m.scrollAttached(-1)
			m.prefixArmed = true // stay armed so repeated presses page through
		case "j", "down", "pgdown":
			m.scrollAttached(1)
			m.prefixArmed = true
		case "G", "end":
			m.attached.ScrollToBottom()
			m.setStatus("live view")
		case "r":
			// ctrl+l belongs to the remote shell while a session has focus, so
			// the redraw lives behind the prefix instead.
			return m, tea.ClearScreen
		}
		return m, nil
	}
	if key == prefixKey {
		m.prefixArmed = true
		return m, nil
	}
	if !m.attached.Alive() {
		return m.detachSession(m.attached.Host.Name + " " + m.attached.Status())
	}
	m.attached.SendKey(msg)
	return m, nil
}

func (m *Model) scrollAttached(d int) {
	_, h := m.attached.Size()
	page := max(h-1, 1)
	if d < 0 {
		m.attached.ScrollUp(page)
	} else {
		m.attached.ScrollDown(page)
	}
	if off, avail := m.attached.ScrollOffset(); off > 0 {
		m.setStatus(fmt.Sprintf("scrolled back %d of %d lines — %s G for the live view", off, avail, prefixKey))
	} else {
		m.setStatus("live view")
	}
}

// --- closing -----------------------------------------------------------

// detachMessage reports what detaching actually did. Saying "closed" when the
// session is still running would be worse than saying nothing.
func (m Model) detachMessage() string {
	name := m.attached.Host.Name
	if m.attached.Persistent() {
		return "detached from " + name + " — session still running, " +
			m.keys.Key(keymap.Pane) + " to reattach"
	}
	return "disconnected from " + name
}

func (m Model) detachSession(reason string) (tea.Model, tea.Cmd) {
	if m.attached != nil {
		m.recordPaneSession(m.attached)
		m.attached.Close()
		m.attached = nil
		m.reload()
	}
	if m.focus == panelSession {
		m.focus = panelHosts
	}
	m.prefixArmed = false
	m.setStatus(reason)
	return m, nil
}

func (m Model) killSession() (tea.Model, tea.Cmd) {
	if m.attached == nil {
		return m, nil
	}
	name := m.attached.Host.Name
	m.recordPaneSession(m.attached)
	if err := m.attached.Kill(); err != nil {
		m.setErr(err)
	}
	m.attached = nil
	if m.focus == panelSession {
		m.focus = panelHosts
	}
	m.prefixArmed = false
	m.reload()
	m.setStatus("ended the session on " + name)
	return m, nil
}

// recordPaneSession keeps embedded sessions in the same history as handed-off
// ones; a host you just worked on should not read as never connected.
func (m Model) recordPaneSession(p *term.Pane) {
	if p != nil {
		m.st.RecordSession(p.Host.StatKey(), time.Now())
	}
}

// closePanesForExit releases the session, for teardown from main.
func (m Model) closePanesForExit() {
	if m.attached != nil {
		m.attached.Close()
	}
}

func (m Model) handlePaneTick() (tea.Model, tea.Cmd) {
	if m.attached == nil {
		return m, nil
	}
	w, h := m.sessionArea()
	m.attached.Resize(w, h)
	if !m.attached.Alive() && m.focus == panelSession {
		m.setStatus(m.attached.Host.Name + " " + m.attached.Status())
	}
	return m, paneTick()
}

// --- rendering ---------------------------------------------------------

func (m Model) sessionTitle() string {
	name := m.attached.Host.Name
	switch {
	case !m.attached.Alive():
		return name + "  " + m.attached.Status()
	case m.prefixArmed && m.focus == panelSession:
		return name + "  " + theme.Fg(theme.Yellow).Render("prefix…") + scrollIndicator(m.attached)
	case m.focus == panelSession:
		return name + "  " + theme.Dim.Render(prefixKey+" w for the host list") + scrollIndicator(m.attached)
	default:
		return name + "  " + theme.Fg(theme.Green).Render("connected") + scrollIndicator(m.attached)
	}
}

// scrollIndicator labels a session that is not showing live output.
func scrollIndicator(p *term.Pane) string {
	off, avail := p.ScrollOffset()
	if off == 0 {
		return ""
	}
	return "  " + theme.Fg(theme.Yellow).Render(fmt.Sprintf("scrolled %d/%d", off, avail))
}

// sessionCursor puts the real cursor where the remote put it.
func (m Model) sessionCursor() *tea.Cursor {
	if m.attached == nil || !m.attached.Alive() || m.focus != panelSession {
		return nil
	}
	side := clamp(sidebarWidth, 20, m.w/2)
	x, y := m.attached.CursorPosition()
	return tea.NewCursor(side+x+2, y+1)
}
