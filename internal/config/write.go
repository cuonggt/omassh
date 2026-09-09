package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// SetTheme records a theme name in the config file, leaving the rest of it
// exactly as it was.
//
// The picker writes here rather than into the database so that a theme comes
// from one place: what you choose in the interface and what you write by hand
// are the same setting, and neither silently outranks the other. Only the one
// line is rewritten — comments, ordering and any custom palettes survive,
// which unmarshalling and re-marshalling the whole file would not promise.
func SetTheme(path, name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("no theme named")
	}
	line := "theme: " + name

	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return replace(path, "# omassh configuration."+
			" Run omassh -print-config for a documented example.\n"+line+"\n")
	}
	if err != nil {
		return err
	}

	lines := strings.Split(string(raw), "\n")
	for i, l := range lines {
		if themeKey.MatchString(l) {
			lines[i] = line
			return replace(path, strings.Join(lines, "\n"))
		}
	}
	// Nothing set one yet. The top is where -print-config puts it, and where
	// someone looking for it will look.
	return replace(path, line+"\n"+string(raw))
}

// themeKey matches the theme setting at the top level of the document. A key
// belonging to something else is indented under it, so anchoring at column
// zero is what stops this rewriting a custom palette that happens to contain
// the word.
var themeKey = regexp.MustCompile(`^theme[ \t]*:`)

// replace writes the file through a rename, so an interrupted write leaves the
// old config in place rather than half of a new one.
func replace(path, body string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".omassh-config-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())

	if _, err := tmp.WriteString(body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
