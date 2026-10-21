package ui

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// keyPathField is the field a key can be made for. Both a host and a
// credential name one, and nothing else on any form does.
const keyPathField = "Identity"

// keyMadeMsg is what came back from ssh-keygen.
type keyMadeMsg struct {
	path string
	err  error
}

// makeKey hands the terminal to ssh-keygen, the way a snippet's script is
// handed to $EDITOR and a session to ssh.
//
// Handing it over is the point and not a convenience. ssh-keygen takes a
// passphrase interactively or in its argument list; an argument list is
// readable by everything running on the machine, and omassh holds no secrets
// of its own — a host names a key path, and the passphrase is ssh-agent's
// business. So the only honest way to offer this is to put the person in front
// of the real program: the passphrase, its confirmation, and whether to
// overwrite a key already there are all ssh-keygen's to ask, and none of them
// is ever anything omassh sees. The Mac app built on this same core cannot
// give its terminal away, and makes keys without a passphrase for exactly this
// reason.
//
// -t ed25519 is what ssh-keygen itself recommends and what anything short of a
// decade-old server takes. -f is here because omassh has to know where the key
// landed to fill the field in with it; everything else is left to be asked.
func makeKey(path string) tea.Cmd {
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		return func() tea.Msg {
			return keyMadeMsg{err: errors.New("no ssh-keygen on this machine to make a key with")}
		}
	}
	// The directory has to be there first. ssh-keygen makes ~/.ssh only when
	// it prompts for the path itself, which is the one thing not left to it
	// here: handed a -f whose directory is missing it says `Saving key "…"
	// failed: No such file or directory` and stops. 0700 is what ssh wants of
	// that directory anyway, and refuses to use a key from one laxer.
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return func() tea.Msg { return keyMadeMsg{err: err} }
	}
	cmd := exec.Command("ssh-keygen", "-t", "ed25519", "-f", path)
	return tea.ExecProcess(cmd, func(runErr error) tea.Msg {
		// Cancelled, refused, or told not to overwrite: ssh-keygen exits
		// non-zero for all of them, having said on the terminal which it was.
		// There is no new key to point the form at in any of those cases.
		if runErr != nil {
			return keyMadeMsg{err: runErr}
		}
		if _, err := os.Stat(path); err != nil {
			return keyMadeMsg{err: errors.New("ssh-keygen left no key behind")}
		}
		return keyMadeMsg{path: path}
	})
}

// keyPathFor is where a key made from this form would go.
//
// What is already in the field wins: someone who typed a path meant it.
// Otherwise it goes beside the keys already there, named after the record the
// form is for — which is how keys are named in practice, and what makes one
// recognisable a year later where id_ed25519_2 does not.
//
// Nothing where there is no name yet, or nowhere to put it. Both are better
// said than guessed at: a key called `-` or dropped in the working directory
// is worse than a sentence asking for a name.
func (m Model) keyPathFor(f *form) (string, error) {
	if v := strings.TrimSpace(f.value(keyPathField)); v != "" {
		return expandTilde(v), nil
	}
	if m.opts.KeyDir == "" {
		return "", errors.New("there is no ~/.ssh here to keep a key in")
	}
	name := keyFileName(f.value("Name"))
	if name == "" {
		return "", errors.New("give this a name first — the key is named after it")
	}
	return filepath.Join(m.opts.KeyDir, name), nil
}

// keyFileName turns a record's name into a filename, keeping what a name can
// carry and dropping what a path cannot. A name that leaves nothing behind —
// "///" — comes back empty, and the caller asks for a better one.
func keyFileName(s string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '_', r == '.':
			b.WriteRune(r)
		case r == ' ':
			b.WriteRune('-')
		}
	}
	// Nothing leading or trailing that would hide the file or read as an
	// extension on its own.
	return strings.Trim(b.String(), ".-")
}

// expandTilde turns a ~ path into one a program can open.
//
// ssh expands ~ in an -i argument itself, which is why a host's key is written
// that way everywhere here. ssh-keygen does not do the same for -f: handed
// `~/x` it says `Saving key "~/x" failed: No such file or directory`, having
// tried to make a directory called ~ in whatever it was run from.
func expandTilde(p string) string {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	return filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(p, "~"), "/"))
}

// handleKeyMade takes back what ssh-keygen did.
func (m Model) handleKeyMade(msg keyMadeMsg) (tea.Model, tea.Cmd) {
	if m.form == nil {
		return m, nil
	}
	if msg.err != nil {
		m.form.problem = bareKeygenErr(msg.err)
		return m, m.form.focusCurrent()
	}
	// Offered from here on without a reload: the picker's list was built when
	// the form opened, and the key did not exist then.
	m.form.setField(keyPathField, tildePath(msg.path), m.identityChoices())
	m.form.problem = ""
	m.setStatus("made " + tildePath(msg.path))
	return m, m.form.focusCurrent()
}

// bareKeygenErr says what went wrong without the exit status of a program the
// person watching has just been looking at anyway — it said what it thought
// on the terminal before handing it back.
func bareKeygenErr(err error) string {
	if s := err.Error(); !strings.Contains(s, "exit status") {
		return s
	}
	return "no key was made — ssh-keygen said why before it exited"
}
