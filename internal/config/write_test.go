package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSettingTheThemeLeavesTheRestOfTheFileAlone(t *testing.T) {
	before := `# my omassh config, hand written

theme: tokyonight

# a palette of my own
themes:
  mine:
    accent: "#ff8800"

keys:
  connect: o

probe_timeout: 5s
`
	p := writeFile(t, before)
	if err := SetTheme(p, "nord"); err != nil {
		t.Fatal(err)
	}
	after := read(t, p)

	if !strings.Contains(after, "theme: nord") {
		t.Errorf("the theme was not set:\n%s", after)
	}
	// Everything a marshal-and-rewrite would have thrown away.
	for _, keep := range []string{
		"# my omassh config, hand written",
		"# a palette of my own",
		`accent: "#ff8800"`,
		"connect: o",
		"probe_timeout: 5s",
	} {
		if !strings.Contains(after, keep) {
			t.Errorf("rewriting dropped %q:\n%s", keep, after)
		}
	}
	// And it still parses, with the rest of the settings intact.
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("the file it wrote does not load: %v", err)
	}
	if cfg.ThemeName() != "nord" || cfg.Keys["connect"] != "o" || cfg.ProbeTimeout != "5s" {
		t.Errorf("settings did not survive: %+v", cfg)
	}
}

func TestOnlyATopLevelThemeIsRewritten(t *testing.T) {
	// Editing by line is what preserves the file, so the one risk is matching
	// the word somewhere it does not mean the setting.
	p := writeFile(t, `# theme: gruvbox is what I used to use
themes:
  theme:
    accent: "#ff8800"
theme: tokyonight
`)
	if err := SetTheme(p, "mono"); err != nil {
		t.Fatal(err)
	}
	after := read(t, p)

	if !strings.Contains(after, "# theme: gruvbox is what I used to use") {
		t.Errorf("rewrote a comment:\n%s", after)
	}
	if !strings.Contains(after, "  theme:\n") {
		t.Errorf("rewrote a nested key:\n%s", after)
	}
	if strings.Count(after, "theme: mono") != 1 {
		t.Errorf("want exactly one theme: mono\n%s", after)
	}
}

func TestSettingTheThemeWithNoFileYet(t *testing.T) {
	p := filepath.Join(t.TempDir(), "nested", "config.yaml")
	if err := SetTheme(p, "gruvbox"); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ThemeName() != "gruvbox" {
		t.Errorf("theme = %q, want gruvbox", cfg.ThemeName())
	}
	// The file lists nothing secret, but it is the user's, not the world's.
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", fi.Mode().Perm())
	}
}

func TestSettingTheThemeAddsTheKeyWhenTheFileOmitsIt(t *testing.T) {
	p := writeFile(t, "probe_timeout: 5s\n")
	if err := SetTheme(p, "nord"); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ThemeName() != "nord" || cfg.ProbeTimeout != "5s" {
		t.Errorf("got %+v", cfg)
	}
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// A key that is not a setting is a mistake worth reporting. Skipping past it
// is indistinguishable from the file not being read, and the settings most
// often mistyped are the ones where that shows least.
func TestAnUnknownSettingIsReported(t *testing.T) {
	for name, body := range map[string]string{
		"a misspelt setting":     "them: nord\n",
		"a plural dropped":       "ssh_option:\n  - ConnectTimeout=10\n",
		"a hyphen for an unders": "probe-timeout: 5s\n",
		"keys without its s":     "key:\n  connect: o\n",
		"a colour field typo":    "themes:\n  mine:\n    accnt: \"#ff8800\"\n",
		"the wrong colour name":  "themes:\n  mine:\n    selected: \"#333333\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			p := writeFile(t, body)
			cfg, err := Load(p)
			if err == nil {
				t.Fatalf("accepted silently, leaving %+v", cfg)
			}
			if !strings.Contains(err.Error(), p) {
				t.Errorf("the error does not name the file: %v", err)
			}
		})
	}
}

// A config that sets nothing is a config, and is what a fresh one looks like.
func TestAFileThatSetsNothingIsFine(t *testing.T) {
	for name, body := range map[string]string{
		"empty":         "",
		"only comments": "# nothing set here\n# nor here\n",
		"only blank":    "\n\n",
	} {
		t.Run(name, func(t *testing.T) {
			cfg, err := Load(writeFile(t, body))
			if err != nil {
				t.Fatalf("rejected a file that sets nothing: %v", err)
			}
			if cfg.ThemeName() != "tokyonight" || cfg.ProbeTimeout != "2s" {
				t.Errorf("the defaults did not survive: %+v", cfg)
			}
		})
	}
}

// Every real setting still loads, so the check cannot be passing by refusing
// everything.
func TestEverySettingStillLoads(t *testing.T) {
	p := writeFile(t, `theme: mine
themes:
  mine:
    text: "#c0caf5"
    text_dim: "#565f89"
    text_bright: "#ffffff"
    accent: "#ff8800"
    green: "#9ece6a"
    yellow: "#e0af68"
    red: "#f7768e"
    magenta: "#bb9af7"
    border: "#3b4261"
    selected_bg: "#283457"
keys:
  connect: o
ssh_options:
  - ConnectTimeout=10
probe_timeout: 5s
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatalf("a config using every setting was rejected: %v", err)
	}
	if cfg.ThemeName() != "mine" || cfg.Keys["connect"] != "o" ||
		len(cfg.SSHOptions) != 1 || cfg.ProbeTimeout != "5s" {
		t.Errorf("settings did not survive: %+v", cfg)
	}
	if _, err := cfg.Palette(); err != nil {
		t.Errorf("the palette does not resolve: %v", err)
	}
}
