package smoke

import (
	"os"
	"path/filepath"
	"testing"
)

// Making a key hands the terminal to ssh-keygen, and that is the part no unit
// test can reach: Update returns a command, and nothing in a unit test runs
// it. So the only place the trip through tea.Exec is exercised at all is here
// — the shipped binary, a real terminal, and the real ssh-keygen answering on
// it.
//
// $HOME is the test's own, so the key lands in its directory and not in the
// ~/.ssh of whoever is running the suite. That directory does not exist when
// this starts, which is deliberate: ssh-keygen will not make the parent of a
// -f it was given, and a machine that has never used ssh has no ~/.ssh.
func TestMakingAKeyHandsTheTerminalToSSHKeygen(t *testing.T) {
	if !tmuxAvailable() {
		t.Skip("no tmux; this drives the interface through one")
	}
	dir := t.TempDir()
	bin := build(t)
	p := startWith(t, bin, dir, []string{"HOME=" + dir})

	p.send("2", "n")
	p.waitFor("New host")
	// Name, then along to the key path: the hint says which field this is.
	p.send("web", "Tab", "10.0.0.2", "Tab", "Tab", "Tab", "Tab")
	p.waitFor("ssh-keygen")

	p.send("C-g")
	// ssh-keygen has the terminal now, and asks for a passphrase itself —
	// which is the whole reason for handing it over rather than passing one
	// in an argument list everything on the machine can read.
	p.waitFor("passphrase")
	p.send("Enter", "Enter")

	// Back in the form, with the key it made named in the field.
	p.waitFor("~/.ssh/web")

	for _, f := range []string{"web", "web.pub"} {
		if _, err := os.Stat(filepath.Join(dir, ".ssh", f)); err != nil {
			t.Errorf("no %s where the form said the key was: %v", f, err)
		}
	}
}
