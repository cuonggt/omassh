// Package config loads Omassh's YAML configuration.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/cuonggt/omassh/internal/keymap"
	"github.com/cuonggt/omassh/internal/sshx"
	"github.com/cuonggt/omassh/internal/ui/theme"
	"github.com/cuonggt/omassh/internal/yamlerr"
)

// Config is the whole of the config file. Where that lives follows
// os.UserConfigDir, so it is ~/Library/Application Support/omassh/config.yaml
// on macOS and ~/.config/omassh/config.yaml on Linux. Naming only one of them
// has sent people to edit a file that is never read; omassh -h prints the
// resolved path.
type Config struct {
	Theme        string                   `yaml:"theme"`
	Themes       map[string]theme.Palette `yaml:"themes"`
	Keys         map[string]string        `yaml:"keys"`
	SSHOptions   []string                 `yaml:"ssh_options"`
	ProbeTimeout string                   `yaml:"probe_timeout"`
}

func Default() Config {
	return Config{Theme: theme.DefaultName, ProbeTimeout: "2s"}
}

func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "omassh", "config.yaml"), nil
}

// words is what this file's settings are called, for yamlerr to complain in.
//
// yaml says "field bg not found in type theme.Palette", which names a Go type
// at someone choosing colours and never says what the name should have been —
// and for a palette that matters, since the keys are text_bright and
// selected_bg rather than anything guessable.
var words = yamlerr.Vocabulary{
	Field: func(name, typ string) string {
		if strings.HasSuffix(typ, "Palette") {
			return fmt.Sprintf("%q is not a palette colour — they are %s",
				name, strings.Join(theme.PaletteKeys(), ", "))
		}
		return fmt.Sprintf("%q is not a setting", name)
	},
	// The schema is six types wide and fixed; one that is not here is left in
	// yaml's own words rather than guessed at.
	Type: func(typ string) string {
		return map[string]string{
			"config.Config":            "settings",
			"theme.Palette":            `a palette of colour: "#rrggbb" lines`,
			"map[string]theme.Palette": "a palette under each name",
			"map[string]string":        "a block of key: value lines",
			"[]string":                 "a list",
			"string":                   "a single value",
		}[typ]
	},
}

// Load reads path, filling anything unset from the defaults.
//
// A missing file is not an error — Omassh is usable with no configuration at
// all — but a malformed one is, and is reported with the file named, rather
// than being silently ignored and leaving the user wondering why their
// settings did nothing.
func Load(path string) (Config, error) {
	c := Default()

	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return c, nil
		}
		return c, err
	}
	// Decode over the defaults so an absent key keeps its default rather than
	// becoming the zero value.
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	// A key that is not a setting is an error, not something to skip past.
	// Silence is indistinguishable from the file not being read at all, and
	// the settings most often mistyped are the ones where it shows least: a
	// palette with selected rather than selected_bg simply keeps the default
	// colour, and ssh_option without its s quietly passes nothing to ssh.
	dec.KnownFields(true)

	// A file with nothing in it — empty, or only comments — decodes to EOF.
	// That is a config that sets nothing, which is allowed and is what a
	// fresh one looks like.
	if err := dec.Decode(&c); err != nil && !errors.Is(err, io.EOF) {
		return Default(), fmt.Errorf("%s: %w", path, yamlerr.InWords(err, words))
	}
	// Decode reads one document, and a stream can hold several. A second ---
	// section would be skipped in exactly the silent way an unknown key would,
	// so it is an error for the same reason. A leading --- is not one: only a
	// second marker starts a second document.
	if err := dec.Decode(new(Config)); !errors.Is(err, io.EOF) {
		return Default(), fmt.Errorf("%s: this is more than one YAML document — settings after the --- would be skipped", path)
	}
	if err := c.Validate(); err != nil {
		return Default(), fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

func (c Config) Validate() error {
	if _, err := c.Palette(); err != nil {
		return err
	}
	if _, err := c.Keymap(); err != nil {
		return err
	}
	if _, err := c.ProbeDuration(); err != nil {
		return err
	}
	for _, o := range c.SSHOptions {
		if err := sshx.OptionProblem(o); err != nil {
			return fmt.Errorf("ssh_options: %q %w — write it as ConnectTimeout=10", o, err)
		}
	}
	return nil
}

// Palette resolves the configured theme name, preferring a palette defined in
// the config file over a built-in of the same name.
func (c Config) Palette() (theme.Palette, error) {
	name := c.ThemeName()
	if p, ok := c.Themes[name]; ok {
		if err := p.Validate(); err != nil {
			return p, fmt.Errorf("theme %q: %w", name, err)
		}
		return p, nil
	}
	if p, ok := theme.Builtin[name]; ok {
		return p, nil
	}
	return theme.Palette{}, fmt.Errorf("unknown theme %q (built in: %v)", name, theme.BuiltinNames())
}

// ThemeName is the theme in effect, with the default filled in — what the
// picker opens on, and what it marks as current.
func (c Config) ThemeName() string {
	if strings.TrimSpace(c.Theme) == "" {
		return theme.DefaultName
	}
	return c.Theme
}

func (c Config) Keymap() (keymap.Map, error) { return keymap.New(c.Keys) }

func (c Config) ProbeDuration() (time.Duration, error) {
	if c.ProbeTimeout == "" {
		return 2 * time.Second, nil
	}
	d, err := time.ParseDuration(c.ProbeTimeout)
	if err != nil {
		// time.ParseDuration says `time: invalid duration "soon"`, which puts
		// a Go package name where this file has setting names and reads as
		// though "time" were a key of its own. What is missing is nearly
		// always the unit, so the answer is an example rather than a rule.
		return 0, fmt.Errorf("probe_timeout: %q is not a length of time — try 2s, 750ms or 1m", c.ProbeTimeout)
	}
	if d <= 0 {
		return 0, fmt.Errorf("probe_timeout: %q must be longer than zero", c.ProbeTimeout)
	}
	return d, nil
}

// Example is a documented starting point, written by -print-config. It takes
// the path so the output names the file the user should actually write, which
// differs by platform.
//
// The two lists people cannot guess — what the actions are called, and what
// the palette colours are called — are spliced in from the same places the
// errors about them read from, so the example cannot come to describe a
// program this one is not. It used to say "run omassh -print-config to see
// every action name", printed by omassh -print-config, which listed none: the
// one instruction in the file sent you to where you already were, for
// something that was not there.
func Example(path string) string {
	return "# " + path + "\n" + fmt.Sprintf(exampleBody,
		commentList(theme.PaletteKeys()), commentList(keymap.Names()))
}

// commentList sets a long list of names out as comment lines, wrapped so the
// file stays readable in a narrow editor.
func commentList(names []string) string {
	const width = 74
	var b strings.Builder
	line := "#  "
	for _, n := range names {
		if len(line)+1+len(n)+1 > width && line != "#  " {
			b.WriteString(line + "\n")
			line = "#  "
		}
		line += " " + n
	}
	b.WriteString(line)
	return b.String()
}

const exampleBody = `# Every setting is optional; delete anything you do not want to change.

# terminal draws in your terminal's own colours, so it suits whatever scheme
# the terminal has, light or dark. Also built in: gruvbox, mono, nord and
# tokyonight. Or name one defined below.
theme: terminal

# Define your own palette. A colour is "#rrggbb", a number for one of the
# terminal's own (0 black, 1 red, 2 green, 3 yellow, 4 blue, 5 magenta,
# 6 cyan, 7 white, and 8 to 15 the same again brighter, 8 being grey), or
# default: the terminal's text colour, or as selected_bg its own highlight.
# Omitted colours come from the terminal palette. The colours are:
%s
# themes:
#   mine:
#     accent: "#ff8800"
#     border: 8

# Rebind any action. Arrow keys and ctrl+c are reserved and always work.
# The actions are:
%s
# keys:
#   connect: o
#   search: f

# Passed to every ssh invocation, as ssh -o would.
# ssh_options:
#   - ConnectTimeout=10
#   - ServerAliveInterval=30

# How long a reachability probe waits before calling a host down.
probe_timeout: 2s
`
