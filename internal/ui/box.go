package ui

import (
	"image/color"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/cuonggt/omassh/internal/ui/theme"
)

// The chrome here follows Omarchy's design language, and docs/configuration.md
// records the mapping. The short of it: square corners, one-cell borders in the
// muted role with a blank cell between panels rather than two borders touching,
// focus carried by an accent border and a bold title, emphasis carried on an
// edge rather than in a solid fill, and no colour spent on decoration — every
// colour and every glyph on the screen stands for a state.

// box draws a titled, square panel of exactly w x h cells with body inside.
//
// Square because Omarchy rounds nothing by default, and a terminal has no half
// cell to round with regardless: the corners were ╭╮╰╯, which is a radius
// drawn in the font rather than in the layout, and read as a different shape
// in every font that drew it.
//
// The border is drawn by hand rather than with a Lipgloss border style so the
// title can sit in the top rule, lazygit-style. All width maths goes through
// ansi.StringWidth so styled and wide-character content measure correctly.
func box(title string, focused bool, w, h int, body string) string {
	if w < 4 || h < 2 {
		return ""
	}

	// The border says which panel the keyboard is going to, and the title's
	// weight says it a second way: a border is a thin thing to hang a state
	// on, and colour is never the only signal for one.
	bc, tc, bold := theme.Muted, color.Color(theme.DarkFg), false
	if focused {
		bc, tc, bold = theme.Accent, color.Color(theme.BrightFg), true
	}
	bs := theme.Fg(bc)
	ts := theme.Fg(tc).Bold(bold)

	label := ansi.Truncate(title, max(w-5, 0), "…")
	rule := max(w-5-ansi.StringWidth(label), 0)

	var sb strings.Builder
	sb.WriteString(bs.Render("┌─ ") + ts.Render(label) + bs.Render(" "+strings.Repeat("─", rule)+"┐"))

	cw := w - 4
	rows := strings.Split(body, "\n")
	for i := range h - 2 {
		var line string
		if i < len(rows) {
			line = ansi.Truncate(rows[i], cw, "…")
		}
		line += strings.Repeat(" ", max(cw-ansi.StringWidth(line), 0))
		sb.WriteString("\n" + bs.Render("│ ") + line + bs.Render(" │"))
	}

	sb.WriteString("\n" + bs.Render("└"+strings.Repeat("─", w-2)+"┘"))
	return sb.String()
}

// rowState is how a list row is marked.
//
// Omarchy keeps three things apart that a single highlight collapses into one:
// the persistent choice, the cursor the keyboard is moving, and which panel
// input is actually going to. The last of those belongs to the panel's border,
// so a row here is one of the three below.
type rowState int

const (
	// rowPlain is a row the cursor is not on.
	rowPlain rowState = iota
	// rowMarked is the cursor in a panel that does not have focus. Such a row
	// used to be drawn as nothing at all, which meant tabbing out of a list
	// lost where you were in it — and, since the wheel moves the cursor of
	// whatever list it is over, that the cursor could travel invisibly.
	rowMarked
	// rowCursor is the cursor in the panel the keyboard is going to.
	rowCursor
)

// cursorState is how to mark a row, given whether the cursor is on it and
// whether its panel has focus.
func cursorState(isCursor, focused bool) rowState {
	switch {
	case isCursor && focused:
		return rowCursor
	case isCursor:
		return rowMarked
	}
	return rowPlain
}

// cursorBar is the edge that marks the cursor's row. An edge rather than a
// fill because that is where Omarchy puts emphasis, and because it is the one
// mark every palette can draw: a theme that names no selection colour has no
// fill to give, and the terminal's own reverse video — which is what used to
// stand in for one — paints over the badges on the row it lands on.
const cursorBar = "▌"

// row renders one list line, filled to the content width so that a palette's
// selection colour reads as a solid bar and so the columns line up.
//
// The first cell belongs to the cursor on every row, whether or not the cursor
// is there. Omarchy reserves the widest state's border before layout so that
// arriving at a component does not move it; here that is literal — a bar
// prefixed to the cursor's row alone would have shifted its text one cell
// right of every other row in the list.
func row(s string, st rowState, w int) string {
	tw := max(w-1, 0)
	s = ansi.Truncate(s, tw, "…")
	s += strings.Repeat(" ", max(tw-ansi.StringWidth(s), 0))

	style := theme.Normal
	if st == rowCursor {
		style = theme.Cursor
	}
	return gutter(st) + style.Render(s)
}

// gutter is the cursor's own cell, for the rows that compose their own text and
// so cannot go through row — a host with a probe mark and a session badge, a
// directory, a forwarding rule. Those keep their own colours, which the cursor
// style would paint over, so they reserve the column by hand instead.
func gutter(st rowState) string {
	switch st {
	case rowCursor:
		return theme.Fg(theme.Accent).Render(cursorBar)
	case rowMarked:
		return theme.Fg(theme.Muted).Render(cursorBar)
	}
	return " "
}

// cursorText styles the part of a composed row that names the thing it is
// about. Those rows carry colours of their own further along — a run's status
// mark, an address in secondary text — so they cannot go through row, which
// would paint over them; the cursor is the bar at the edge plus weight here,
// which is how Omarchy marks a selected control's own text.
func cursorText(s string, st rowState) string {
	if st == rowCursor {
		return theme.Strong.Render(s)
	}
	return theme.Normal.Render(s)
}

// hint is one key and what it does. The key is a keycap — bold rather than
// coloured, since accent belongs to focus and a status bar of accented keys
// leaves it nothing to mean — and what it does is secondary text beside it.
func hint(key, label string) string {
	return theme.Key.Render(key) + theme.Dim.Render(" "+label)
}

// overlay composites fg on top of bg with its top-left corner at (x, y),
// leaving the rest of bg visible around it. This is what makes a form read as
// a dialog over the list rather than a screen that replaced it.
//
// Every row is rebuilt as left-of-popup + popup + right-of-popup, measured in
// cells rather than bytes, so styled background content survives being cut.
func overlay(bg, fg string, x, y int) string {
	bgLines := strings.Split(bg, "\n")
	fgLines := strings.Split(fg, "\n")

	for i, fgLine := range fgLines {
		row := y + i
		if row < 0 || row >= len(bgLines) {
			continue
		}
		line := bgLines[row]
		fgw := ansi.StringWidth(fgLine)

		left := ansi.Truncate(line, x, "")
		// A background row shorter than x needs padding, or the popup would
		// slide left and the frame would lose its rectangular shape.
		left += strings.Repeat(" ", max(x-ansi.StringWidth(left), 0))
		right := ansi.TruncateLeft(line, x+fgw, "")

		bgLines[row] = left + fgLine + right
	}
	return strings.Join(bgLines, "\n")
}

// centre returns the top-left corner that puts a w x h box in the middle of a
// frame, biased upwards so a dialog sits where the eye already is.
func centre(w, h, frameW, frameH int) (x, y int) {
	return max((frameW-w)/2, 0), max((frameH-h)/3, 0)
}
