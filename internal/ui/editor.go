package ui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// scriptEditedMsg is what came back from $EDITOR.
type scriptEditedMsg struct {
	script string
	err    error
}

// editScript hands the terminal to $EDITOR on a copy of script, the way a
// session is handed to ssh.
//
// Every input on a form here is single-line, and a shell script is not. Rather
// than grow a text area — and with it a second set of opinions about tabs,
// indentation, and where the cursor goes when you press up — this gives the
// job to the editor already configured on the machine. It is the same argument
// that keeps an SSH implementation out of this program.
//
// The scratch file is written 0600 inside a directory of its own, and the
// directory goes when the editor exits. A snippet is not meant to hold a
// secret and the export says so, but a file that briefly holds whatever
// someone has typed does not need to be readable by everything on the machine
// in the meantime — and vim writes its swap file beside the one it is editing,
// which is why this is a directory rather than a lone temporary file.
func editScript(script string) tea.Cmd {
	name, args, err := editorCommand()
	if err != nil {
		return func() tea.Msg { return scriptEditedMsg{err: err} }
	}

	dir, err := os.MkdirTemp("", "omassh-snippet-")
	if err != nil {
		return func() tea.Msg { return scriptEditedMsg{err: err} }
	}
	// Named .sh so the editor recognises what it is and highlights it.
	path := filepath.Join(dir, "snippet.sh")
	if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
		os.RemoveAll(dir)
		return func() tea.Msg { return scriptEditedMsg{err: err} }
	}

	return tea.ExecProcess(exec.Command(name, append(args, path)...), func(runErr error) tea.Msg {
		defer os.RemoveAll(dir)
		// An editor that exited badly has said nothing about what the script
		// should be, so the one already on the form stands. vim's :cq is the
		// deliberate way to say that; a crash is the accidental one.
		if runErr != nil {
			return scriptEditedMsg{err: runErr}
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return scriptEditedMsg{err: err}
		}
		return scriptEditedMsg{script: string(b)}
	})
}

// editorCommand is what to run: $VISUAL, then $EDITOR, then vi.
//
// Split on spaces rather than handed to a shell. $EDITOR very often carries a
// flag — "code -w", "emacsclient -nw" — and a shell would mean quoting the
// path being passed to it as well. What this does not cover is an editor whose
// own path contains a space; $VISUAL pointing at a symlink is the way round
// that, and it is rarer than the flag.
func editorCommand() (string, []string, error) {
	spec := strings.TrimSpace(os.Getenv("VISUAL"))
	if spec == "" {
		spec = strings.TrimSpace(os.Getenv("EDITOR"))
	}
	if spec == "" {
		// What every other program falls back to, and the last thing that can
		// be tried before giving up.
		spec = "vi"
	}
	parts := strings.Fields(spec)
	if _, err := exec.LookPath(parts[0]); err != nil {
		return "", nil, fmt.Errorf("cannot run %s — set $EDITOR to an editor on this machine", parts[0])
	}
	return parts[0], parts[1:], nil
}
