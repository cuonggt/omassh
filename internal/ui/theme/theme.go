// Package theme holds Omassh's colour palette and the styles derived from it.
package theme

import (
	"fmt"
	"image/color"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"
)

// Palette is a named set of colours, as hex strings so it can come from YAML.
type Palette struct {
	Text     string `yaml:"text"`
	TextDim  string `yaml:"text_dim"`
	TextBrt  string `yaml:"text_bright"`
	Accent   string `yaml:"accent"`
	Green    string `yaml:"green"`
	Yellow   string `yaml:"yellow"`
	Red      string `yaml:"red"`
	Magenta  string `yaml:"magenta"`
	Border   string `yaml:"border"`
	Selected string `yaml:"selected_bg"`
}

// Builtin palettes, selectable by name from the config file.
var Builtin = map[string]Palette{
	"tokyonight": {
		Text: "#c0caf5", TextDim: "#565f89", TextBrt: "#ffffff", Accent: "#7aa2f7",
		Green: "#9ece6a", Yellow: "#e0af68", Red: "#f7768e", Magenta: "#bb9af7",
		Border: "#3b4261", Selected: "#283457",
	},
	"gruvbox": {
		Text: "#ebdbb2", TextDim: "#928374", TextBrt: "#fbf1c7", Accent: "#83a598",
		Green: "#b8bb26", Yellow: "#fabd2f", Red: "#fb4934", Magenta: "#d3869b",
		Border: "#504945", Selected: "#3c3836",
	},
	"nord": {
		Text: "#d8dee9", TextDim: "#6c7a94", TextBrt: "#eceff4", Accent: "#88c0d0",
		Green: "#a3be8c", Yellow: "#ebcb8b", Red: "#bf616a", Magenta: "#b48ead",
		Border: "#434c5e", Selected: "#3b4252",
	},
	// For terminals with a limited or unusual palette, and for screenshots.
	"mono": {
		Text: "#cccccc", TextDim: "#777777", TextBrt: "#ffffff", Accent: "#ffffff",
		Green: "#cccccc", Yellow: "#cccccc", Red: "#ffffff", Magenta: "#cccccc",
		Border: "#555555", Selected: "#333333",
	},
}

// BuiltinNames lists the palettes, for error messages and documentation.
func BuiltinNames() []string {
	names := make([]string, 0, len(Builtin))
	for n := range Builtin {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// The active colours. Rendering code reads these directly.
var (
	Text    color.Color
	TextDim color.Color
	TextBrt color.Color
	Accent  color.Color
	Green   color.Color
	Yellow  color.Color
	Red     color.Color
	Magenta color.Color
	Border  color.Color
	SelBg   color.Color
)

// Styles derived from the colours above. They are recomputed by Apply, since a
// style captures its colour at construction and would otherwise keep the old
// palette after a theme change.
var (
	Dim      lipgloss.Style
	Normal   lipgloss.Style
	Selected lipgloss.Style
	Title    lipgloss.Style
	Key      lipgloss.Style
)

func init() { Apply(Builtin["tokyonight"]) }

func Fg(c color.Color) lipgloss.Style { return lipgloss.NewStyle().Foreground(c) }

// Apply makes p the active palette. Fields left empty fall back to the
// built-in default, so a config file can override one colour without having to
// restate the rest.
func Apply(p Palette) {
	base := Builtin["tokyonight"]
	pick := func(v, fallback string) color.Color {
		if strings.TrimSpace(v) == "" {
			return lipgloss.Color(fallback)
		}
		return lipgloss.Color(v)
	}

	Text = pick(p.Text, base.Text)
	TextDim = pick(p.TextDim, base.TextDim)
	TextBrt = pick(p.TextBrt, base.TextBrt)
	Accent = pick(p.Accent, base.Accent)
	Green = pick(p.Green, base.Green)
	Yellow = pick(p.Yellow, base.Yellow)
	Red = pick(p.Red, base.Red)
	Magenta = pick(p.Magenta, base.Magenta)
	Border = pick(p.Border, base.Border)
	SelBg = pick(p.Selected, base.Selected)

	Dim = Fg(TextDim)
	Normal = Fg(Text)
	Selected = lipgloss.NewStyle().Foreground(TextBrt).Background(SelBg).Bold(true)
	Title = Fg(Accent).Bold(true)
	Key = Fg(Accent).Bold(true)
}

// Validate reports colours that lipgloss cannot parse, so a typo in a config
// file is named at startup instead of silently rendering as black.
func (p Palette) Validate() error {
	fields := map[string]string{
		"text": p.Text, "text_dim": p.TextDim, "text_bright": p.TextBrt,
		"accent": p.Accent, "green": p.Green, "yellow": p.Yellow,
		"red": p.Red, "magenta": p.Magenta, "border": p.Border,
		"selected_bg": p.Selected,
	}
	names := make([]string, 0, len(fields))
	for n := range fields {
		names = append(names, n)
	}
	sort.Strings(names)

	for _, n := range names {
		v := strings.TrimSpace(fields[n])
		if v == "" {
			continue
		}
		if !validHex(v) {
			return fmt.Errorf("%s: %q is not a hex colour like #7aa2f7", n, v)
		}
	}
	return nil
}

func validHex(s string) bool {
	if len(s) != 4 && len(s) != 7 {
		return false
	}
	if s[0] != '#' {
		return false
	}
	for _, c := range s[1:] {
		if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
			return false
		}
	}
	return true
}
