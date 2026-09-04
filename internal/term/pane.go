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

	// session is the tmux session backing this pane, empty when running ssh
	// directly. Persistent panes detach on close instead of ending.
	session string
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

	// Prefer a persistent tmux-backed session; fall back to a plain ssh child
	// where tmux is not installed, which simply means sessions end with the UI.
	sshArgs := sshx.Build(h)
	cmd := exec.Command("ssh", sshArgs...)
	session := ""
	if TmuxAvailable() {
		if c, name, err := tmuxCommand(h, sshArgs); err == nil {
			cmd, session = c, name
		}
	}
	// The child is talking to our emulator, not the user's terminal, so it is
	// told what the emulator actually implements rather than inheriting a TERM
	// that promises more.
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
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
	}
	p.em.SetScrollbackSize(scrollback)

	// Remote output into the emulator, under the pane's lock so a concurrent
	// scrollback read cannot observe a half-applied update.
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := p.pty.Read(buf)
			if n > 0 {
				p.emMu.Lock()
				p.em.Write(buf[:n])
				p.emMu.Unlock()
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
		tmuxCopyScroll(p.session, true)
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
		tmuxCopyScroll(p.session, false)
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.scroll = max(p.scroll-lines, 0)
}

// ScrollToBottom returns to the live view. Sending input calls this, because a
// terminal that stayed scrolled while you typed would hide your own output.
func (p *Pane) ScrollToBottom() {
	if p.session != "" {
		tmuxCopyCancel(p.session)
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.scroll = 0
}

// ScrollOffset is how many lines back the view is, and how many exist.
func (p *Pane) ScrollOffset() (offset, available int) {
	if p.session != "" {
		return tmuxScrollPosition(p.session)
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
