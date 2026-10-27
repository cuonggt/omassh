// Package theme holds Omassh's colour palette and the styles derived from it.
//
// The roles are Omarchy's. An Omarchy theme is a colors.toml naming accent,
// selection, muted, foreground and the rest, and a palette here is spelled with
// those same names, so one written by hand and one copied out of a theme say
// the same thing. The names used to be ours — text, text_dim, border,
// selected_bg — and a config file written against them still loads; see
// paletteAliases.
//
// Omarchy's surface roles are deliberately absent. background and its lighter
// and darker kin have nowhere to go here: Omassh paints no background, because
// the terminal's own is what lets the default palette read on a light terminal
// as well as a dark one, and a role nothing ever draws with is a role that
// drifts out of step with the ones that do.
package theme

import (
	"errors"
	"fmt"
	"image/color"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"gopkg.in/yaml.v3"
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

// paletteAliases are the names these colours had before they took Omarchy's,
// and the ones they took. A config file is read with both, since the names
// changed and the colours they stand for did not — and unlike a setting that
// quietly does nothing, an unknown key here is refused outright
// (config.Load decodes with KnownFields), so a rename with no alias would stop
// an existing file from starting Omassh at all.
var paletteAliases = map[string]string{
	"text":        "foreground",
	"text_dim":    "dark_foreground",
	"text_bright": "bright_foreground",
	"border":      "muted",
	"selected_bg": "selection",
}

// IsPaletteKey reports whether name is a colour a palette may set, under
// either its own name or the one it used to have. The config file's own
// complaint about a colour with no value comes through here, so a key it
// accepts is a key that check still looks at.
func IsPaletteKey(name string) bool {
	if _, ok := paletteAliases[name]; ok {
		return true
	}
	for _, k := range PaletteKeys() {
		if k == name {
			return true
		}
	}
	return false
}

// Palette is a named set of colours, as strings so it can come from YAML. Each
// is a hex colour, a number from the terminal's own palette, or default; parse
// says what each of those draws.
type Palette struct {
	Accent     string `yaml:"accent"`
	Selection  string `yaml:"selection"`
	Muted      string `yaml:"muted"`
	Foreground string `yaml:"foreground"`
	DarkFg     string `yaml:"dark_foreground"`
	BrightFg   string `yaml:"bright_foreground"`
	Red        string `yaml:"red"`
	Yellow     string `yaml:"yellow"`
	Green      string `yaml:"green"`
	Magenta    string `yaml:"magenta"`
}

// UnmarshalYAML reads a palette, taking each colour under its own name or the
// one it used to have.
//
// Hand-rolled rather than left to the decoder because the decoder cannot be
// told about an alias: it is run with KnownFields, so every name has to be a
// field, and a field per old name would put all ten of them in PaletteKeys and
// from there into the example config and into the complaint about a name that
// is not a colour. The node is walked instead, which also means the complaint
// can name the line the key is on.
func (p *Palette) UnmarshalYAML(n *yaml.Node) error {
	// Anything that is not a block of keys has no keys to alias, so it is left
	// to yaml's own words — which the config package already has a phrasing
	// for, and which name the line. raw has no UnmarshalYAML of its own, so
	// this does not come back here.
	if n.Kind != yaml.MappingNode {
		type raw Palette
		err := n.Decode((*raw)(p))
		// raw is here only to keep this method from calling itself, and yaml
		// names the type it was handed in the error. theme.raw is not a thing
		// this program has, and the config package phrases its complaints by
		// the type's name, so the real one goes back in.
		var te *yaml.TypeError
		if errors.As(err, &te) {
			for i, e := range te.Errors {
				te.Errors[i] = strings.ReplaceAll(e, "theme.raw", "theme.Palette")
			}
		}
		return err
	}

	v := reflect.ValueOf(p).Elem()
	fields := map[string]reflect.Value{}
	for i := range v.NumField() {
		if tag, _, _ := strings.Cut(v.Type().Field(i).Tag.Get("yaml"), ","); tag != "" {
			fields[tag] = v.Field(i)
		}
	}

	// Collected rather than returned at the first one, and returned as the
	// error type yaml folds into its own, so a file with three mistakes in it
	// still reports all three. Returning early meant a palette's first bad key
	// hid every later mistake in the file, including the ones outside the
	// palette, and a config file would have had to be fixed one failed startup
	// at a time.
	var errs []string
	for i := 0; i+1 < len(n.Content); i += 2 {
		key, value := n.Content[i], n.Content[i+1]
		name := key.Value
		if to, ok := paletteAliases[name]; ok {
			name = to
		}
		f, ok := fields[name]
		if !ok {
			errs = append(errs, fmt.Sprintf("line %d: %q is not a palette colour — they are %s",
				key.Line, key.Value, strings.Join(PaletteKeys(), ", ")))
			continue
		}
		// A key with no value leaves the colour empty, which is a palette
		// taking the default for it. config's blankColours reports that
		// separately, with the advice about a # that yaml read as a comment.
		if value.ShortTag() == "!!null" {
			continue
		}
		var s string
		if err := value.Decode(&s); err != nil {
			var te *yaml.TypeError
			if errors.As(err, &te) {
				errs = append(errs, te.Errors...)
			} else {
				errs = append(errs, err.Error())
			}
			continue
		}
		f.SetString(s)
	}
	if len(errs) > 0 {
		return &yaml.TypeError{Errors: errs}
	}
	return nil
}

// DefaultName is the palette used when nothing else is chosen.
const DefaultName = "terminal"

// Builtin palettes, selectable by name from the config file.
//
// All but two of them are Omarchy's own themes, with each role taken from the
// theme's colors.toml, so the list T offers is the list omarchy-theme-set
// offers and a machine running one of them can be asked for the same name
// here. Omarchy's light themes are left out: with no background of our own to
// paint, a light foreground lands on whatever the terminal already had behind
// it, and on a dark one that is a palette drawn in its own invisible ink.
//
// Some of them barely have a red or a green — vantablack and solitude give
// both as greys. Nothing is lost by that, because no colour here is ever the
// only thing saying what it means: a failure is red and ✖, a tunnel up is
// green and ▶. A theme is allowed to be monochrome.
var Builtin = map[string]Palette{
	// The terminal's own colours, and the default. The default used to be a
	// scheme of ours drawn in hex over whatever the terminal had behind it —
	// made, like every palette here, for a dark background, so on a light one
	// (Terminal.app's default among them) the text was pale lavender on white.
	// A number from 0 to 15 is a colour the terminal's scheme defines, so it
	// is drawn in whatever scheme the user chose, suits its background, and
	// changes when it does. Under Omarchy that scheme is the Omarchy theme,
	// which makes this palette the one that follows it without being told.
	//
	// Bright black is where schemes keep the grey that everything else dims
	// with. Nothing is bright white, which on a light terminal is the colour
	// of the page: emphasis is the terminal's own text colour, made bold by
	// the styles that want it. The selection has no colour of its own at all,
	// since none of ours is sure to read against a background this cannot
	// see — the cursor is an accent bar and accent text instead, which is
	// Omarchy's way of it anyway.
	"terminal": {
		Accent: "4", Selection: "default", Muted: "8",
		Foreground: "default", DarkFg: "8", BrightFg: "default",
		Red: "1", Yellow: "3", Green: "2", Magenta: "5",
	},
	// For terminals with a limited or unusual palette, and for screenshots.
	"mono": {
		Accent: "#ffffff", Selection: "#333333", Muted: "#555555",
		Foreground: "#cccccc", DarkFg: "#777777", BrightFg: "#ffffff",
		Red: "#ffffff", Yellow: "#cccccc", Green: "#cccccc", Magenta: "#cccccc",
	},
	"catppuccin": {
		Accent: "#89b4fa", Selection: "#45475a", Muted: "#585b70",
		Foreground: "#cdd6f4", DarkFg: "#6c7086", BrightFg: "#cdd6f4",
		Red: "#f38ba8", Yellow: "#f9e2af", Green: "#a6e3a1", Magenta: "#f5c2e7",
	},
	"ethereal": {
		Accent: "#7d82d9", Selection: "#252e56", Muted: "#6d7db6",
		Foreground: "#ffcead", DarkFg: "#6d7db6", BrightFg: "#ffcead",
		Red: "#ed5b5a", Yellow: "#e9bb4f", Green: "#92a593", Magenta: "#c89dc1",
	},
	"everforest": {
		Accent: "#7fbbb3", Selection: "#3d484d", Muted: "#475258",
		Foreground: "#d3c6aa", DarkFg: "#4f585e", BrightFg: "#d3c6aa",
		Red: "#e67e80", Yellow: "#dbbc7f", Green: "#a7c080", Magenta: "#d699b6",
	},
	"gruvbox": {
		Accent: "#7daea3", Selection: "#504945", Muted: "#665c54",
		Foreground: "#d4be98", DarkFg: "#7c6f64", BrightFg: "#d4be98",
		Red: "#ea6962", Yellow: "#d8a657", Green: "#a9b665", Magenta: "#d3869b",
	},
	"hackerman": {
		Accent: "#82fb9c", Selection: "#1f253a", Muted: "#2d3450",
		Foreground: "#ddf7ff", DarkFg: "#6a6e95", BrightFg: "#ddf7ff",
		Red: "#50f872", Yellow: "#50f7d4", Green: "#4fe88f", Magenta: "#86a7df",
	},
	"jade-horse": {
		Accent: "#8bcbb1", Selection: "#2b5147", Muted: "#64887b",
		Foreground: "#dfede5", DarkFg: "#91ad9f", BrightFg: "#f0faf4",
		Red: "#d88d91", Yellow: "#c5cc91", Green: "#8bcbb1", Magenta: "#b7a1c8",
	},
	"kanagawa": {
		Accent: "#dcd7ba", Selection: "#363646", Muted: "#54546d",
		Foreground: "#dcd7ba", DarkFg: "#727169", BrightFg: "#dcd7ba",
		Red: "#c34043", Yellow: "#c0a36e", Green: "#76946a", Magenta: "#957fb8",
	},
	"last-horizon": {
		Accent: "#b59790", Selection: "#584e51", Muted: "#584e51",
		Foreground: "#fafcfb", DarkFg: "#584e51", BrightFg: "#e2dddc",
		Red: "#c38b7b", Yellow: "#6b5e73", Green: "#87a9b0", Magenta: "#c4d8e2",
	},
	"lumon": {
		Accent: "#8bc9eb", Selection: "#243d56", Muted: "#304860",
		Foreground: "#d6e2ee", DarkFg: "#4d86b0", BrightFg: "#f2fcff",
		Red: "#4d86b0", Yellow: "#6fa4c9", Green: "#5e95bc", Magenta: "#8bc9eb",
	},
	"matte-black": {
		Accent: "#e68e0d", Selection: "#2a2a2a", Muted: "#333333",
		Foreground: "#bebebe", DarkFg: "#555555", BrightFg: "#bebebe",
		Red: "#d35f5f", Yellow: "#b91c1c", Green: "#ffc107", Magenta: "#d35f5f",
	},
	"miasma": {
		Accent: "#78824b", Selection: "#383838", Muted: "#666666",
		Foreground: "#c2c2b0", DarkFg: "#555555", BrightFg: "#c2c2b0",
		Red: "#685742", Yellow: "#b36d43", Green: "#5f875f", Magenta: "#bb7744",
	},
	"nord": {
		Accent: "#81a1c1", Selection: "#434c5e", Muted: "#4c566a",
		Foreground: "#d8dee9", DarkFg: "#667080", BrightFg: "#d8dee9",
		Red: "#bf616a", Yellow: "#ebcb8b", Green: "#a3be8c", Magenta: "#b48ead",
	},
	"retro-82": {
		Accent: "#faa968", Selection: "#134e5a", Muted: "#2a6b78",
		Foreground: "#f6dcac", DarkFg: "#3f8f8a", BrightFg: "#f6dcac",
		Red: "#f85525", Yellow: "#e97b3c", Green: "#028391", Magenta: "#3f8f8a",
	},
	"ristretto": {
		Accent: "#f38d70", Selection: "#403e41", Muted: "#72696a",
		Foreground: "#e6d9db", DarkFg: "#72696a", BrightFg: "#e6d9db",
		Red: "#fd6883", Yellow: "#f9cc6c", Green: "#adda78", Magenta: "#a8a9eb",
	},
	"solitude": {
		Accent: "#798186", Selection: "#343d41", Muted: "#4b4e55",
		Foreground: "#cacccc", DarkFg: "#4b4e55", BrightFg: "#a5aeb4",
		Red: "#565d60", Yellow: "#d9dbdc", Green: "#9fa5a9", Magenta: "#aeaeae",
	},
	"starship": {
		Accent: "#ff9f0a", Selection: "#3a3a3c", Muted: "#8e8e93",
		Foreground: "#f5f5f7", DarkFg: "#98989d", BrightFg: "#ffffff",
		Red: "#ff5f4a", Yellow: "#ffc27a", Green: "#ff9f0a", Magenta: "#ff8fa3",
	},
	"tokyo-night": {
		Accent: "#7aa2f7", Selection: "#292e42", Muted: "#414868",
		Foreground: "#a9b1d6", DarkFg: "#565f89", BrightFg: "#c0caf5",
		Red: "#f7768e", Yellow: "#e0af68", Green: "#9ece6a", Magenta: "#ad8ee6",
	},
	"vantablack": {
		Accent: "#8d8d8d", Selection: "#1a1a1a", Muted: "#7a7a7a",
		Foreground: "#ffffff", DarkFg: "#505050", BrightFg: "#ffffff",
		Red: "#a4a4a4", Yellow: "#cecece", Green: "#b6b6b6", Magenta: "#9b9b9b",
	},
}

// themeAliases are palettes that have been renamed, so a config file naming
// the old one still resolves to the colours it asked for. tokyonight was ours;
// Omarchy spells the same scheme tokyo-night, and that is the name the picker
// now lists.
var themeAliases = map[string]string{"tokyonight": "tokyo-night"}

// Lookup is the built-in palette a name asks for, following a rename. Every
// place that resolves a theme name comes through here, so what starts Omassh
// and what the picker shows cannot disagree about which palette a name means.
func Lookup(name string) (Palette, bool) {
	if to, ok := themeAliases[name]; ok {
		name = to
	}
	p, ok := Builtin[name]
	return p, ok
}

// BuiltinNames lists the palettes, for error messages and documentation. The
// names a palette used to go by are not among them: they resolve, and offering
// one in the picker would list the same colours twice.
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
	Accent     color.Color
	Selection  color.Color
	Muted      color.Color
	Foreground color.Color
	DarkFg     color.Color
	BrightFg   color.Color
	Red        color.Color
	Yellow     color.Color
	Green      color.Color
	Magenta    color.Color
)

// Styles derived from the colours above. They are recomputed by Apply, since a
// style captures its colour at construction and would otherwise keep the old
// palette after a theme change.
var (
	// Dim is secondary information: a label, a provenance, a hint's words.
	Dim lipgloss.Style
	// Normal is body text.
	Normal lipgloss.Style
	// Strong is a heading or the one word in a sentence that carries it.
	// Omarchy spends bright_foreground sparingly, so this is not the default
	// for anything.
	Strong lipgloss.Style
	// Key is a keycap: the key itself in a hint, bold rather than coloured,
	// because accent belongs to focus and a screen full of accented keys
	// leaves nothing for it to mean. Separate from Strong, which it happens
	// to equal, so that changing one does not silently change the other.
	Key lipgloss.Style
	// Cursor is the text of the row the keyboard is on. Omarchy pairs its
	// selection fill with bright_foreground; a palette with no fill of its own
	// gets accent text, which the bar beside it agrees with.
	Cursor lipgloss.Style
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

	Accent = pick(p.Accent, base.Accent)
	Selection = pick(p.Selection, base.Selection)
	Muted = pick(p.Muted, base.Muted)
	Foreground = pick(p.Foreground, base.Foreground)
	DarkFg = pick(p.DarkFg, base.DarkFg)
	BrightFg = pick(p.BrightFg, base.BrightFg)
	Red = pick(p.Red, base.Red)
	Yellow = pick(p.Yellow, base.Yellow)
	Green = pick(p.Green, base.Green)
	Magenta = pick(p.Magenta, base.Magenta)

	Dim = Fg(DarkFg)
	Normal = Fg(Foreground)
	Strong = Fg(BrightFg).Bold(true)
	Key = Fg(BrightFg).Bold(true)

	Cursor = Fg(BrightFg).Background(Selection).Bold(true)
	if Selection == (lipgloss.NoColor{}) {
		// The terminal's own background is no fill at all, so a palette that
		// names the selection none of its own gets none: the row is marked by
		// the accent bar at its edge and accent text beside it. Which is where
		// Omarchy would have put the emphasis regardless — an edge rather than
		// a solid fill — and it is the one treatment that cannot turn out to
		// be the colour of the page.
		Cursor = Fg(Accent).Bold(true)
	}
}

// Validate reports colours that parse cannot read, so a typo in a config file
// is named at startup instead of silently being drawn as some other colour.
//
// Read off the struct, like PaletteKeys: this was a hand-written list of the
// ten names, which is to say a second place to remember a role exists, and the
// one place in the package that a new role would have been left out of.
func (p Palette) Validate() error {
	v := reflect.ValueOf(p)
	for i := range v.NumField() {
		name, _, _ := strings.Cut(v.Type().Field(i).Tag.Get("yaml"), ",")
		value := v.Field(i).String()
		if name == "" || strings.TrimSpace(value) == "" {
			continue
		}
		if _, err := parse(value); err != nil {
			return fmt.Errorf("%s: %w", name, err)
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
//   - default is the terminal's own text colour, and as selection no fill at
//     all — the row marked by its accent edge instead.
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
