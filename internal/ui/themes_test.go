package ui

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/cuonggt/omassh/internal/ui/theme"
)

// accent is the palette's most visible colour, and stands in for "the
// interface is drawn in this theme" — the styles are global, so a test can
// read them the way the renderer does.
func accent() string { return fmt.Sprint(theme.Accent) }

// themeHarness builds a model with a theme picker worth opening, and puts the
// global palette back afterwards so one test cannot colour the next.
func themeHarness(t *testing.T, apply ...func(*Options)) (*harness, *string) {
	t.Helper()
	saved := new(string)
	h := newHarness(t, func(o *Options) {
		o.Theme = theme.DefaultName
		o.SaveTheme = func(name string) error { *saved = name; return nil }
		for _, fn := range apply {
			fn(o)
		}
	})
	t.Cleanup(func() { theme.Apply(theme.Builtin[theme.DefaultName]) })
	return h, saved
}

func TestPickingAThemeRecoloursAndSavesIt(t *testing.T) {
	h, saved := themeHarness(t)
	was := accent()

	h.press("T")
	h.mustContain("Theme")
	for _, name := range theme.BuiltinNames() {
		h.mustContain(name)
	}

	// The list opens on the theme in effect, so moving once lands somewhere
	// else and the interface changes colour under the dialog.
	h.press("j")
	if accent() == was {
		t.Fatal("moving through the picker did not recolour anything")
	}
	preview := accent()

	h.press("enter")
	if *saved == "" {
		t.Fatal("keeping a theme saved nothing")
	}
	if *saved == theme.DefaultName {
		t.Errorf("saved %q, which is the theme it started on", *saved)
	}
	if accent() != preview {
		t.Error("the colours changed again on ↵; what was previewed is what should be kept")
	}
	if h.m.mode != modeBrowse {
		t.Error("the picker stayed open")
	}
}

func TestLeavingTheThemePickerPutsTheColoursBack(t *testing.T) {
	h, saved := themeHarness(t)
	was := accent()

	h.press("T", "j", "j")
	if accent() == was {
		t.Fatal("nothing was previewed, so there is nothing to undo")
	}

	h.press("esc")
	if accent() != was {
		t.Errorf("accent = %s after esc, want the original %s", accent(), was)
	}
	if *saved != "" {
		t.Errorf("esc saved %q", *saved)
	}
}

func TestAPaletteFromTheConfigFileIsOfferedToo(t *testing.T) {
	// A theme defined in the config file is as pickable as a built-in, and a
	// name shared with one appears once, resolving the way it does at startup.
	h, saved := themeHarness(t, func(o *Options) {
		o.Themes = map[string]theme.Palette{
			"mine": {Accent: "#ff8800"},
			"nord": {Accent: "#000001"},
		}
	})
	h.press("T")
	h.mustContain("mine")

	names := h.m.themeNames()
	seen := 0
	for _, n := range names {
		if n == "nord" {
			seen++
		}
	}
	if seen != 1 {
		t.Errorf("nord appears %d times in %v, want once", seen, names)
	}

	// Walk to the custom palette and keep it.
	for range len(names) {
		if names[h.m.themes.idx] == "mine" {
			break
		}
		h.press("j")
	}
	h.press("enter")
	if *saved != "mine" {
		t.Fatalf("saved %q, want mine", *saved)
	}
	// The palette is resolved to a colour, so compare against the same hex
	// put through the same conversion rather than against the text.
	if want := fmt.Sprint(lipgloss.Color("#ff8800")); accent() != want {
		t.Errorf("accent = %s, want the custom %s", accent(), want)
	}
}

func TestAThemeThatCannotBeSavedStillApplies(t *testing.T) {
	// Losing the colours as well as the setting would be two disappointments
	// for one failure.
	h, _ := themeHarness(t, func(o *Options) {
		o.SaveTheme = func(string) error { return fmt.Errorf("read-only file system") }
	})
	was := accent()
	h.press("T", "j", "enter")

	if accent() == was {
		t.Error("a save failure reverted the colours too")
	}
	if !h.contains("read-only file system") {
		t.Errorf("the failure was not reported:\n%s", h.screen())
	}
}

// longTheme is a palette named the way people name them in a config file,
// rather than the way the built-ins are named.
const longTheme = "capichi-production-dark-high-contrast"

func withLongTheme(o *Options) {
	o.Themes = map[string]theme.Palette{longTheme: {Accent: "#ff8800"}}
}

// The picker is the only place a palette's name is written, and it was drawn
// in a box fixed at 34 cells however much room there was — so a name from the
// config file lost its end on a terminal with a hundred columns to spare, and
// two names differing late in them drew as the same row.
func TestALongThemeNameIsListedInFull(t *testing.T) {
	h, _ := themeHarness(t, withLongTheme)
	h.press("T")
	h.mustContain(longTheme)
	// The built-ins are what the narrow box was for; widening for one long
	// name must not stop them being listed.
	for _, name := range theme.BuiltinNames() {
		h.mustContain(name)
	}
}

// Widening happens only when a name asks for it. With the built-ins alone the
// picker is the size it has always been, because what it is for is the
// interface showing around it rather than the list itself.
func TestTheThemePickerIsUnchangedForTheBuiltIns(t *testing.T) {
	h, _ := themeHarness(t)
	h.press("T")
	if got := h.m.themeWidth(); got != 34 {
		t.Errorf("the picker is %d cells wide for the built-ins, want the 34 it has always been", got)
	}
}

// Widening stops at the frame. Where the name will not fit it is cut with an
// ellipsis, rather than drawn through the border or off the screen.
func TestTheThemePickerStaysInsideTheFrame(t *testing.T) {
	h, _ := themeHarness(t, withLongTheme)
	for _, size := range [][2]int{{200, 50}, {100, 30}, {60, 20}, {40, 12}} {
		h.send(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		h.press("T")
		if h.m.mode != modeTheme {
			t.Fatalf("%dx%d: T did not open the picker", size[0], size[1])
		}
		for i, l := range strings.Split(h.screen(), "\n") {
			if w := ansiWidth(l); w > size[0] {
				t.Errorf("%dx%d line %d is %d wide, want at most %d", size[0], size[1], i, w, size[0])
			}
		}
		h.press("esc")
	}
}

var sgrRE = regexp.MustCompile(`\x1b\[([0-9;:]*)m`)

// styleAt is the style in effect where text is first drawn in s: the
// parameters of the last SGR sequence before it.
func styleAt(s, text string) string {
	i := strings.Index(s, text)
	if i < 0 {
		return ""
	}
	all := sgrRE.FindAllStringSubmatch(s[:i], -1)
	if len(all) == 0 {
		return ""
	}
	return all[len(all)-1][1]
}

// foreignColours is every sequence in s that draws in a colour given by value,
// or by a number past the sixteen a terminal's scheme sets: 38;2;r;g;b and
// 38;5;n, and the same for a background.
func foreignColours(s string) []string {
	var out []string
	for _, m := range sgrRE.FindAllStringSubmatch(s, -1) {
		for _, p := range strings.FieldsFunc(m[1], func(r rune) bool { return r == ';' || r == ':' }) {
			if p == "38" || p == "48" || p == "58" {
				out = append(out, strconv.Quote(m[0]))
				break
			}
		}
	}
	return out
}

// Nothing configured means the terminal's own colours on every screen,
// including what is drawn by components that bring colours of their own. A
// form's hints were a fixed grey from the 256-colour cube: the one thing on the
// screen the terminal's scheme had no say in.
func TestTheDefaultThemeDrawsEveryScreenInTheTerminalsOwnColours(t *testing.T) {
	theme.Apply(theme.Builtin[theme.DefaultName])
	t.Cleanup(func() { theme.Apply(theme.Builtin[theme.DefaultName]) })

	for _, screen := range []struct {
		name string
		keys []string
	}{
		{"the host list", []string{"2"}},
		{"a form", []string{"2", "n", "zanzibar", "tab"}},
		{"the filter", []string{"/"}},
		{"help", []string{"?"}},
		{"the theme picker", []string{"T"}},
	} {
		t.Run(screen.name, func(t *testing.T) {
			h := newHarness(t)
			h.addHost("web-01", "10.0.1.1")
			h.addHost("db-01", "10.0.2.1")
			h.press(screen.keys...)
			if bad := foreignColours(h.m.View().Content); len(bad) > 0 {
				t.Errorf("drawn in colours the terminal's scheme does not set: %s\n%s",
					strings.Join(bad, " "), h.screen())
			}
		})
	}
}

// A form's inputs take their colours from the palette like everything else.
// bubbles gave them colours of its own — the terminal's white for any field not
// being typed in — and on a light terminal white is the colour of the page, so
// every value in a form but the one under the cursor was all but invisible.
func TestAFormsFieldsAreDrawnInThePalettesColours(t *testing.T) {
	theme.Apply(theme.Palette{Text: "#123456", TextDim: "#654321"})
	t.Cleanup(func() { theme.Apply(theme.Builtin[theme.DefaultName]) })
	h := newHarness(t)

	h.press("2", "n")
	h.type_("zanzibar")
	h.press("tab")
	content := h.m.View().Content

	if got := styleAt(content, "zanzibar"); !strings.Contains(got, "38;2;18;52;86") {
		t.Errorf("a value not being typed in is drawn with %q, want the palette's text colour", got)
	}
	if got := styleAt(content, "private key"); !strings.Contains(got, "38;2;101;67;33") {
		t.Errorf("a hint is drawn with %q, want the palette's dim colour", got)
	}
}

// A kept filter stays on screen beside the list through a trip to the picker,
// and is the one input that does. It was built with the colours in effect when
// omassh started, so it has to be handed each palette the picker shows —
// including the one esc puts back.
func TestAKeptFilterIsRecolouredWithEverythingElse(t *testing.T) {
	one, two := theme.Palette{Text: "#111111"}, theme.Palette{Text: "#222222"}
	theme.Apply(one)
	h, _ := themeHarness(t, func(o *Options) {
		o.Theme = "one"
		o.Themes = map[string]theme.Palette{"one": one, "two": two}
	})
	h.addHost("zanzibar", "10.0.1.1")
	h.press("/")
	h.type_("zanz")
	h.press("enter", "T")
	for range len(h.m.themes.names) {
		if h.m.themes.names[h.m.themes.idx] == "two" {
			break
		}
		h.press("j")
	}

	if got := styleAt(h.m.filter.View(), "zanz"); !strings.Contains(got, "38;2;34;34;34") {
		t.Errorf("previewing two, the kept filter is drawn with %q, want two's text colour", got)
	}
	h.press("esc")
	if got := styleAt(h.m.filter.View(), "zanz"); !strings.Contains(got, "38;2;17;17;17") {
		t.Errorf("after esc the kept filter is drawn with %q, want one's again", got)
	}
}
