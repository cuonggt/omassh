// Package term runs an SSH session inside an embedded terminal pane.
//
// This is the alternative to the full-screen handoff in internal/sshx. The
// handoff gives a real terminal to a real ssh and is perfect by construction;
// a pane emulates one, which is what lets a session live in a tab alongside
// the rest of the interface, at the cost of owning every emulation detail.
// Both exist, and the handoff remains the highest-fidelity path for that
// reason.
package term

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vt"
	"github.com/charmbracelet/x/xpty"

	"github.com/cuonggt/omassh/internal/sshx"
	"github.com/cuonggt/omassh/internal/store"
)

// scrollback is how many lines above the viewport each pane keeps.
const scrollback = 2000

// termVar is what a pane's child is told it is talking to. That is our
// emulator, not the user's terminal, so it gets what the emulator actually
// implements rather than inheriting a TERM that promises more.
const termVar = "TERM=xterm-256color"

// Pane is one embedded session: a pty running ssh, a terminal emulator fed by
// its output, and the plumbing to send keys back.
type Pane struct {
	Host store.Host

	em  *vt.SafeEmulator
	pty xpty.Pty
	cmd *exec.Cmd

	mu     sync.Mutex
	w, h   int
	scroll int // lines scrolled up from the live view

	// emMu serialises emulator mutation against scrollback reads.
	// SafeEmulator locks each of its own methods, but Scrollback() returns a
	// raw pointer after releasing the lock, so walking those lines while the
	// pty goroutine is writing is a data race. Every write goes through this.
	emMu     sync.RWMutex
	exited   bool
	exitErr  error
	exitCode int

	closeOnce sync.Once
	done      chan struct{}

	// changed is signalled whenever output reaches the emulator, and closed
	// once there will be no more. The emulator cannot say that the screen
	// moved, but the goroutine feeding it can, so the interface redraws when
	// there is something new rather than on a timer — which drew twenty times
	// a second whether anything had happened or not, and showed a character
	// typed a moment after a tick up to fifty milliseconds after it had come
	// back. One slot: a burst of output is one redraw, not one per read.
	changed chan struct{}

	// session is the tmux session backing this pane, empty when running ssh
	// directly. Persistent panes detach on close instead of ending.
	session string

	// tmuxOffset and tmuxHistory are how far back tmux has the view scrolled,
	// and how much history it holds, as it said after the last scroll omassh
	// made — the only thing that moves it. Asking is a process launch, around
	// seven milliseconds, and it was asked twice for every key typed and once
	// for every frame drawn; keys queued behind tmux launches, and an idle
	// session launched twenty a second just to draw its title. Guarded by mu.
	tmuxOffset, tmuxHistory int
}

// Open starts an ssh session for h inside a w x h pane.
func Open(h store.Host, w, height int) (*Pane, error) {
	if w < 2 || height < 2 {
		return nil, fmt.Errorf("pane is too small")
	}

	pty, err := xpty.NewPty(w, height)
	if err != nil {
		return nil, fmt.Errorf("allocate pty: %w", err)
	}

	cmd, session := sessionCommand(h, TmuxAvailable())
	attachTTY(cmd)

	if err := pty.Start(cmd); err != nil {
		pty.Close()
		return nil, fmt.Errorf("start ssh: %w", err)
	}

	p := &Pane{
		Host:    h,
		session: session,
		em:      vt.NewSafeEmulator(w, height),
		pty:     pty,
		cmd:     cmd,
		w:       w,
		h:       height,
		done:    make(chan struct{}),
		changed: make(chan struct{}, 1),
	}
	p.em.SetScrollbackSize(scrollback)
	// A session being reattached can still be scrolled back from the window
	// that last had it, and typing into that would land in tmux's copy mode.
	if session != "" {
		p.recordTmuxScroll()
	}

	// Remote output into the emulator, under the pane's lock so a concurrent
	// scrollback read cannot observe a half-applied update. This goroutine is
	// the only one that signals changed, so it is also the one that closes it.
	go func() {
		defer close(p.changed)
		buf := make([]byte, 32*1024)
		for {
			n, err := p.pty.Read(buf)
			if n > 0 {
				p.emMu.Lock()
				p.em.Write(buf[:n])
				p.emMu.Unlock()
				select {
				case p.changed <- struct{}{}:
				default:
				}
			}
			if err != nil {
				return
			}
		}
	}()
	// Whatever the emulator wants to say back — encoded keys, replies to
	// device queries — goes to the pty. Its Read blocks, so it needs a
	// goroutine of its own.
	go func() {
		io.Copy(p.pty, p.em)
	}()
	// Reap the child so the pane can report that the session ended.
	go func() {
		err := p.cmd.Wait()
		p.mu.Lock()
		p.exited = true
		p.exitErr = err
		if ee, ok := err.(*exec.ExitError); ok {
			p.exitCode = ee.ExitCode()
			p.exitErr = nil
		}
		p.mu.Unlock()
		close(p.done)
	}()

	return p, nil
}

// sessionCommand is what a pane runs: a persistent tmux-backed session where
// tmux is installed, and a plain ssh child where it is not, which simply means
// sessions end with the UI.
//
// The two put a password credential's environment in different places, and the
// difference is load-bearing. The tmux command carries it in its own argv,
// through env(1), because setting it on the Cmd would put it on tmux rather
// than on the ssh tmux goes on to run, and because the session outlives omassh
// anyway. The plain child has nowhere to put it but the Cmd.
//
// One command each, rather than a plain one the tmux branch overwrites: TERM
// used to be assigned after that overwrite, and so replaced the credential
// environment outright. A machine without tmux then prompted in the pane for a
// password it had already been given. Nothing failed and nothing was logged;
// the stored password was simply never reached.
func sessionCommand(h store.Host, tmuxOK bool) (*exec.Cmd, string) {
	sshArgs := sshx.Build(h)
	env := sshx.AttendedEnv(h)

	if tmuxOK {
		if cmd, name, err := tmuxCommand(h, sshArgs, env); err == nil {
			// Deliberately without env: this tmux server is shared with every
			// other session, and a credential in the environment that starts
			// it would reach panes it has nothing to do with.
			cmd.Env = append(os.Environ(), termVar)
			return cmd, name
		}
	}

	cmd := exec.Command("ssh", sshArgs...)
	cmd.Env = append(os.Environ(), env...)
	cmd.Env = append(cmd.Env, termVar)
	return cmd, ""
}

// SendKey forwards a key press to the remote session.
//
// Printable input is sent as text rather than through the key encoder. Bubble
// Tea reports an uppercase letter as shift plus the base key, and the encoder
// emits nothing at all for that combination — so typing anything capitalised
// silently vanished. A real terminal sends printable characters as
// characters; only control keys need encoding, which is what Shift being the
// sole modifier distinguishes.
func (p *Pane) SendKey(k tea.KeyPressMsg) {
	p.ScrollToBottom()
	if k.Text != "" && k.Mod&^tea.ModShift == 0 {
		p.em.SendText(k.Text)
		return
	}
	p.em.SendKey(toUV(k))
}

// SendText writes a string to the session as literal input: text the terminal
// delivers in one piece rather than as key presses.
func (p *Pane) SendText(s string) {
	p.ScrollToBottom()
	p.em.SendText(s)
}

// Paste delivers text the way a terminal delivers a paste, which for anything
// with a newline in it is a different thing from typing it.
//
// Typed, every line runs as it arrives: a four-line script becomes four
// commands, and the first of them is often the one that makes the rest wrong.
// Bracketing says "this arrived in one piece", and a shell that understands
// bracketed paste — bash through readline, zsh, fish — puts the whole of it in
// its edit buffer for the person to read and run themselves.
//
// A remote that has not asked for bracketed paste gets the text as it is,
// which is also what a real terminal does: its shell then runs each line as it
// arrives. Whether that is what happens cannot be known from here. The mode
// belongs to the shell at the far end, and with tmux in between it is tmux's
// own that reaches this emulator — tmux turns bracketed paste on for its
// client whatever the pane is running, keeps the pane's real mode to itself,
// and exposes no format variable holding it. A watcher on the output stream
// was written, and it read tmux every time.
func (p *Pane) Paste(s string) {
	p.ScrollToBottom()
	p.em.Paste(s)
}

// toUV converts a Bubble Tea key press to the ultraviolet event the emulator
// encodes. The two structs are field-identical; only the named type differs.
func toUV(k tea.KeyPressMsg) uv.KeyPressEvent {
	return uv.KeyPressEvent{
		Text:        k.Text,
		Mod:         uv.KeyMod(k.Mod),
		Code:        k.Code,
		ShiftedCode: k.ShiftedCode,
		BaseCode:    k.BaseCode,
		IsRepeat:    k.IsRepeat,
	}
}

// Resize tells both the emulator and the remote about a new pane size.
func (p *Pane) Resize(w, h int) {
	if w < 2 || h < 2 {
		return
	}
	p.mu.Lock()
	same := w == p.w && h == p.h
	p.w, p.h = w, h
	p.mu.Unlock()
	if same {
		return
	}
	p.emMu.Lock()
	p.em.Resize(w, h)
	p.emMu.Unlock()
	// Without this the remote keeps drawing to the old geometry, and anything
	// full-screen renders into the wrong shape.
	p.pty.Resize(w, h)
}

// Render returns the pane's screen as a styled string. When scrolled back it
// composes the visible window from scrollback lines followed by the top of the
// live screen, so the join is seamless.
func (p *Pane) Render() string {
	p.emMu.RLock()
	defer p.emMu.RUnlock()

	p.mu.Lock()
	off, height := p.scroll, p.h
	p.mu.Unlock()

	if off <= 0 {
		return p.em.Render()
	}

	sb := p.em.Scrollback()
	n := sb.Len()
	if off > n {
		off = n
	}
	live := strings.Split(p.em.Render(), "\n")

	out := make([]string, 0, height)
	start := n - off
	for i := range height {
		switch idx := start + i; {
		case idx < 0 || idx >= n+len(live):
			out = append(out, "")
		case idx < n:
			out = append(out, sb.Line(idx).Render())
		default:
			out = append(out, live[idx-n])
		}
	}
	return strings.Join(out, "\n")
}

// ScrollUp moves the view back through the scrollback.
func (p *Pane) ScrollUp(lines int) {
	if p.session != "" {
		tmuxCopyScroll(p.session, true, lines)
		p.recordTmuxScroll()
		return
	}
	p.emMu.RLock()
	max := p.em.ScrollbackLen()
	p.emMu.RUnlock()

	p.mu.Lock()
	defer p.mu.Unlock()
	p.scroll = min(p.scroll+lines, max)
}

// ScrollDown moves the view back toward the live screen.
func (p *Pane) ScrollDown(lines int) {
	if p.session != "" {
		// Paging down to the bottom leaves copy mode by itself, which is why
		// tmux is asked where things stand rather than told.
		tmuxCopyScroll(p.session, false, lines)
		p.recordTmuxScroll()
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.scroll = max(p.scroll-lines, 0)
}

// ScrollToBottom returns to the live view. Sending input calls this, because a
// terminal that stayed scrolled while you typed would hide your own output.
//
// For a tmux session that means leaving copy mode, which has to happen before
// the key is sent — tmux would otherwise take it as a copy-mode command — but
// only when the view is actually scrolled. It used to be done for every key,
// scrolled or not, at the cost of a tmux launch each time.
func (p *Pane) ScrollToBottom() {
	if p.session != "" {
		p.mu.Lock()
		scrolled := p.tmuxOffset > 0
		p.tmuxOffset = 0
		p.mu.Unlock()
		if scrolled {
			tmuxCopyCancel(p.session)
		}
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.scroll = 0
}

// recordTmuxScroll asks tmux where the view now stands.
func (p *Pane) recordTmuxScroll() {
	off, avail := tmuxScrollPosition(p.session)
	p.mu.Lock()
	p.tmuxOffset, p.tmuxHistory = off, avail
	p.mu.Unlock()
}

// ScrollOffset is how many lines back the view is, and how many exist.
//
// For a tmux session both are as tmux gave them after the last scroll omassh
// made. Nothing else moves the view, and the count is only ever shown beside
// a view that has just been scrolled, so it is not asked for in between —
// that would be a tmux launch each time, which is what this avoids.
func (p *Pane) ScrollOffset() (offset, available int) {
	if p.session != "" {
		p.mu.Lock()
		defer p.mu.Unlock()
		return p.tmuxOffset, p.tmuxHistory
	}
	p.emMu.RLock()
	available = p.em.ScrollbackLen()
	p.emMu.RUnlock()
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.scroll, available
}

// CursorPosition is where the remote put the cursor, in pane coordinates.
func (p *Pane) CursorPosition() (x, y int) {
	pos := p.em.CursorPosition()
	return pos.X, pos.Y
}

// Size reports the pane's current dimensions.
func (p *Pane) Size() (w, h int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.w, p.h
}

// Alive reports whether the session is still running.
func (p *Pane) Alive() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return !p.exited
}

// Status describes how the session ended, for the pane header.
func (p *Pane) Status() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch {
	case !p.exited:
		return "connected"
	case p.exitErr != nil:
		return "failed: " + p.exitErr.Error()
	case p.exitCode != 0:
		return fmt.Sprintf("exited %d", p.exitCode)
	default:
		return "session ended"
	}
}

// Done is closed when the session ends.
func (p *Pane) Done() <-chan struct{} { return p.done }

// Changed receives when there is new output to draw, and is closed when the
// output has ended — with the session, or with Close.
func (p *Pane) Changed() <-chan struct{} { return p.changed }

// Persistent reports whether the session outlives this pane.
func (p *Pane) Persistent() bool { return p.session != "" }

// Session is the tmux session backing this pane, if any.
func (p *Pane) Session() string { return p.session }

// Close releases the pane. For a persistent session this detaches — the tmux
// server keeps the session running, and reconnecting reattaches to it. For a
// plain ssh child there is nothing to detach from, so it ends.
func (p *Pane) Close() error {
	var err error
	p.closeOnce.Do(func() {
		if p.cmd != nil && p.cmd.Process != nil {
			// Killing the client detaches; the session belongs to the server.
			p.cmd.Process.Signal(os.Interrupt)
			select {
			case <-p.done:
			case <-time.After(time.Second):
				p.cmd.Process.Kill()
			}
		}
		err = p.pty.Close()
	})
	return err
}

// Kill ends the underlying session outright, rather than detaching from it.
//
// The client is closed first. Killing the session while our own client is
// still starting loses the race: `new-session -A` creates when missing, so the
// client would promptly recreate the session that was just removed.
func (p *Pane) Kill() error {
	if p.session == "" {
		return p.Close()
	}
	p.Close()
	return KillSession(p.session)
}
