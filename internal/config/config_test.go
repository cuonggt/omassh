package config

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cuonggt/omassh/internal/ui/theme"
)

func write(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// Omassh must be fully usable with no configuration at all.
func TestMissingFileIsNotAnError(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "absent.yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Theme != "tokyonight" {
		t.Errorf("defaults not applied: %+v", c)
	}
}

// Keys the file omits must keep their defaults, not become zero values.
func TestPartialConfigKeepsDefaults(t *testing.T) {
	c, err := Load(write(t, "theme: nord\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Theme != "nord" {
		t.Errorf("Theme = %q", c.Theme)
	}
	if c.ProbeTimeout != "2s" {
		t.Errorf("ProbeTimeout = %q, want the default", c.ProbeTimeout)
	}
}

func TestFullConfig(t *testing.T) {
	c, err := Load(write(t, `
theme: mine
themes:
  mine:
    accent: "#ff8800"
keys:
  connect: c
ssh_options:
  - ConnectTimeout=10
probe_timeout: 750ms
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	p, err := c.Palette()
	if err != nil {
		t.Fatalf("Palette: %v", err)
	}
	if p.Accent != "#ff8800" {
		t.Errorf("Accent = %q", p.Accent)
	}
	km, err := c.Keymap()
	if err != nil {
		t.Fatalf("Keymap: %v", err)
	}
	if km.Key("connect") != "c" {
		t.Errorf("connect bound to %q, want c", km.Key("connect"))
	}
	if len(c.SSHOptions) != 1 || c.SSHOptions[0] != "ConnectTimeout=10" {
		t.Errorf("SSHOptions = %v", c.SSHOptions)
	}
	if d, _ := c.ProbeDuration(); d != 750*time.Millisecond {
		t.Errorf("ProbeDuration = %v", d)
	}
}

// A user-defined palette wins over a built-in of the same name.
func TestUserThemeShadowsBuiltin(t *testing.T) {
	c, err := Load(write(t, "theme: nord\nthemes:\n  nord:\n    accent: \"#123456\"\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	p, _ := c.Palette()
	if p.Accent != "#123456" {
		t.Errorf("Accent = %q, want the user's override", p.Accent)
	}
}

// A broken config must be reported, not silently ignored — otherwise settings
// appear to do nothing for no visible reason.
func TestBadConfigIsReported(t *testing.T) {
	cases := map[string]string{
		"malformed yaml": "theme: [unclosed\n",
		"unknown theme":  "theme: neon-dreams\n",
		"bad colour":     "theme: mine\nthemes:\n  mine:\n    accent: \"not-a-colour\"\n",
		"unknown action": "keys:\n  teleport: t\n",
		"reserved key":   "keys:\n  connect: ctrl+c\n",
		"key conflict":   "keys:\n  connect: e\n",
		"bad duration":   "probe_timeout: soon\n",
		// Decode reads one document, so a second --- section would set
		// nothing, as silently as a mistyped key would.
		"two documents": "theme: nord\n---\ntheme: gruvbox\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			path := write(t, body)
			c, err := Load(path)
			if err == nil {
				t.Fatalf("Load accepted %s", name)
			}
			if !strings.Contains(err.Error(), filepath.Base(path)) {
				t.Errorf("error does not name the file: %v", err)
			}
			// A rejected config must leave usable defaults behind.
			if c.Theme != "tokyonight" {
				t.Errorf("config not reset to defaults after error: %+v", c)
			}
		})
	}
}

// Opening with the marker is a plain YAML habit and is still one document —
// only a second --- starts a second one.
func TestALeadingDocumentMarkerIsFine(t *testing.T) {
	c, err := Load(write(t, "---\ntheme: nord\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Theme != "nord" {
		t.Errorf("Theme = %q", c.Theme)
	}
}

func TestExampleIsValid(t *testing.T) {
	c, err := Load(write(t, Example("/tmp/omassh/config.yaml")))
	if err != nil {
		t.Fatalf("the shipped example does not load: %v", err)
	}
	if err := c.Validate(); err != nil {
		t.Errorf("the shipped example does not validate: %v", err)
	}
}

// A config file is written by hand, so what is wrong with one has to be said
// in the words of the file. yaml says "field bg not found in type
// theme.Palette", which names a Go type at someone choosing colours and never
// says what the name should have been.
func TestAKeyThatIsNotASettingIsNamedInOmasshsWords(t *testing.T) {
	cases := map[string]struct {
		body string
		want []string
	}{
		"a setting": {
			body: "ssh_option:\n  - ConnectTimeout=10\n",
			want: []string{`"ssh_option" is not a setting`, "line 1"},
		},
		"a palette colour": {
			// Palette keys are not guessable — text_bright, selected_bg — so
			// the complaint has to list them rather than only refuse. Written
			// out here rather than derived: a colour added to the palette
			// should fail this and be added deliberately.
			body: "themes:\n  mine:\n    bg: \"#101014\"\n",
			want: []string{`"bg" is not a palette colour`, "line 3",
				"text, text_dim, text_bright, accent, green, yellow, red, magenta, border, selected_bg"},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Load(write(t, tc.body))
			if err == nil {
				t.Fatal("Load accepted it")
			}
			got := err.Error()
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("does not say %q: %s", w, got)
				}
			}
			for _, leak := range []string{"not found in type", "theme.Palette", "config.Config", "yaml: unmarshal errors"} {
				if strings.Contains(got, leak) {
					t.Errorf("leaks %q at the user: %s", leak, got)
				}
			}
		})
	}
}

// yaml collects every mistake in a file, and all of them have to survive the
// rewriting. Reporting only the first would mean fixing a config file one
// failed startup at a time.
func TestEveryMistakeInAFileIsReported(t *testing.T) {
	_, err := Load(write(t, "themes:\n  mine:\n    bg: \"#101014\"\nssh_option: [a]\n"))
	if err == nil {
		t.Fatal("Load accepted a file with two mistakes in it")
	}
	got := err.Error()
	for _, w := range []string{`"bg"`, `"ssh_option"`} {
		if !strings.Contains(got, w) {
			t.Errorf("does not mention %s: %s", w, got)
		}
	}
}

// The colours the complaint offers have to be the ones that actually work.
// That is the whole reason the list is read off the struct rather than written
// out beside it — a colour added to the palette and not to the list would be
// named as wrong by the very message meant to help.
func TestEveryColourTheComplaintOffersIsAccepted(t *testing.T) {
	keys := theme.PaletteKeys()
	if !slices.Contains(keys, "selected_bg") {
		t.Fatalf("PaletteKeys does not list the palette: %v", keys)
	}
	for _, key := range keys {
		body := fmt.Sprintf("theme: mine\nthemes:\n  mine:\n    %s: \"#123456\"\n", key)
		if _, err := Load(write(t, body)); err != nil {
			t.Errorf("%s is offered as a palette colour but refused: %v", key, err)
		}
	}
}

// Every setting in the file, given the wrong shape. yaml answers with Go types
// and YAML tags — "cannot unmarshal !!str `blue` into theme.Palette" — which is
// nothing someone editing a config file can act on. The schema is small and
// fixed, so every position this can happen at is listed here; a setting of a
// new type fails this test until it has words of its own.
//
// The line numbers are part of what is asserted. The offending value is not
// quoted — yaml cuts it short to "Connect...", which reads like damage — so
// the line is the only thing left saying where to look.
func TestAValueOfTheWrongShapeIsDescribedInWords(t *testing.T) {
	cases := map[string]struct{ body, want string }{
		"the file itself":  {"- a\n- b\n", "line 1: this should be settings, not a list"},
		"theme":            {"theme:\n  - a\n", "line 2: this should be a single value, not a list"},
		"themes":           {"themes: hello\n", "line 1: this should be a palette under each name, not text"},
		"one palette":      {"themes:\n  mine: blue\n", `line 2: this should be a palette of colour: "#rrggbb" lines, not text`},
		"a palette number": {"themes:\n  mine: 2.5\n", `line 2: this should be a palette of colour: "#rrggbb" lines, not a number`},
		"one colour":       {"themes:\n  mine:\n    accent:\n      a: b\n", "line 4: this should be a single value, not a block of key: value lines"},
		"keys":             {"keys: true\n", "line 1: this should be a block of key: value lines, not true or false"},
		"one binding":      {"keys:\n  connect:\n    - a\n", "line 3: this should be a single value, not a list"},
		"ssh_options":      {"ssh_options: 10\n", "line 1: this should be a list, not a number"},
		"probe_timeout":    {"probe_timeout:\n  a: b\n", "line 2: this should be a single value, not a block of key: value lines"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Load(write(t, tc.body))
			if err == nil {
				t.Fatal("Load accepted it")
			}
			got := err.Error()
			if !strings.Contains(got, tc.want) {
				t.Errorf("want %q\n got %s", tc.want, got)
			}
			for _, leak := range []string{"cannot unmarshal", "!!", "theme.Palette", "config.Config", "map[string]", "[]string"} {
				if strings.Contains(got, leak) {
					t.Errorf("leaks %q at the user: %s", leak, got)
				}
			}
		})
	}
}

// A duration that will not parse must be described the way the file spells
// one. time.ParseDuration says `time: invalid duration "soon"`, which puts a
// Go package name where this file has setting names.
func TestABadProbeTimeoutSaysWhatALengthOfTimeLooksLike(t *testing.T) {
	cases := map[string]struct{ body, value string }{
		"in words":    {"probe_timeout: soon\n", `"soon"`},
		"without ing": {"probe_timeout: 5\n", `"5"`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Load(write(t, tc.body))
			if err == nil {
				t.Fatal("Load accepted it")
			}
			got := err.Error()
			for _, w := range []string{"probe_timeout", tc.value, "2s"} {
				if !strings.Contains(got, w) {
					t.Errorf("does not say %s: %s", w, got)
				}
			}
			for _, leak := range []string{"time: invalid", "time: missing"} {
				if strings.Contains(got, leak) {
					t.Errorf("leaks %q at the user: %s", leak, got)
				}
			}
		})
	}
}

// Zero is a valid duration and a useless timeout: it calls every host down
// before it has been given a chance to answer.
func TestAProbeTimeoutOfZeroOrLessIsRefused(t *testing.T) {
	for _, v := range []string{"0s", "-2s"} {
		_, err := Load(write(t, "probe_timeout: "+v+"\n"))
		if err == nil {
			t.Fatalf("Load accepted probe_timeout: %s", v)
		}
		if !strings.Contains(err.Error(), "longer than zero") {
			t.Errorf("probe_timeout: %s says %v", v, err)
		}
	}
}
