// Package theme holds Omassh's colour palette and the styles derived from it.
package theme

import (
	"fmt"
	"image/color"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
)

// PaletteKeys are the colours a palette may set, in the order the example
// config lists them. Read off the struct so the two cannot drift: a name
// added here appears in the complaint about a name that is not here.
func PaletteKeys() []string {
	t := reflect.TypeOf(Palette{})
	out := make([]string, 0, t.NumField())
	for i := range t.NumField() {
		if tag, _, _ := strings.Cut(t.Field(i).Tag.Get("yaml"), ","); tag != "" {
			out = append(out, tag)
		}
	}
	return out
}

// Palette is a named set of colours, as strings so it can come from YAML. Each
// is a hex colour, a number from the terminal's own palette, or default; parse
// says what each of those draws.
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

// DefaultName is the palette used when nothing else is chosen.
const DefaultName = "terminal"

// Builtin palettes, selectable by name from the config file.
var Builtin = map[string]Palette{
	// The terminal's own colours. The default used to be tokyonight, a scheme
	// of ours drawn in hex over whatever the terminal had behind it — made,
	// like every palette here, for a dark background, so on a light one
	// (Terminal.app's default among them) the text was pale lavender on white.
	// A number from 0 to 15 is a colour the terminal's scheme defines, so it
	// is drawn in whatever scheme the user chose, suits its background, and
	// changes when it does.
	//
	// Bright black is where schemes keep the grey that everything else dims
	// with. Nothing is bright white, which on a light terminal is the colour
	// of the page: emphasis is the terminal's own text colour, made bold by
	// the styles that want it. And the selection is the terminal's own
	// highlight, its colours swapped, since no colour of ours is sure to read
	// against a background this cannot see.
	"terminal": {
		Text: "default", TextDim: "8", TextBrt: "default", Accent: "4",
		Green: "2", Yellow: "3", Red: "1", Magenta: "5",
		Border: "8", Selected: "default",
	},
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

func init() { Apply(Builtin[DefaultName]) }

func Fg(c color.Color) lipgloss.Style { return lipgloss.NewStyle().Foreground(c) }

// Apply makes p the active palette. Fields left empty fall back to the
// built-in default, so a config file can override one colour without having to
// restate the rest. So does a value parse cannot read, which Validate is there
// to report before anything is drawn.
func Apply(p Palette) {
	base := Builtin[DefaultName]
	pick := func(v, fallback string) color.Color {
		if c, err := parse(v); err == nil {
			return c
		}
		c, _ := parse(fallback)
		return c
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
	if SelBg == (lipgloss.NoColor{}) {
		// The terminal's own background is no highlight at all, so default
		// here means the terminal's own highlight: its two colours swapped.
		// Those are the one pair every scheme makes sure can be read against
		// each other, and text_bright is left out of it because a colour of
		// ours as the bar could be the colour of the page.
		Selected = lipgloss.NewStyle().Reverse(true).Bold(true)
	}
	Title = Fg(Accent).Bold(true)
	Key = Fg(Accent).Bold(true)
}

// Validate reports colours that parse cannot read, so a typo in a config file
// is named at startup instead of silently being drawn as some other colour.
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
		if strings.TrimSpace(fields[n]) == "" {
			continue
		}
		if _, err := parse(fields[n]); err != nil {
			return fmt.Errorf("%s: %w", n, err)
		}
	}
	return nil
}

// parse reads one colour of a palette. Apply and Validate both come through
// here, so a value the config file is allowed to hold is always one that
// draws — which lipgloss does not promise on its own: anything it cannot read
// it draws as nothing, and says nothing.
//
//   - "#rrggbb" or "#rgb" is that colour exactly, whatever the terminal is.
//   - A number is that entry in the terminal's own palette. The first sixteen
//     are the ones a colour scheme sets, so they follow it.
//   - default is the terminal's own text colour, and as selected_bg its own
//     highlight — the row drawn in reverse.
func parse(s string) (color.Color, error) {
	s = strings.TrimSpace(s)
	switch {
	case strings.EqualFold(s, "default"):
		return lipgloss.NoColor{}, nil
	case validHex(s), validIndex(s):
		return lipgloss.Color(s), nil
	}
	return nil, fmt.Errorf("%q is not a colour — write one as #7aa2f7, as a number from 0 to 255 for one of the terminal's own, or as default", s)
}

// validIndex is a number the terminal's palette has an entry for. lipgloss
// takes any integer, reading a negative one as its opposite and anything past
// 255 as a 24-bit colour, so 1000 typed for 100 would have drawn a colour
// nobody asked for rather than being refused.
func validIndex(s string) bool {
	if s == "" || strings.TrimLeft(s, "0123456789") != "" {
		return false
	}
	n, err := strconv.Atoi(s)
	return err == nil && n <= 255
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
