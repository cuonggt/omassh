package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	if c.Theme != "tokyonight" || c.Fanout != 8 {
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
	if c.Fanout != 8 {
		t.Errorf("Fanout = %d, want the default 8", c.Fanout)
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
fanout: 3
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
	if c.Fanout != 3 {
		t.Errorf("Fanout = %d", c.Fanout)
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
		"malformed yaml":  "theme: [unclosed\n",
		"unknown theme":   "theme: neon-dreams\n",
		"bad colour":      "theme: mine\nthemes:\n  mine:\n    accent: \"not-a-colour\"\n",
		"unknown action":  "keys:\n  teleport: t\n",
		"reserved key":    "keys:\n  connect: ctrl+c\n",
		"key conflict":    "keys:\n  connect: e\n",
		"bad duration":    "probe_timeout: soon\n",
		"negative fanout": "fanout: -1\n",
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

func TestExampleIsValid(t *testing.T) {
	c, err := Load(write(t, Example("/tmp/omassh/config.yaml")))
	if err != nil {
		t.Fatalf("the shipped example does not load: %v", err)
	}
	if err := c.Validate(); err != nil {
		t.Errorf("the shipped example does not validate: %v", err)
	}
}
