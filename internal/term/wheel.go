package term

import (
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
)

// The wheel over a session goes where a terminal would send it.
//
// A shell's is the session's history, scrolled back through. A program that
// has taken the whole screen — less, man, vim — has no history above it worth
// the name: the lines there are whatever the shell said before it started.
// Scrolling into them stacked the shell's old output over the program, which
// went on showing the same page. A terminal gives such a program arrow keys
// for the wheel instead, and a program that asked for the mouse — vim with
// mouse=a, htop — the wheel itself, which is what this does too.

// wheelGoes is where a notch of the wheel is delivered.
type wheelGoes int

const (
	// toHistory is the session's own history, which the caller scrolls.
	toHistory wheelGoes = iota
	// asArrows is the program, as the arrow keys a terminal sends for it.
	asArrows
	// asWheel is the program, which asked for the mouse.
	asWheel
)

// mouseModes are the modes a program asks for the mouse with — presses,
// drags, all motion — as tmux counts them for its mouse_any_flag.
var mouseModes = []ansi.Mode{ansi.ModeMouseNormal, ansi.ModeMouseButtonEvent, ansi.ModeMouseAnyEvent}

// Wheel turns the wheel a notch over a cell of the pane, and reports whether
// the program in the session took it. When it did not, the notch belongs to
// the session's history, which is the caller's to scroll — as it is whenever
// the view is already scrolled back, whatever is running.
//
// A program that asked for the mouse gets one notch as one wheel event, and
// scrolls by however much it scrolls for one. A program on the alternate
// screen gets lines arrow keys, the distance a notch moves the history.
func (p *Pane) Wheel(x, y int, up bool, lines int) bool {
	switch p.wheelGoes() {
	case asWheel:
		b := uv.MouseWheelDown
		if up {
			b = uv.MouseWheelUp
		}
		p.em.SendMouse(uv.MouseWheelEvent{X: x, Y: y, Button: b})
	case asArrows:
		k := uv.KeyPressEvent{Code: uv.KeyDown}
		if up {
			k.Code = uv.KeyUp
		}
		for range max(lines, 1) {
			p.em.SendKey(k)
		}
	default:
		return false
	}
	return true
}

// wheelGoes decides where the next notch goes.
//
// With tmux in between that costs asking tmux, once a notch — but only from
// the live view. Scrolled back, the answer is the history without asking, so
// a shell pays for it once, on the notch that leaves the live view.
func (p *Pane) wheelGoes() wheelGoes {
	if !p.Alive() {
		return toHistory // nothing is running to give it to
	}
	if p.session != "" {
		p.mu.Lock()
		scrolled := p.tmuxOffset > 0
		p.mu.Unlock()
		if scrolled {
			return toHistory
		}
		inMode, alternate, mouse, ok := tmuxProgram(p.session)
		switch {
		case !ok, inMode:
			// Copy mode is the history, and a drag selecting in it goes on
			// selecting as the wheel scrolls it.
			return toHistory
		case mouse:
			return asWheel
		case alternate:
			return asArrows
		}
		return toHistory
	}

	p.mu.Lock()
	scrolled := p.scroll > 0
	p.mu.Unlock()
	switch {
	case scrolled:
		return toHistory
	case p.mouse.Load() != 0:
		return asWheel
	case p.em.IsAltScreen():
		return asArrows
	}
	return toHistory
}

// followProgramMouse keeps p.mouse in step with the mouse modes the program
// has turned on, one bit each.
//
// Only a pane without tmux needs to. Its emulator is the program's terminal,
// so it hears the program ask; it keeps the modes to itself, but says when one
// changes. Everything that changes them happens on the output goroutine, so
// nothing else writes the bits.
func (p *Pane) followProgramMouse() {
	turn := func(m ansi.Mode, on bool) {
		for i, mm := range mouseModes {
			if m != mm {
				continue
			}
			bits := p.mouse.Load() &^ (1 << i)
			if on {
				bits |= 1 << i
			}
			p.mouse.Store(bits)
		}
	}
	p.em.SetCallbacks(vt.Callbacks{
		EnableMode:  func(m ansi.Mode) { turn(m, true) },
		DisableMode: func(m ansi.Mode) { turn(m, false) },
	})
	// A full reset — what `reset` sends — puts every mode back at once,
	// without saying so mode by mode, and the bits would go on claiming a
	// mouse nobody wants. The emulator's handlers run newest first, so this
	// sees the reset before the emulator's own handler does, and leaves that
	// one to carry it out.
	p.em.RegisterEscHandler('c', func() bool {
		p.mouse.Store(0)
		return false
	})
}
