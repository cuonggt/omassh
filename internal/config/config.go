// Package config loads Omassh's YAML configuration.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/cuonggt/omassh/internal/keymap"
	"github.com/cuonggt/omassh/internal/ui/theme"
)

// Config is the whole of ~/.config/omassh/config.yaml.
type Config struct {
	Theme        string                   `yaml:"theme"`
	Themes       map[string]theme.Palette `yaml:"themes"`
	Keys         map[string]string        `yaml:"keys"`
	SSHOptions   []string                 `yaml:"ssh_options"`
	Fanout       int                      `yaml:"fanout"`
	ProbeTimeout string                   `yaml:"probe_timeout"`
}

func Default() Config {
	return Config{Theme: "tokyonight", Fanout: 8, ProbeTimeout: "2s"}
}

func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "omassh", "config.yaml"), nil
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
	if err := yaml.Unmarshal(raw, &c); err != nil {
		return Default(), fmt.Errorf("%s: %w", path, err)
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
	if c.Fanout < 0 {
		return fmt.Errorf("fanout must not be negative")
	}
	return nil
}

// Palette resolves the configured theme name, preferring a palette defined in
// the config file over a built-in of the same name.
func (c Config) Palette() (theme.Palette, error) {
	name := c.Theme
	if name == "" {
		name = "tokyonight"
	}
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

func (c Config) Keymap() (keymap.Map, error) { return keymap.New(c.Keys) }

func (c Config) ProbeDuration() (time.Duration, error) {
	if c.ProbeTimeout == "" {
		return 2 * time.Second, nil
	}
	d, err := time.ParseDuration(c.ProbeTimeout)
	if err != nil {
		return 0, fmt.Errorf("probe_timeout: %w", err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("probe_timeout must be positive")
	}
	return d, nil
}

// Example is a documented starting point, written by -print-config.
const Example = `# ~/.config/omassh/config.yaml
# Every setting is optional; delete anything you do not want to change.

# Built in: tokyonight, gruvbox, nord, mono. Or name one defined below.
theme: tokyonight

# Define your own palette. Omitted colours fall back to the default.
# themes:
#   mine:
#     accent: "#ff8800"
#     border: "#444444"

# Rebind any action. Arrow keys and ctrl+c are reserved and always work.
# Run omassh -print-config to see every action name.
# keys:
#   connect: o
#   search: f

# Passed to every ssh invocation, as ssh -o would.
# ssh_options:
#   - ConnectTimeout=10
#   - ServerAliveInterval=30

# How many hosts a snippet fan-out talks to at once.
fanout: 8

# How long a reachability probe waits before calling a host down.
probe_timeout: 2s
`
