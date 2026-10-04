package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sshConfigAt writes an ssh config under a home of the test's own, so the
// detail pane can name it as ~/.ssh/config the way it does for everyone, and
// returns its path.
func sshConfigAt(t *testing.T, body string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".ssh", "config")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if body != "" {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

// A block your ssh config keeps under a host's name changes how the host is
// reached as surely as a group does, so the detail pane says when it is in use
// — and the command beneath it, which is the one that runs, names the host the
// way the block needs. The file is read on every load, like the store, so an
// edit reaches the next connection after an r rather than after a restart.
func TestTheDetailPaneSaysWhenYourSSHConfigsBlockIsInUse(t *testing.T) {
	path := sshConfigAt(t, "")
	h := newHarness(t, func(o *Options) { o.SSHConfig = path })
	h.addHost("prod-web", "10.0.0.5")
	h.selectHost("prod-web")

	h.mustNotContain("config  ")
	h.mustContain("ssh 10.0.0.5")

	if err := os.WriteFile(path, []byte("Host prod-web\n    HostName 10.0.0.5\n    ForwardAgent yes\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.press("r")

	h.mustContain("config  Host prod-web  ← ~/.ssh/config")
	h.mustContain("ssh -o HostName=10.0.0.5 prod-web")
}

// A block of the same name for another machine is passed over, and the pane
// says why rather than leave whoever wrote it wondering where their settings
// went — it is usually an address that moved in one place and not the other.
func TestTheDetailPaneSaysWhyABlockOfTheSameNameIsNotUsed(t *testing.T) {
	path := sshConfigAt(t, "Host web\n    HostName 10.0.0.5\n    ForwardAgent yes\n")
	h := newHarness(t, func(o *Options) { o.SSHConfig = path })
	h.addHost("web", "10.0.0.9")
	h.selectHost("web")

	h.mustContain("config  not used — ~/.ssh/config puts web at 10.0.0.5")
	h.mustContain("ssh 10.0.0.9")
	h.mustNotContain("HostName=")
}

// A config that cannot be read costs the names and nothing else: the list is
// all there, every host is reached by its address as before, and the status
// says which file and why, since a block that has quietly stopped applying is
// the very thing reading the file is for.
func TestAnSSHConfigThatCannotBeReadCostsOnlyTheNames(t *testing.T) {
	path := sshConfigAt(t, "# >>> omassh >>>\nHost prod-web\n    HostName 10.0.0.5\n")
	h := newHarness(t, func(o *Options) { o.SSHConfig = path })
	h.addHost("prod-web", "10.0.0.5")
	h.selectHost("prod-web")

	if !h.m.failed || !strings.HasPrefix(h.m.status, "~/.ssh/config: ") || !strings.Contains(h.m.status, "reached by their address") {
		t.Errorf("status = %q, want the file named, what is wrong with it, and what that means", h.m.status)
	}
	h.mustContain("ssh 10.0.0.5")
	h.mustNotContain("config  ")
}
