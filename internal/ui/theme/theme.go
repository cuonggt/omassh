// Package theme holds omassh's colour palette and shared styles.
package theme

import (
	"image/color"

	"charm.land/lipgloss/v2"
)

var (
	Text    = lipgloss.Color("#c0caf5")
	TextDim = lipgloss.Color("#565f89")
	TextBrt = lipgloss.Color("#ffffff")
	Accent  = lipgloss.Color("#7aa2f7")
	Green   = lipgloss.Color("#9ece6a")
	Yellow  = lipgloss.Color("#e0af68")
	Red     = lipgloss.Color("#f7768e")
	Magenta = lipgloss.Color("#bb9af7")
	Border  = lipgloss.Color("#3b4261")
	SelBg   = lipgloss.Color("#283457")
)

func Fg(c color.Color) lipgloss.Style { return lipgloss.NewStyle().Foreground(c) }

var (
	Dim      = Fg(TextDim)
	Normal   = Fg(Text)
	Selected = lipgloss.NewStyle().Foreground(TextBrt).Background(SelBg).Bold(true)
	Title    = Fg(Accent).Bold(true)
	Key      = Fg(Accent).Bold(true)
)
