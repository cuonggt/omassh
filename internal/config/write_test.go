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
