package theme

import (
	"image/color"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"gopkg.in/yaml.v3"
)

// restore puts the default back afterwards. The palette in effect is global,
// so one test's colours would otherwise be the next one's.
func restore(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { Apply(Builtin[DefaultName]) })
}

// active is every colour the rendering code reads, by the name a palette gives
// it.
func active() map[string]color.Color {
	return map[string]color.Color{
		"accent": Accent, "selection": Selection, "muted": Muted,
		"foreground": Foreground, "dark_foreground": DarkFg,
		"bright_foreground": BrightFg, "green": Green, "yellow": Yellow,
		"red": Red, "magenta": Magenta,
	}
}

var sgrRE = regexp.MustCompile(`\x1b\[([0-9;:]*)m`)

// params is every parameter rendered text sets its style with.
func params(s string) []string {
	var out []string
	for _, m := range sgrRE.FindAllStringSubmatch(s, -1) {
		out = append(out, strings.FieldsFunc(m[1], func(r rune) bool { return r == ';' || r == ':' })...)
	}
	return out
}

// Nothing configured means the terminal's own colours, and only those: its
// text colour, and the sixteen its scheme sets. A single colour of ours in
// among them is drawn the same on every terminal — so it is the one thing on
// the screen that ignores the scheme, and on a background it was not chosen
// for, the one thing that cannot be read.
func TestTheDefaultIsDrawnOnlyInColoursTheTerminalsSchemeSets(t *testing.T) {
	restore(t)
	Apply(Builtin[DefaultName])

	for name, c := range active() {
		switch c.(type) {
		case lipgloss.NoColor, ansi.BasicColor:
		default:
			t.Errorf("%s is %#v, a colour of ours rather than one the terminal's scheme sets", name, c)
		}
	}
}

// Every built-in is one a config file could hold, and holds every colour. The
// default is what an omitted colour falls back to, so an empty one there
// would leave that colour with nothing to fall back to at all.
func TestEveryBuiltInPaletteIsCompleteAndOneAConfigFileCouldHold(t *testing.T) {
	for name, p := range Builtin {
		if err := p.Validate(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
		v := reflect.ValueOf(p)
		for i := range v.NumField() {
			if strings.TrimSpace(v.Field(i).String()) == "" {
				t.Errorf("%s leaves %s empty", name, v.Type().Field(i).Tag.Get("yaml"))
			}
		}
	}
}

// A palette that gives the selection no colour of its own has no fill to draw
// the cursor's row with, so the row is marked where Omarchy would have put the
// emphasis regardless: on its edge, by the bar in box.go, with the text going
// accent and bold to agree with it.
//
// It brings no colour of its own into the row and does not reverse it, which
// is what it used to do. The swap read well enough but painted over the badges
// on whichever row it landed on, and reverse on a light terminal is the one
// treatment whose result nothing here can predict.
func TestACursorRowWithNoSelectionColourIsAccentTextRatherThanAFill(t *testing.T) {
	restore(t)
	for name, p := range map[string]Palette{
		"the default":            Builtin[DefaultName],
		"a bright text of yours": {BrightFg: "#ffffff"},
	} {
		Apply(p)
		got := params(Cursor.Render("web-01"))
		if slices.Contains(got, "48") {
			t.Errorf("%s: the cursor's row is filled, with no colour to fill it: %q", name, got)
		}
		if slices.Contains(got, "7") {
			t.Errorf("%s: the cursor's row is drawn in reverse: %q", name, got)
		}
		if !slices.Contains(got, "1") {
			t.Errorf("%s: the cursor's row is not bold, so the bar is marking it alone: %q", name, got)
		}
	}
}

// A palette that does name a selection colour gets a bar of exactly that, with
// bright_foreground on it — the pairing Omarchy gives the selection role, and
// the one pair a theme has promised can be read against each other.
func TestACursorRowWithASelectionColourIsFilledWithItAndBrightText(t *testing.T) {
	restore(t)
	Apply(Builtin["tokyo-night"])
	got := strings.Join(params(Cursor.Render("web-01")), ";")

	if !strings.Contains(got, "48;2;41;46;66") {
		t.Errorf("tokyo-night's cursor row is %q, want its own #292e42 bar", got)
	}
	if !strings.Contains(got, "38;2;192;202;245") {
		t.Errorf("tokyo-night's cursor row is %q, want its #c0caf5 bright text on it", got)
	}
	if strings.Contains(got, ";7;") {
		t.Errorf("tokyo-night's cursor row is drawn in reverse: %q", got)
	}
}

// The colours are Omarchy's roles, and a config file written before they were
// still loads: the names changed and what they stand for did not. Refusing one
// would have stopped Omassh at startup rather than drawing something slightly
// wrong, since the decoder is run with KnownFields.
func TestAPaletteIsReadUnderTheColourNamesItUsedToHave(t *testing.T) {
	var p Palette
	if err := yaml.Unmarshal([]byte(
		"text: \"#111111\"\ntext_dim: \"#222222\"\ntext_bright: \"#333333\"\n"+
			"border: \"#444444\"\nselected_bg: \"#555555\"\naccent: \"#666666\"\n"), &p); err != nil {
		t.Fatalf("a palette in the old names was refused: %v", err)
	}

	for name, got := range map[string]string{
		"foreground": p.Foreground, "dark_foreground": p.DarkFg,
		"bright_foreground": p.BrightFg, "muted": p.Muted,
		"selection": p.Selection, "accent": p.Accent,
	} {
		if want := map[string]string{
			"foreground": "#111111", "dark_foreground": "#222222",
			"bright_foreground": "#333333", "muted": "#444444",
			"selection": "#555555", "accent": "#666666",
		}[name]; got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

// A name that is neither is still refused, saying which line it is on and what
// the colours are actually called — the two things someone editing the file by
// hand cannot work out for themselves.
func TestAColourNameThatIsNeitherIsRefusedByNameAndLine(t *testing.T) {
	err := yaml.Unmarshal([]byte("accent: \"#ff8800\"\nbg: \"#000000\"\n"), new(Palette))
	if err == nil {
		t.Fatal("bg was accepted as a palette colour")
	}
	for _, want := range []string{`"bg"`, "line 2", "selection", "dark_foreground"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the complaint does not say %s: %v", want, err)
		}
	}
}

// A palette that has been renamed still resolves under the name a config file
// already has for it, and the old name is not offered back as one of the list.
func TestAPaletteThatWasRenamedStillResolvesUnderItsOldName(t *testing.T) {
	was, ok := Lookup("tokyonight")
	if !ok {
		t.Fatal("tokyonight no longer resolves to anything")
	}
	if now, _ := Lookup("tokyo-night"); was != now {
		t.Errorf("tokyonight resolves to %+v, want the same palette as tokyo-night", was)
	}
	if slices.Contains(BuiltinNames(), "tokyonight") {
		t.Error("tokyonight is listed as well as tokyo-night, so the picker offers it twice")
	}
}

// A colour in a palette is one of three things, and each draws as what it
// says. That includes one written with spaces around it: Validate trimmed
// them, lipgloss did not, and a hex colour that had been accepted drew as no
// colour at all.
func TestAColourIsHexANumberFromTheTerminalsPaletteOrDefault(t *testing.T) {
	restore(t)
	for value, want := range map[string]color.Color{
		"#7aa2f7":     color.RGBA{0x7a, 0xa2, 0xf7, 0xff},
		"#FFF":        color.RGBA{0xff, 0xff, 0xff, 0xff},
		" #7aa2f7 ":   color.RGBA{0x7a, 0xa2, 0xf7, 0xff},
		"4":           ansi.BasicColor(4),
		"15":          ansi.BasicColor(15),
		"08":          ansi.BasicColor(8),
		"255":         ansi.IndexedColor(255),
		"default":     lipgloss.NoColor{},
		"Default":     lipgloss.NoColor{},
		" default\t ": lipgloss.NoColor{},
	} {
		if err := (Palette{Accent: value}).Validate(); err != nil {
			t.Errorf("%q was refused: %v", value, err)
			continue
		}
		Apply(Palette{Accent: value})
		if Accent != want {
			t.Errorf("%q drew as %#v, want %#v", value, Accent, want)
		}
	}
}

// Anything else is refused, with what to write instead. lipgloss would have
// drawn most of these as something: a name or 0x10 as no colour at all, -1 as
// 1, and 256 as a 24-bit colour nobody chose.
func TestAColourThatIsNoneOfThoseIsRefusedWithWhatToWrite(t *testing.T) {
	for _, bad := range []string{"256", "1000", "-1", "+4", "0x10", "4.0", "1e3", "blue", "#12345", "#ggg", "defaults", "none"} {
		err := (Palette{Accent: bad}).Validate()
		if err == nil {
			t.Errorf("%q was accepted", bad)
			continue
		}
		for _, want := range []string{"accent", `"` + bad + `"`, "#7aa2f7", "0 to 255", "default"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%q: the complaint does not say %s: %v", bad, want, err)
			}
		}
	}
}

// A palette that sets one colour keeps the terminal's for the rest, since
// that is the default an omitted colour comes from.
func TestAPaletteThatSetsOneColourTakesTheRestFromTheTerminal(t *testing.T) {
	restore(t)
	Apply(Palette{Accent: "#ff8800"})

	if Accent != (color.RGBA{0xff, 0x88, 0x00, 0xff}) {
		t.Errorf("accent = %#v, want the palette's own", Accent)
	}
	if Foreground != (lipgloss.NoColor{}) {
		t.Errorf("foreground = %#v, want the terminal's own", Foreground)
	}
	if DarkFg != ansi.BasicColor(8) {
		t.Errorf("dark_foreground = %#v, want the terminal's grey", DarkFg)
	}
}
