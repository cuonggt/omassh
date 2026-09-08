package ui

import (
	"image/color"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/cuonggt/omassh/internal/ui/theme"
)

// box draws a titled, rounded panel of exactly w x h cells with body inside.
//
// The border is drawn by hand rather than with a Lipgloss border style so the
// title can sit in the top rule, lazygit-style. All width maths goes through
// ansi.StringWidth so styled and wide-character content measure correctly.
func box(title string, focused bool, w, h int, body string) string {
	if w < 4 || h < 2 {
		return ""
	}

	bc, tc := theme.Border, color.Color(theme.TextDim)
	if focused {
		bc, tc = theme.Accent, color.Color(theme.TextBrt)
	}
	bs := theme.Fg(bc)
	ts := theme.Fg(tc).Bold(focused)

	label := ansi.Truncate(title, max(w-5, 0), "…")
	rule := max(w-5-ansi.StringWidth(label), 0)

	var sb strings.Builder
	sb.WriteString(bs.Render("╭─ ") + ts.Render(label) + bs.Render(" "+strings.Repeat("─", rule)+"╮"))

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

	sb.WriteString("\n" + bs.Render("╰"+strings.Repeat("─", w-2)+"╯"))
	return sb.String()
}

// row renders one selectable list line, filling the full content width so the
// selection highlight reads as a solid bar.
func row(s string, selected bool, w int) string {
	s = ansi.Truncate(s, w, "…")
	s += strings.Repeat(" ", max(w-ansi.StringWidth(s), 0))
	if selected {
		return theme.Selected.Render(s)
	}
	return theme.Normal.Render(s)
}

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
