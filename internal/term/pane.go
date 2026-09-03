// Package term runs an SSH session inside an embedded terminal pane.
//
// This is the alternative to the full-screen handoff in internal/sshx. The
// handoff gives a real terminal to a real ssh and is perfect by construction;
// a pane emulates one, which is what makes splits and side-by-side views
// possible, at the cost of owning every emulation detail. Both exist, and the
// handoff remains the default for that reason.
package term

import (
	"fmt"
	"io"
	"os"
	"os/exec"
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

	mu       sync.Mutex
	w, h     int
	exited   bool
	exitErr  error
	exitCode int

	closeOnce sync.Once
	done      chan struct{}
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

	cmd := exec.Command("ssh", sshx.Build(h)...)
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
		Host: h,
		em:   vt.NewSafeEmulator(w, height),
		pty:  pty,
		cmd:  cmd,
		w:    w,
		h:    height,
		done: make(chan struct{}),
	}
	p.em.SetScrollbackSize(scrollback)

	// Remote output into the emulator.
	go func() {
		io.Copy(p.em, p.pty)
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
func (p *Pane) SendKey(k tea.KeyPressMsg) {
	p.em.SendKey(toUV(k))
}

// SendText pastes a string into the session, used for broadcast.
func (p *Pane) SendText(s string) { p.em.SendText(s) }

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
	p.em.Resize(w, h)
	// Without this the remote keeps drawing to the old geometry, and anything
	// full-screen renders into the wrong shape.
	p.pty.Resize(w, h)
}

// Render returns the pane's screen as a styled string.
func (p *Pane) Render() string { return p.em.Render() }

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

// Close ends the session and releases the pty.
func (p *Pane) Close() error {
	var err error
	p.closeOnce.Do(func() {
		if p.cmd != nil && p.cmd.Process != nil {
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
