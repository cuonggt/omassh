package theme

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// write puts a colors.toml somewhere the test owns, and gives back its path.
func writeColors(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "colors.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write colors.toml: %v", err)
	}
	return path
}

// Omarchy's own tokyo-night, read from the file Omarchy ships, has to come out
// as the tokyo-night built in here — they are the same ten colours out of the
// same document, so anything the reader or the fallbacks got wrong shows up as
// a difference between the two.
//
// The fixture is Omarchy's file as it ships, not one written for the test: a
// reader checked only against input invented alongside it is a reader checked
// against its own assumptions.
func TestReadingOmarchysOwnThemeGivesThePaletteBuiltInUnderTheSameName(t *testing.T) {
	got, err := ReadOmarchy(filepath.Join("testdata", "tokyo-night-colors.toml"))
	if err != nil {
		t.Fatalf("ReadOmarchy: %v", err)
	}
	if want := Builtin["tokyo-night"]; got != want {
		t.Errorf("Omarchy's tokyo-night reads as\n%+v\nwant the built-in\n%+v", got, want)
	}
}

// A theme may leave a role out, and Omarchy has a cascade for every one of
// them so that every consumer lands on the same palette. Omassh follows it
// rather than guessing, or the host list would disagree with the bar at the
// top of the screen about what the theme is.
func TestARoleAThemeLeavesOutIsFilledInTheWayOmarchyFillsIt(t *testing.T) {
	// Written in the old ANSI and short names alone, which is the shape of a
	// theme made before the semantic palette and the reason the cascade is
	// there at all.
	p, err := ReadOmarchy(writeColors(t, `
fg = "#aaaaaa"
color8 = "#555555"
color15 = "#ffffff"
color1 = "#ff0000"
color2 = "#00ff00"
color3 = "#ffff00"
purple = "#ff00ff"
background = "#111111"
`))
	if err != nil {
		t.Fatalf("ReadOmarchy: %v", err)
	}

	for _, c := range []struct{ role, got, want string }{
		{"foreground", p.Foreground, "#aaaaaa"},      // from fg
		{"dark_foreground", p.DarkFg, "#555555"},     // from color8
		{"bright_foreground", p.BrightFg, "#ffffff"}, // from color15
		{"muted", p.Muted, "#555555"},                // from color8
		{"selection", p.Selection, "#555555"},        // color8 before background
		{"red", p.Red, "#ff0000"},
		{"green", p.Green, "#00ff00"},
		{"yellow", p.Yellow, "#ffff00"},
		{"magenta", p.Magenta, "#ff00ff"}, // from purple
	} {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.role, c.got, c.want)
		}
	}

	// Nothing names an accent, and nothing here should invent one: Apply
	// fills an empty role from the terminal palette, which is a better answer
	// than a colour computed out of the others.
	if p.Accent != "" {
		t.Errorf("accent = %q, want it left for the terminal palette to fill", p.Accent)
	}
}

// colors.toml is Omarchy's file and holds values for consumers Omassh is not
// one of — gradients, rgba() — so one it cannot draw counts as a role the
// theme did not set and the cascade carries on past it. Refusing to start over
// a gradient in someone's theme would be Omassh deciding what Omarchy's file
// is allowed to contain.
func TestAColourOmasshCannotDrawIsPassedOverRatherThanRefused(t *testing.T) {
	p, err := ReadOmarchy(writeColors(t, `
foreground = "linear-gradient(-45deg, #a0a0a0, #ffffff)"
color7 = "#cccccc"
muted = "rgba(85, 85, 85, 0.6)"
color8 = "#555555"
`))
	if err != nil {
		t.Fatalf("ReadOmarchy: %v", err)
	}
	if p.Foreground != "#cccccc" {
		t.Errorf("foreground = %q, want the next colour the cascade offers", p.Foreground)
	}
	if p.Muted != "#555555" {
		t.Errorf("muted = %q, want the next colour the cascade offers", p.Muted)
	}
}

// The file is read the way omarchy-theme-color reads it, so a file Omarchy
// accepts is one Omassh accepts: comments and blank lines are nothing, a
// trailing comment is not part of the value, and the quotes are not either.
func TestAColoursFileIsReadTheWayOmarchyReadsIt(t *testing.T) {
	p, err := ReadOmarchy(writeColors(t, `
# the theme's own notes
mode = "dark"

accent = "#7aa2f7"   # the one people change
  foreground  =  "#a9b1d6"
`))
	if err != nil {
		t.Fatalf("ReadOmarchy: %v", err)
	}
	if p.Accent != "#7aa2f7" {
		t.Errorf("accent = %q, want the colour without the comment after it", p.Accent)
	}
	if p.Foreground != "#a9b1d6" {
		t.Errorf("foreground = %q, want the spaces around it gone", p.Foreground)
	}
}

// Asking for Omarchy's theme where there is no Omarchy says what to do
// instead. It is a theme that only works on one kind of machine, so the one
// place that has to be said well is the machine it does not work on.
func TestAskingForOmarchyWhereThereIsNoneSaysWhatToDoInstead(t *testing.T) {
	for name, path := range map[string]string{
		"nothing to read": "",
		"nothing there":   filepath.Join(t.TempDir(), "colors.toml"),
	} {
		_, err := ReadOmarchy(path)
		if err == nil {
			t.Fatalf("%s: a palette was returned for a theme that cannot be read", name)
		}
		// Named, and told what to write instead — the picker is no use here,
		// since this is what stops Omassh from starting at all.
		for _, want := range []string{"Omarchy", "theme:", "built-in"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: the complaint does not say %q: %v", name, want, err)
			}
		}
	}
}

// Resolve is the one place a theme name is turned into colours, so it is the
// one place that has to know omarchy is not built in.
func TestResolveReadsOmarchysThemeAndStillKnowsTheBuiltIns(t *testing.T) {
	was := OmarchyFile
	t.Cleanup(func() { OmarchyFile = was })
	OmarchyFile = filepath.Join("testdata", "tokyo-night-colors.toml")

	got, err := Resolve(OmarchyName)
	if err != nil {
		t.Fatalf("Resolve(%q): %v", OmarchyName, err)
	}
	if want := Builtin["tokyo-night"]; got != want {
		t.Errorf("omarchy resolves to %+v, want what its file says", got)
	}
	if !OmarchyAvailable() {
		t.Error("the picker is not offered a theme that reads perfectly well")
	}

	if _, err := Resolve("nord"); err != nil {
		t.Errorf("nord stopped resolving: %v", err)
	}
	if _, err := Resolve("no-such-theme"); err == nil {
		t.Error("an unknown theme was accepted")
	}
}

// The stamp is how following Omarchy's theme stays cheap: a couple of stats
// on a tick rather than a reread and a repaint. It has to change for both of
// the things a switch changes, because Omarchy writes the token first and
// repoints the colours after, and a watcher that noticed only one of them
// would settle on whichever it saw first.
func TestTheStampChangesForEitherHalfOfASwitch(t *testing.T) {
	was := OmarchyFile
	t.Cleanup(func() { OmarchyFile = was })

	dir := filepath.Join(t.TempDir(), "current", "theme")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("lay out a theme: %v", err)
	}
	OmarchyFile = filepath.Join(dir, "colors.toml")
	generation := filepath.Join(filepath.Dir(dir), "theme.generation")

	write := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	write(OmarchyFile, "accent = \"#111111\"\n")
	write(generation, "1-first\n")
	first := OmarchyStamp()

	// The token alone, which is what a switch writes before it repoints the
	// colours at the theme being switched to.
	write(generation, "2-second\n")
	second := OmarchyStamp()
	if second == first {
		t.Error("the stamp did not change when Omarchy said the theme had")
	}

	// The colours alone, for an Omarchy too old to write a token and for a
	// colors.toml changed by hand. Of a different length as well as at a
	// different moment, so this cannot come down to how finely the filesystem
	// records a time.
	write(OmarchyFile, "accent = \"#222222\"\nforeground = \"#333333\"\n")
	if third := OmarchyStamp(); third == second {
		t.Error("the stamp did not change when the colours themselves did")
	}
}

// The suite must never read the colours of whoever is running it. OmarchyFile
// is empty unless a test sets it, exactly as Options.SSHConfig is, and a
// default that pointed at the real file would make the whole suite's idea of
// a theme depend on the machine it ran on.
func TestTheSuiteNeverReadsTheRunnersOwnOmarchyTheme(t *testing.T) {
	if OmarchyFile != "" {
		t.Errorf("OmarchyFile is %q, want empty so the suite reads no theme of its own", OmarchyFile)
	}
	if OmarchyAvailable() {
		t.Error("Omarchy's theme reads as available with nothing configured to read")
	}
}
