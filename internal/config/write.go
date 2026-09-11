package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/cuonggt/omassh/internal/safefile"
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
			// Whatever was written beside the setting is the reader's own note
			// about it — "gruvbox  # easiest on this monitor" — and rewriting
			// the line took it away, for a file otherwise left alone.
			lines[i] = line + trailingComment(l)
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

// trailingComment is the comment at the end of a line, with the whitespace
// that sets it off, or nothing.
//
// A # only opens one where it follows whitespace and sits outside quotes, so
// `theme: "a#b"` has no comment while `theme: nord  # my favourite` is all
// comment from the spaces on.
func trailingComment(s string) string {
	var inSingle, inDouble bool
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
		case '#':
			if inSingle || inDouble || i == 0 {
				continue
			}
			if s[i-1] != ' ' && s[i-1] != '\t' {
				continue
			}
			j := i
			for j > 0 && (s[j-1] == ' ' || s[j-1] == '\t') {
				j--
			}
			return s[j:]
		}
	}
	return ""
}

// replace writes the file through a rename, so an interrupted write leaves the
// old config in place rather than half of a new one.
func replace(path, body string) error {
	return safefile.Replace(path, []byte(body), 0o600)
}
