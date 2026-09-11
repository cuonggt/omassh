package ui

import (
	"fmt"
	"time"

	"github.com/charmbracelet/x/ansi"

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
	// Already connected here: this is someone coming back to the session, not
	// asking for another one. Reconnecting would close the pane first, and for
	// a plain ssh child that ends the shell they were in the middle of.
	if m.attachedTo(h) {
		m.focus = panelSession
		m.prefixArmed = false
		m.setStatusOf(h.Name, "back — "+prefixKey+" w for the host list")
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

	target := m.d.resolver.Resolve(h).Host
	// Asked before opening, since opening is what makes it true.
	shared := term.SessionAttached(term.SessionName(target))

	w, ht := m.sessionArea()
	p, err := term.Open(target, w, ht)
	if err != nil {
		m.setErr(err)
		return m, nil
	}
	m.attached = p
	m.focus = panelSession
	m.prefixArmed = false
	m.setStatusOf(h.Name, attachedMessage(shared))
	return m, paneTick()
}

// attachedMessage says what has just been connected to, and warns when the
// session is already open somewhere else.
//
// Two windows sharing a session mirror each other, which is what reattaching
// means and is often what you want. What is not obvious is the size: tmux
// gives the session to whichever client was last typed in, so the other window
// draws it short of its pane or clipped by it, with nothing to say why.
// The name is the subject and goes first when the bar is short of room: it is
// in the host list and in the pane's own title, while the way back out is not
// written anywhere else — and a session owns the keyboard, so someone who
// cannot see it has nothing to try.
func attachedMessage(shared bool) string {
	if shared {
		return "connected — also open in another window; its size follows whichever you type in"
	}
	return "connected — " + prefixKey + " w for the host list"
}

// sessionArea is the emulator size inside the main pane's border.
func (m Model) sessionArea() (int, int) {
	side := m.sidebar()
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

	if m.prefixArmed || key == prefixKey {
		return m.sessionCommand(msg)
	}
	// A session that has ended is dismissed deliberately, not by whatever you
	// were in the middle of typing. Detaching on any key sent the rest of a
	// half-typed command to the host list, where e opens an edit form and the
	// remainder lands in a host's name — saved by the enter meant for the
	// shell. The prefix still works, since it is handled above.
	if !m.attached.Alive() {
		switch key {
		case "esc", "enter":
			return m.detachSession(m.attached.Host.Name, m.attached.Status())
		}
		m.setStatus(endedMessage(m.attached))
		return m, nil
	}
	return m.typeIntoSession(msg)
}

// toRemote hands input to the session, and says so when doing that has brought
// the view back from a scroll.
//
// Anything sent to the pane scrolls it to the bottom, which is what stops a
// terminal sitting scrolled while your own output goes past. The status did
// not hear about it, so it went on reporting a position the screen had left —
// offering ctrl+\ G for a live view that was already live, beside a title
// that had correctly stopped saying so.
//
// Every way in goes through here, because there are three of them — typing, a
// literal prefix, and a paste — and fixing one at a time left the other two
// doing it.
func (m Model) toRemote(send func()) (tea.Model, tea.Cmd) {
	scrolled, _ := m.attached.ScrollOffset()
	send()
	if scrolled > 0 {
		m.setStatus("live view")
	}
	return m, nil
}

func (m Model) typeIntoSession(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	return m.toRemote(func() { m.attached.SendKey(msg) })
}

// sessionCommand arms the prefix, then runs the command that follows it.
//
// Both sides reach this. In the session pane the prefix is the only way to be
// heard over the remote, which takes every other key. From the host list it is
// how the session is reached at all: detaching and ending are facts about the
// session rather than about whichever panel happens to hold the keyboard, and
// getting to them used to mean going back into the pane first — where the
// prefix was silently ignored on the way, so ctrl+\ d on the list opened the
// delete confirmation for the highlighted host instead.
func (m Model) sessionCommand(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if !m.prefixArmed {
		m.prefixArmed = true
		return m, nil
	}
	// Whether the prefix was pressed or re-armed by a scroll, which decides
	// what an unrecognised key means.
	fromScroll := m.scrollArmed
	m.prefixArmed, m.scrollArmed = false, false

	switch msg.String() {
	case prefixKey:
		// Pressed twice: the remote wanted it. Only the pane can deliver it;
		// from the list there is nothing typing into.
		if m.focus == panelSession {
			return m.typeIntoSession(msg)
		}
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
		m.prefixArmed, m.scrollArmed = true, true // so repeated presses page
	case "j", "down", "pgdown":
		m.scrollAttached(1)
		m.prefixArmed, m.scrollArmed = true, true
	case "G", "end":
		m.attached.ScrollToBottom()
		m.setStatus("live view")
	case "r":
		// ctrl+l belongs to the remote shell while a session has focus, so
		// the redraw lives behind the prefix instead.
		return m, tea.ClearScreen
	default:
		// Armed by a scroll rather than pressed, so this is the next thing
		// someone typed, not a command. Dropping it cost the first letter of
		// whatever followed a look back through the output — `echo` arriving
		// as `cho` — for a prefix nobody had pressed. From the host list there
		// is nothing being typed into, so it is still dropped there.
		if fromScroll && m.focus == panelSession {
			return m.typeIntoSession(msg)
		}
	}
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

// detachMessage reports what detaching actually did, and the host it did it to
// separately: on a narrow bar the name goes and "still running, t to reattach"
// stays, since that is the part telling you the session is not lost.
func (m Model) detachMessage() (ctx, msg string) {
	return detachText(m.attached.Host.Name, m.attached.Persistent(), m.keys.Key(keymap.Pane))
}

// detachText is what detaching says, and about what. Saying "closed" when the
// session is still running would be worse than saying nothing, and the name
// travels separately so a narrow bar keeps the part that says it is not lost.
func detachText(name string, persistent bool, reattach string) (ctx, msg string) {
	if persistent {
		return name, "detached — session still running, " + reattach + " to reattach"
	}
	return name, "disconnected"
}

func (m Model) detachSession(ctx, reason string) (tea.Model, tea.Cmd) {
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
	m.setStatusOf(ctx, reason)
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
//
// The session is recorded on the way out, as detaching and ending one already
// do. Quitting with a session open is the ordinary way to leave the main pane,
// and without this the host went on reading "never connected" while the very
// session it was describing sat waiting to be reattached to.
func (m Model) closePanesForExit() {
	if m.attached != nil {
		m.recordPaneSession(m.attached)
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
		m.setStatus(endedMessage(m.attached))
	}
	return m, paneTick()
}

// endedMessage is what a session that has stopped says, and how to leave it.
//
// Written in one place because it is written from two, and the tick repeats
// itself twenty times a second: the key handler's version — the one carrying
// the way out — was replaced within fifty milliseconds by the tick's terser
// one, so the line saying esc returns to the list could not be read at any
// terminal width. A pane whose remote has gone owns the keyboard until it is
// dismissed, and nothing else on screen says how.
func endedMessage(p *term.Pane) string {
	return p.Host.Name + " " + p.Status() + " — esc to return to the list"
}

// --- rendering ---------------------------------------------------------

// sessionTitle names the session and says what to do with it, in a box that is
// as wide as the frame allows.
//
// The name is what gives way when there is not room for both. It is in the
// host list a few columns to the left, while "ctrl+\ w for the host list" is
// the way out of a pane that owns every keystroke — truncating the title from
// the right took that off first, so a host with a long name left no way out
// on screen at all.
func (m Model) sessionTitle(w int) string {
	plain, styled := m.sessionTail()
	return sessionTitleText(m.attached.Host.Name, plain, styled, w)
}

// sessionTitleText fits a name beside what follows it, eliding the name and
// dropping it entirely when there is no room worth having.
//
// box gives a title w-5 cells and the name is followed by two spaces, so what
// is left for the name is whatever the tail does not need. When that is almost
// nothing the name goes rather than the tail: which host this is has a mark
// against it in the list two columns to the left, while "ctrl+\ w for the host
// list" is the way out of a pane that swallows every keystroke, and is written
// nowhere else on a narrow screen.
func sessionTitleText(name, plain, styled string, w int) string {
	avail := w - 5 - ansi.StringWidth(plain) - 2
	if avail < 4 {
		return styled
	}
	return ansi.Truncate(name, avail, "…") + "  " + styled
}

// sessionTail is what follows the name, as plain text for measuring and as
// styled text for drawing.
func (m Model) sessionTail() (plain, styled string) {
	bare, lit := scrollIndicator(m.attached)
	switch {
	case !m.attached.Alive():
		return m.attached.Status(), m.attached.Status()
	case m.prefixArmed:
		return "prefix…" + bare, theme.Fg(theme.Yellow).Render("prefix…") + lit
	case m.focus == panelSession:
		out := prefixKey + " w for the host list"
		return out + bare, theme.Dim.Render(out) + lit
	default:
		return "connected" + bare, theme.Fg(theme.Green).Render("connected") + lit
	}
}

// scrollIndicator labels a session that is not showing live output, plainly
// for measuring and styled for drawing — a title has to be sized before it is
// coloured.
func scrollIndicator(p *term.Pane) (plain, styled string) {
	off, avail := p.ScrollOffset()
	if off == 0 {
		return "", ""
	}
	s := fmt.Sprintf("scrolled %d/%d", off, avail)
	return "  " + s, "  " + theme.Fg(theme.Yellow).Render(s)
}

// sessionCursor puts the real cursor where the remote put it.
func (m Model) sessionCursor() *tea.Cursor {
	if m.attached == nil || !m.attached.Alive() || m.focus != panelSession {
		return nil
	}
	side := m.sidebar()
	x, y := m.attached.CursorPosition()
	return tea.NewCursor(side+x+2, y+1)
}
