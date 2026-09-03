package ui

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/cuonggt/omassh/internal/term"
	"github.com/cuonggt/omassh/internal/ui/theme"
)

// paneFPS is how often an open pane redraws. The emulator has no way to say
// "something changed", so this polls; 20 a second is smooth for a terminal and
// cheap enough that an idle pane costs nothing noticeable.
const paneFPS = 50 * time.Millisecond

// detachKey returns to the host list from inside a pane. Nearly every other
// key belongs to the remote — including ctrl+c — so the way out has to be
// something a shell almost never wants.
const detachKey = "ctrl+\\"

type paneTickMsg struct{}

func paneTick() tea.Cmd {
	return tea.Tick(paneFPS, func(time.Time) tea.Msg { return paneTickMsg{} })
}

// openPane starts an embedded session for the selected host.
func (m Model) openPane() (tea.Model, tea.Cmd) {
	h, ok := m.selectedHost()
	if !ok {
		return m, nil
	}
	w, ht := m.paneSize()
	p, err := term.Open(m.d.resolver.Resolve(h).Host, w, ht)
	if err != nil {
		m.setErr(err)
		return m, nil
	}
	m.pane = p
	m.mode = modePane
	m.setStatus("attached to " + h.Name + " — " + detachKey + " to detach")
	return m, paneTick()
}

// paneSize is the emulator geometry for the current window.
func (m Model) paneSize() (int, int) {
	return max(m.w-4, 20), max(m.h-statusHeight-2, 5)
}

func (m Model) closePane(reason string) (tea.Model, tea.Cmd) {
	if m.pane != nil {
		// A pane session is a session: record it the same way the handoff
		// does, or the host reads as never connected after you just used it.
		if err := m.st.RecordSession(m.pane.Host.StatKey(), time.Now()); err != nil {
			m.setErr(err)
		}
		m.pane.Close()
		m.pane = nil
		m.reload()
	}
	m.mode = modeBrowse
	m.setStatus(reason)
	return m, nil
}

func (m Model) handlePaneKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.pane == nil {
		m.mode = modeBrowse
		return m, nil
	}
	// The session is over; any key returns rather than typing into nothing.
	if !m.pane.Alive() {
		return m.closePane(m.pane.Host.Name + " " + m.pane.Status())
	}
	if msg.String() == detachKey {
		return m.closePane("detached from " + m.pane.Host.Name + " — session closed")
	}
	m.pane.SendKey(msg)
	return m, nil
}

func (m Model) handlePaneTick() (tea.Model, tea.Cmd) {
	if m.pane == nil || m.mode != modePane {
		return m, nil
	}
	// Keep the emulator matched to the window; a resize that never reaches the
	// remote leaves full-screen programs drawing to the wrong shape.
	w, h := m.paneSize()
	m.pane.Resize(w, h)

	if !m.pane.Alive() {
		// Leave the final screen up and say so, rather than yanking it away.
		m.setStatus(m.pane.Host.Name + " " + m.pane.Status() + " — any key to return")
	}
	return m, paneTick()
}

// paneView renders the embedded session.
func (m Model) paneView(content int) string {
	if m.pane == nil {
		return ""
	}
	title := m.pane.Host.Name + "  " + m.pane.Status()
	if m.pane.Alive() {
		title = m.pane.Host.Name + "  " + theme.Dim.Render(detachKey+" to detach")
	}
	return box(title, true, m.w, content, m.pane.Render())
}

// paneCursor places the terminal's real cursor where the remote put it, so the
// embedded session looks like a terminal rather than a picture of one.
func (m Model) paneCursor() *tea.Cursor {
	if m.pane == nil || !m.pane.Alive() {
		return nil
	}
	x, y := m.pane.CursorPosition()
	// Offset by the box border and padding.
	return tea.NewCursor(x+2, y+1)
}
