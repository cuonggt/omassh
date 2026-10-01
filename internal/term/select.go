package term

import (
	"bytes"
	"encoding/base64"
	"slices"
	"strings"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
)

// Selecting text in a pane goes the two ways scrolling one does.
//
// A session in tmux is selected by tmux. The left button is passed through to
// it, and tmux draws the selection, joins a line it wrapped back into one,
// takes a word on a double click and a line on a triple, and scrolls when the
// drag reaches the edge. The emulator could manage only some of that: it keeps
// no record of which rows were one line, so a long command or a URL copied
// from it would arrive broken wherever the pane happened to wrap it. What tmux
// copies comes back as OSC 52, written to its client — this pane's emulator.
//
// A pane with nothing behind it to hand the button to — no tmux installed, or
// a session that has ended — selects what it is showing itself, row by row as
// it is drawn.

// copyWindow is how long after the button comes up a copy is taken as the
// answer to it. tmux copies a double-clicked word a third of a second after
// the click, so the word can be seen being chosen. Anything later was not
// asked for here: tmux tells every window showing a session what was copied
// from it, so the same session open in another window would otherwise put a
// selection made there onto this one's clipboard as well.
const copyWindow = 2 * time.Second

// selected is how a selection made here is drawn: reverse video, which is how
// tmux is told to draw its own, and how the host list draws its selection.
var selected = uv.Style{Attrs: uv.AttrReverse}

// press is the left button, from the moment it goes down in a pane until it
// comes up.
type press struct {
	// tmux is whether the button went to tmux rather than to a selection made
	// here. Fixed when it goes down.
	tmux bool
	// moved is whether the pointer has moved with the button down. A press
	// that never moved is a click, and selects nothing.
	moved bool
	// ax, ay is the cell the button went down on, and bx, by the one the
	// pointer is over now.
	ax, ay, bx, by int
	// view is what the pane was showing when the button went down, for a
	// selection made here. It is held still for as long as the drag goes on,
	// as tmux holds its own: output arriving meanwhile would otherwise slide
	// under the highlight, and what was copied would not be what was chosen.
	view []uv.Line
}

// Press puts the left button down on a cell of the pane.
func (p *Pane) Press(x, y int) {
	pr := &press{tmux: p.session != "" && p.Alive(), ax: x, ay: y, bx: x, by: y}
	if pr.tmux {
		p.em.SendMouse(uv.MouseClickEvent{X: x, Y: y, Button: uv.MouseLeft})
	} else {
		pr.view = p.viewCells()
	}
	p.mu.Lock()
	p.press = pr
	p.mu.Unlock()
}

// Drag moves the pointer, with the button still down, to a cell of the pane.
func (p *Pane) Drag(x, y int) {
	p.mu.Lock()
	pr := p.press
	if pr != nil {
		pr.moved = true
		pr.bx, pr.by = x, y
	}
	p.mu.Unlock()
	if pr != nil && pr.tmux && p.Alive() {
		p.em.SendMouse(uv.MouseMotionEvent{X: x, Y: y, Button: uv.MouseLeft})
	}
}

// Release lets the button go on a cell of the pane, and returns what a
// selection made here covered. tmux answers for itself a moment later, with
// what it copied, through TakeCopy.
func (p *Pane) Release(x, y int) string {
	p.mu.Lock()
	pr := p.press
	p.press = nil
	p.released = time.Now()
	p.mu.Unlock()

	switch {
	case pr == nil:
		return ""
	case pr.tmux:
		if !p.Alive() {
			return ""
		}
		if pr.moved {
			// tmux takes the first motion of a drag as where the selection
			// starts, and moves it only on the motions after that — so a drag
			// that arrived as a single motion selected nothing at all. Where
			// the pointer was when the button came up is one more of them.
			p.em.SendMouse(uv.MouseMotionEvent{X: x, Y: y, Button: uv.MouseLeft})
		}
		p.em.SendMouse(uv.MouseReleaseEvent{X: x, Y: y, Button: uv.MouseLeft})
		return ""
	case !pr.moved:
		return ""
	}
	return selectedText(pr.view, pr.ax, pr.ay, x, y)
}

// TakeCopy hands over what tmux last copied from the pane, once.
func (p *Pane) TakeCopy() (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	s, ok := p.copied, p.hasCopied
	p.copied, p.hasCopied = "", false
	return s, ok
}

// tmuxCopied takes what tmux has copied from the session, which it writes to
// its client as OSC 52: "52;<selection>;<base64 text>".
//
// A question mark in place of the text asks to read the clipboard instead, and
// is never answered — nothing here reads the clipboard. It runs on the output
// goroutine, inside the emulator's own lock, so it records and returns.
func (p *Pane) tmuxCopied(data []byte) bool {
	_, rest, _ := bytes.Cut(data, []byte{';'})
	_, payload, ok := bytes.Cut(rest, []byte{';'})
	if !ok || bytes.Equal(payload, []byte("?")) {
		return true
	}
	text, err := base64.StdEncoding.DecodeString(string(payload))
	if err != nil {
		return true
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if time.Since(p.released) > copyWindow {
		return true
	}
	// tmux copies on its way out of copy mode, so a view the wheel had
	// scrolled back is live again — and was still described as scrolled.
	p.tmuxOffset = 0
	// A drag that came back to where it began copies nothing, and tmux says
	// so with an empty copy. Passed on, it would empty the clipboard.
	if len(text) > 0 {
		p.copied, p.hasCopied = string(text), true
	}
	return true
}

// forgetSelection drops a selection made here, because the view it was made
// on is no longer what the pane shows. One handed to tmux is tmux's to keep.
func (p *Pane) forgetSelection() {
	p.mu.Lock()
	if p.press != nil && !p.press.tmux {
		p.press = nil
	}
	p.mu.Unlock()
}

// selection is the selection made here being dragged out, if there is one to
// draw.
func (p *Pane) selection() (press, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.press == nil || p.press.tmux || !p.press.moved {
		return press{}, false
	}
	return *p.press, true
}

// viewCells is what the pane is showing, cell by cell: the window Render
// composes from the scrollback when scrolled back, and the screen otherwise.
func (p *Pane) viewCells() []uv.Line {
	p.emMu.RLock()
	defer p.emMu.RUnlock()

	p.mu.Lock()
	off, w, h := p.scroll, p.w, p.h
	p.mu.Unlock()

	blank := func() uv.Line {
		line := make(uv.Line, w)
		for x := range line {
			line[x] = uv.EmptyCell
		}
		return line
	}
	live := func(y int) uv.Line {
		line := blank()
		for x := range line {
			if c := p.em.CellAt(x, y); c != nil {
				line[x] = *c
			}
		}
		return line
	}

	sb := p.em.Scrollback()
	n := sb.Len()
	off = min(max(off, 0), n)
	rows := make([]uv.Line, h)
	for i := range rows {
		if idx := n - off + i; idx < n {
			rows[i] = blank()
			copy(rows[i], sb.Line(idx))
		} else {
			rows[i] = live(idx - n)
		}
	}
	return rows
}

// ordered puts two cells in reading order.
func ordered(ax, ay, bx, by int) (sx, sy, ex, ey int) {
	if by < ay || by == ay && bx < ax {
		return bx, by, ax, ay
	}
	return ax, ay, bx, by
}

// span is the columns of row y a selection covers: from the first cell, up to
// but not including the last one — which is how tmux counts its own, so a drag
// covers the same text whichever of the two has made it.
func span(y, sx, sy, ex, ey, width int) (from, to int) {
	from, to = 0, width
	if y == sy {
		from = sx
	}
	if y == ey {
		to = ex
	}
	return min(max(from, 0), width), min(to, width)
}

// drawSelection renders a view with the cells between two of them highlighted.
func drawSelection(view []uv.Line, ax, ay, bx, by int) string {
	sx, sy, ex, ey := ordered(ax, ay, bx, by)
	rows := make([]string, len(view))
	for y, line := range view {
		if y < sy || y > ey {
			rows[y] = line.Render()
			continue
		}
		line = slices.Clone(line)
		from, to := span(y, sx, sy, ex, ey, len(line))
		for x := from; x < to; x++ {
			// The right half of a wide character draws nothing of its own;
			// styled, it would stop being skipped and be drawn as a cell.
			if line[x].Width > 0 {
				line[x].Style = selected
			}
		}
		rows[y] = line.Render()
	}
	return strings.Join(rows, "\n")
}

// selectedText is what lies between two cells of a view, a row to a line.
//
// Each line loses the spaces at its end, which are the width of the pane
// rather than anything that was written there. A wide character is taken
// whole if its left half is inside, and not at all otherwise.
func selectedText(view []uv.Line, ax, ay, bx, by int) string {
	sx, sy, ex, ey := ordered(ax, ay, bx, by)
	lines := make([]string, 0, ey-sy+1)
	for y := max(sy, 0); y <= ey && y < len(view); y++ {
		line := view[y]
		from, to := span(y, sx, sy, ex, ey, len(line))
		var b strings.Builder
		for _, c := range line[from:max(from, to)] {
			switch {
			case c.Width == 0:
				// the right half of a wide character
			case c.Content == "":
				b.WriteByte(' ')
			default:
				b.WriteString(c.Content)
			}
		}
		lines = append(lines, strings.TrimRight(b.String(), " "))
	}
	return strings.Join(lines, "\n")
}
