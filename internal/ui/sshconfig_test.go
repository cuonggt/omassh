package ui

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cuonggt/omassh/internal/store"
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

// A host your ssh config sends through a bastion is behind one as surely as a
// host whose group names one, so the via line says what it goes through and
// where that was said — for a host the config names, and for one reached by
// its address that a pattern in the config matches.
func TestTheDetailPaneSaysWhatYourSSHConfigSendsAHostThrough(t *testing.T) {
	path := sshConfigAt(t, `Host prod-web
    HostName 10.0.0.5
    ProxyJump bastion

Host 10.0.1.*
    ProxyCommand ssh -W %h:%p bastion
`)
	h := newHarness(t, func(o *Options) { o.SSHConfig = path })
	h.addHost("prod-web", "10.0.0.5")
	h.addHost("db", "10.0.1.7")

	h.selectHost("prod-web")
	h.mustContain("via     bastion  ← ~/.ssh/config")

	h.selectHost("db")
	h.mustContain("via     ssh -W %h:%p bastion  ← ~/.ssh/config")
}

// The probe dials an address from here, and a host your ssh config sends
// through a bastion has an address that means something only from the far
// side of it. So it is passed over, as a host behind omassh's own jump host
// is, rather than reported on: whatever answers at that address here is
// another machine. This test's own listener stands in for that machine, and
// was reported up — beside a host the config does not send anywhere, which
// is still dialled.
func TestAProbePassesOverAHostYourSSHConfigSendsThroughABastion(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	port := l.Addr().(*net.TCPAddr).Port

	path := sshConfigAt(t, fmt.Sprintf("Host prod-web\n    HostName 127.0.0.1\n    Port %d\n    ProxyJump bastion\n", port))
	h := newHarness(t, func(o *Options) { o.SSHConfig = path })
	for _, name := range []string{"prod-web", "local"} {
		if _, err := h.store.PutHost(store.Host{Name: name, Addr: "127.0.0.1", Port: port}); err != nil {
			t.Fatal(err)
		}
	}
	h.reload()

	h.press("2", "p")
	for done := false; !done; {
		select {
		case ev := <-h.m.probeCh:
			h.send(ev)
			done = ev.done
		case <-time.After(10 * time.Second):
			t.Fatal("the sweep never finished")
		}
	}

	h.mustContain("◌ prod-web")
	h.mustContain("● local")
	h.mustContain("1 up · 1 skipped")
}
