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
		"text": Text, "text_dim": TextDim, "text_bright": TextBrt,
		"accent": Accent, "green": Green, "yellow": Yellow, "red": Red,
		"magenta": Magenta, "border": Border, "selected_bg": SelBg,
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

// The terminal's own background is no highlight at all, so a palette that
// gives the selection none of its own gets the terminal's own highlight: its
// colours swapped. That swap leaves text_bright out. A colour of ours as the
// bar could be the colour of the page — white, on a light terminal — and the
// row under the cursor would vanish rather than stand out.
func TestASelectionWithNoBackgroundOfItsOwnIsTheTerminalsOwnHighlight(t *testing.T) {
	restore(t)
	for name, p := range map[string]Palette{
		"the default":            Builtin[DefaultName],
		"a bright text of yours": {TextBrt: "#ffffff"},
	} {
		Apply(p)
		got := params(Selected.Render("web-01"))
		if !slices.Contains(got, "7") {
			t.Errorf("%s: the selected row is not drawn in reverse: %q", name, got)
		}
		if slices.Contains(got, "38") || slices.Contains(got, "48") {
			t.Errorf("%s: the selected row brings a colour of its own into the swap: %q", name, got)
		}
	}

	// A palette that names a background still gets a bar of exactly that.
	Apply(Builtin["tokyonight"])
	got := params(Selected.Render("web-01"))
	if !strings.Contains(strings.Join(got, ";"), "48;2;40;52;87") || slices.Contains(got, "7") {
		t.Errorf("tokyonight's selection is %q, want its own #283457 bar and no reverse", got)
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
	if Text != (lipgloss.NoColor{}) {
		t.Errorf("text = %#v, want the terminal's own", Text)
	}
	if TextDim != ansi.BasicColor(8) {
		t.Errorf("text_dim = %#v, want the terminal's grey", TextDim)
	}
}
