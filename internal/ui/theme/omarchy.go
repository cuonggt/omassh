package theme

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Omassh can be asked for Omarchy's own current theme rather than for a
// palette of its own, and then it looks like the rest of the desktop and
// changes when the desktop does.
//
// This is a reader and not a translation: the roles here are already the ones
// colors.toml names, because they were taken from it. The file is a flat block
// of key = "value" lines, so it needs no TOML parser and no new dependency —
// Omassh has one dependency for colour and none for configuration, and ten
// lines of parsing is not the place to change that.

// OmarchyName is the theme that follows whatever Omarchy is set to.
const OmarchyName = "omarchy"

// OmarchyFile is where Omarchy keeps the current theme's colours. main sets it
// at startup and the tests leave it empty, so the suite never reads the
// colours of whoever is running it — the same bargain Options.SSHConfig makes
// with ~/.ssh/config. Empty is also the truth on a machine without Omarchy:
// there is nothing to read.
var OmarchyFile string

// OmarchyPath is the file Omarchy writes the current theme's colours to.
//
// current/theme is a symlink Omarchy repoints as the theme changes, so the
// path is fixed and what it resolves to is not — which is the whole reason
// this works. Spelled out of $HOME rather than $XDG_STATE_HOME because that is
// where Omarchy itself writes it: honouring a variable Omarchy ignores would
// mean looking somewhere nothing is ever put.
func OmarchyPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "state", "omarchy", "current", "theme", "colors.toml")
}

// OmarchyStamp is a value that changes when Omarchy's current theme does, so
// that following it costs a couple of stats rather than a reread and a
// repaint. Empty where there is no Omarchy to follow.
//
// Two things go into it. Omarchy writes a token to theme.generation every time
// a theme is set, and reads it back itself to tell whether a switch it is
// partway through has been superseded — it is the one file in the directory
// whose whole job is to say the theme changed. And the colours file's own size
// and time, because current/theme is a symlink: a switch repoints it at
// another directory, so the same path stats as a different file. The second
// covers an Omarchy too old to write the token, and a colors.toml edited by
// hand, which changes nothing else.
//
// Omarchy writes the token before it repoints the symlink, so a read can land
// between the two and see the new token beside the old colours. Nothing has to
// be done about that: the repoint changes the stamp again, and whoever is
// watching reads the file a second time and lands on the right palette.
func OmarchyStamp() string {
	if strings.TrimSpace(OmarchyFile) == "" {
		return ""
	}
	token, _ := os.ReadFile(omarchyGenerationPath(OmarchyFile))

	var colours string
	if fi, err := os.Stat(OmarchyFile); err == nil {
		colours = fmt.Sprintf("%d:%d", fi.ModTime().UnixNano(), fi.Size())
	}
	return strings.TrimSpace(string(token)) + "\x00" + colours
}

// omarchyGenerationPath is where Omarchy writes the token, given where it
// writes the colours: current/theme/colors.toml, so the token is two
// directories up, beside the symlink rather than inside the theme it points
// at — which is what lets it outlive the theme being switched away from.
func omarchyGenerationPath(colours string) string {
	return filepath.Join(filepath.Dir(filepath.Dir(colours)), "theme.generation")
}

// OmarchyAvailable reports whether Omarchy's current theme can be read here,
// which is what decides whether the picker offers it. A machine without
// Omarchy is not offered a theme that cannot be drawn on it.
func OmarchyAvailable() bool {
	_, err := ReadOmarchy(OmarchyFile)
	return err == nil
}

// ReadOmarchy is the palette Omarchy's current theme describes.
func ReadOmarchy(path string) (Palette, error) {
	// The advice is about the config file and not about the theme picker,
	// because this is only ever read at startup: the picker does not offer
	// omarchy where it cannot be read, so by the time anyone could press a
	// key, this has already stopped Omassh from starting.
	const fix = "set theme: to a built-in, or leave it out for your terminal's own colours"
	if strings.TrimSpace(path) == "" {
		return Palette{}, fmt.Errorf("theme %s follows the theme Omarchy is set to, and there is no Omarchy on this machine — %s", OmarchyName, fix)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Palette{}, fmt.Errorf("theme %s follows the theme Omarchy is set to, and %s is not there — %s", OmarchyName, path, fix)
		}
		return Palette{}, fmt.Errorf("theme %s: %s could not be read: %w", OmarchyName, path, err)
	}
	return omarchyPalette(omarchyKeys(raw)), nil
}

// omarchyKeys reads the key = "value" lines of a colors.toml.
//
// The rules are the ones omarchy-theme-color reads the same file with, so a
// file Omarchy accepts is one this accepts: the key loses its quotes and
// spaces, a value in quotes is what lies between the first pair of them —
// which is how a trailing comment is dropped — and an unquoted one is
// trimmed. A key with anything but letters, digits, dash or underscore in it
// is skipped rather than guessed at.
func omarchyKeys(raw []byte) map[string]string {
	kv := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.Map(func(r rune) rune {
			if r == '"' || r == '\'' || r == ' ' || r == '\t' {
				return -1
			}
			return r
		}, key)
		if key == "" || strings.HasPrefix(key, "#") || !omarchyKey(key) {
			continue
		}
		if i := strings.IndexAny(value, `"'`); i >= 0 {
			value = value[i+1:]
			if j := strings.IndexAny(value, `"'`); j >= 0 {
				value = value[:j]
			}
		}
		kv[key] = strings.TrimSpace(value)
	}
	return kv
}

func omarchyKey(s string) bool {
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

// omarchyPalette resolves the ten roles Omassh draws with out of whatever the
// file happened to define.
//
// The fallbacks are omarchy-theme-color's, in its order, so a theme that omits
// a role is drawn here the way Waybar and the terminal draw it rather than the
// way Omassh would otherwise have guessed. Omarchy carries them because a
// theme may be written in the old ANSI names alone, or in the short ones —
// fg, dark_bg — and every consumer is meant to land on the same palette.
//
// Only the roles Omassh has are resolved. Omarchy mixes shades for several it
// does not, and a role still empty at the end of this is left empty on
// purpose: Apply fills it from the terminal palette, which is a better answer
// than a colour computed here for a role nothing draws with.
func omarchyPalette(kv map[string]string) Palette {
	// A value Omassh cannot draw counts as one the file did not set, so the
	// cascade carries on past it. colors.toml holds gradients and rgba() for
	// consumers that can use them, and a theme is not wrong for having them.
	hex := func(names ...string) string {
		for _, n := range names {
			if v, ok := kv[n]; ok && validHex(v) {
				return v
			}
		}
		return ""
	}

	fg := hex("foreground", "fg", "color7")
	darkFg := strOr(hex("dark_foreground", "dark_fg", "color8"), fg)
	brightFg := strOr(hex("bright_foreground", "bright_fg", "color15"), fg)

	return Palette{
		Accent:     hex("accent"),
		Selection:  hex("selection", "selection_background", "color8", "color0", "background"),
		Muted:      strOr(hex("muted", "color8"), darkFg),
		Foreground: fg,
		DarkFg:     darkFg,
		BrightFg:   brightFg,
		Red:        hex("red", "color1"),
		Yellow:     hex("yellow", "color3"),
		Green:      hex("green", "color2"),
		Magenta:    hex("magenta", "color5", "purple"),
	}
}

func strOr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
