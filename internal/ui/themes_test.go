package ui

import (
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"regexp"
	"slices"
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

// walkTo moves the picker's cursor onto a theme. The list is windowed, so a
// name is on screen only while the window is over it, and a test that wants to
// look at one has to go there the way a person would.
func walkTo(t *testing.T, h *harness, name string) {
	t.Helper()
	for range len(h.m.themes.names) {
		if h.m.themes.names[h.m.themes.idx] == name {
			return
		}
		h.press("j")
	}
	t.Fatalf("the picker never reached %q, walking the whole list", name)
}

// The picker is the only place a palette's name is written, and it was drawn
// in a box fixed at 34 cells however much room there was — so a name from the
// config file lost its end on a terminal with a hundred columns to spare, and
// two names differing late in them drew as the same row.
func TestALongThemeNameIsListedInFull(t *testing.T) {
	h, _ := themeHarness(t, withLongTheme)
	h.press("T")
	walkTo(t, h, longTheme)
	h.mustContain(longTheme)
}

// omarchyHome lays out a theme directory the way Omarchy does and points
// Omassh at it, and gives back a way to switch the theme — the colours, and
// the token Omarchy writes beside them to say that it has.
func omarchyHome(t *testing.T, colours string) func(string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "current", "theme")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("lay out a theme: %v", err)
	}
	was := theme.OmarchyFile
	t.Cleanup(func() { theme.OmarchyFile = was })
	theme.OmarchyFile = filepath.Join(dir, "colors.toml")

	switches := 0
	set := func(body string) {
		t.Helper()
		switches++
		if err := os.WriteFile(theme.OmarchyFile, []byte(body), 0o600); err != nil {
			t.Fatalf("write the colours: %v", err)
		}
		token := fmt.Appendf(nil, "%d-switch\n", switches)
		if err := os.WriteFile(filepath.Join(filepath.Dir(dir), "theme.generation"), token, 0o600); err != nil {
			t.Fatalf("write the generation: %v", err)
		}
	}
	set(colours)
	return set
}

// omarchyHarness is Omassh started on Omarchy's own theme, with the colours
// applied the way main applies them at startup.
func omarchyHarness(t *testing.T) *harness {
	t.Helper()
	h, _ := themeHarness(t, func(o *Options) { o.Theme = theme.OmarchyName })
	p, err := theme.Resolve(theme.OmarchyName)
	if err != nil {
		t.Fatalf("resolve omarchy: %v", err)
	}
	h.m.applyTheme(p)
	return h
}

// Switching the Omarchy theme repaints Omassh, rather than leaving it in the
// colours of the theme that was set when it started. Following Omarchy is what
// the theme is called and what it is for; following it only until the next
// time someone switches would be most of a feature.
func TestOmasshRepaintsWhenOmarchysThemeChangesUnderIt(t *testing.T) {
	set := omarchyHome(t, "accent = \"#112233\"\n")
	h := omarchyHarness(t)

	if h.m.Init() == nil {
		t.Fatal("nothing is following Omarchy's theme, though that is the theme in effect")
	}
	was := accent()

	set("accent = \"#445566\"\n")
	if cmd := h.send(omarchyTickMsg{}); cmd == nil {
		t.Error("the watch stopped at the first change it saw")
	}

	if accent() == was {
		t.Error("Omarchy's theme changed and Omassh kept the colours it had")
	}
	if theme.Accent != (color.RGBA{0x44, 0x55, 0x66, 0xff}) {
		t.Errorf("the accent is %#v, want the one Omarchy was just set to", theme.Accent)
	}
}

// A form open while the theme changes is recoloured with everything else. Its
// fields take their colours when it opens, which was the whole story while a
// theme could only change in the picker — and is not one now that Omarchy can
// change it from outside while a form is up.
func TestAFormOpenWhenOmarchysThemeChangesIsRecolouredToo(t *testing.T) {
	set := omarchyHome(t, "foreground = \"#112233\"\n")
	h := omarchyHarness(t)

	h.press("2", "n")
	h.type_("zanzibar")
	h.press("tab")

	set("foreground = \"#445566\"\n")
	h.send(omarchyTickMsg{})

	if got := styleAt(h.m.View().Content, "zanzibar"); !strings.Contains(got, "38;2;68;85;102") {
		t.Errorf("a field in the open form is drawn with %q, want the colour Omarchy was just set to", got)
	}
}

// Nothing is woken for a theme nobody is following. A timer ticking on every
// machine, for a setting that means something on few of them, is a cost with
// nothing to show for it.
func TestNothingFollowsOmarchyUnlessItIsTheThemeInEffect(t *testing.T) {
	omarchyHome(t, "accent = \"#112233\"\n")
	h, _ := themeHarness(t) // the default theme, not Omarchy's

	if h.m.Init() != nil {
		t.Error("a watch was started for a theme that is not Omarchy's")
	}
	if cmd := h.send(omarchyTickMsg{}); cmd != nil {
		t.Error("a tick that arrived anyway scheduled another")
	}
}

// Picking away from Omarchy's theme stops following it, and picking it again
// starts over — with one watch running either way, not one per visit to the
// picker.
func TestFollowingOmarchyStartsAndStopsWithTheThemeItself(t *testing.T) {
	omarchyHome(t, "accent = \"#112233\"\n")
	h := omarchyHarness(t)
	h.m.Init()

	h.m.opts.Theme = "nord"
	if cmd := h.send(omarchyTickMsg{}); cmd != nil {
		t.Error("the watch kept ticking for a theme that is no longer Omarchy's")
	}
	if h.m.watchingOmarchy {
		t.Error("the watch still says it is running")
	}

	h.press("T")
	walkTo(t, h, theme.OmarchyName)
	h.press("enter")
	if !h.m.watchingOmarchy {
		t.Fatal("picking Omarchy's theme did not start following it again")
	}
	// A second pick must not leave two of them running.
	h.press("T")
	walkTo(t, h, theme.OmarchyName)
	if cmd := h.m.watchOmarchy(); cmd != nil {
		t.Error("a second watch was started beside the one already running")
	}
}

// Omarchy's own current theme is offered where there is an Omarchy to read it
// from, and nowhere else. A name in this list is a thing you can look at, so
// one that previewed as nothing on every machine but a few would be a row that
// does not answer to the key that moved onto it.
func TestThePickerOffersOmarchysOwnThemeOnlyWhereOmarchyIs(t *testing.T) {
	h, _ := themeHarness(t)

	h.press("T")
	if slices.Contains(h.m.themes.names, theme.OmarchyName) {
		t.Error("omarchy is offered with no Omarchy theme on this machine to read")
	}
	h.press("esc")

	was := theme.OmarchyFile
	t.Cleanup(func() { theme.OmarchyFile = was })
	theme.OmarchyFile = filepath.Join("theme", "testdata", "tokyo-night-colors.toml")

	h.press("T")
	if !slices.Contains(h.m.themes.names, theme.OmarchyName) {
		t.Fatalf("omarchy is not offered though its theme reads: %v", h.m.themes.names)
	}

	// Moving onto it has to recolour the interface like any other row, which
	// is the thing that proves the picker resolves it rather than merely
	// listing it.
	walkTo(t, h, theme.OmarchyName)
	h.mustContain(theme.OmarchyName)
	if got, want := accent(), fmt.Sprint(theme.Builtin["tokyo-night"].Accent); !strings.Contains(got, "122") {
		t.Errorf("the accent previewed as %s, want the #7aa2f7 the file names (%s)", got, want)
	}
}

// Every built-in is offered, which since the list is windowed is not the same
// as every one being on screen at once: the picker has to be walked to see
// them, and walking it has to reach all of them.
func TestThePickerOffersEveryBuiltInPalette(t *testing.T) {
	h, _ := themeHarness(t)
	h.press("T")
	for _, name := range theme.BuiltinNames() {
		walkTo(t, h, name)
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

// The picker windows its list, like every other list here, and says how far
// through it the cursor is — once a list scrolls, the border alone no longer
// shows that there is more below.
//
// It used to draw every palette there was. That was fine while five were built
// in; with Omarchy's twenty the list ran out of the bottom of the dialog, and
// a config file with a dozen palettes of its own had always done so. The cap
// is tighter than the frame on purpose: the picker is for the recoloured
// interface around it, which a list long enough to fill the terminal covers.
func TestTheThemePickerWindowsItsListRatherThanSpillingPastItsBorder(t *testing.T) {
	h, _ := themeHarness(t)
	h.press("T")

	content := testH - statusHeight
	rows := h.m.themeListRows(content)
	if len(h.m.themes.names) <= rows {
		t.Fatalf("%d themes fit in %d rows, so nothing here is windowed",
			len(h.m.themes.names), rows)
	}

	if got := strings.Count(h.m.themeBody(h.m.themeWidth()-4, content), "\n") + 1; got != rows {
		t.Errorf("the picker drew %d rows, want the %d it has room for", got, rows)
	}
	h.mustContain(fmt.Sprintf("%d/%d", h.m.themes.idx+1, len(h.m.themes.names)))
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
	theme.Apply(theme.Palette{Foreground: "#123456", DarkFg: "#654321"})
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
	one, two := theme.Palette{Foreground: "#111111"}, theme.Palette{Foreground: "#222222"}
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
