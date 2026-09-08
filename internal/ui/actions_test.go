package ui

import (
	"net"
	"strconv"
	"testing"

	"github.com/cuonggt/omassh/internal/store"
)

// Enter means different things per panel. On Forwards it must start a tunnel,
// not open a session.
func TestEnterOnForwardsTogglesTheTunnel(t *testing.T) {
	// Hold a port so the pre-flight check refuses, which exercises the routing
	// without spawning ssh.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	port := l.Addr().(*net.TCPAddr).Port

	h := newHarness(t)
	host := h.addHost("db", "10.0.0.1")
	h.store.PutForward(store.Forward{
		HostKey: host.StatKey(), Name: "pg", Kind: store.ForwardLocal,
		ListenPort: port, TargetHost: "localhost", TargetPort: 5432,
	})
	h.reload()

	h.press("3", "enter")

	if h.m.mode != modeBrowse {
		t.Fatalf("enter on Forwards changed mode to %v", h.m.mode)
	}
	if !h.m.failed {
		t.Fatalf("expected the busy port to be reported, status was %q", h.m.status)
	}
	h.mustContain("already in use")
	h.mustContain(strconv.Itoa(port))
}

func TestSSHConfigHostsAreReadOnly(t *testing.T) {
	dir := t.TempDir()
	cfg := dir + "/config"
	if err := writeFile(cfg, "Host fromconfig\n  HostName 10.1.2.3\n"); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, func(o *Options) { o.SSHConfigPath = cfg })

	h.mustContain("ssh_config")
	h.mustContain("fromconfig")

	// Editing one must be refused rather than silently writing to the store.
	h.press("2", "e")
	if h.m.mode == modeForm {
		t.Fatal("an ssh_config host opened an edit form")
	}
	h.mustContain("read-only")
}

// A deleted host's session would otherwise keep running with nothing left in
// the interface that can reach it.
func TestDeletingAHostWarnsAboutItsSession(t *testing.T) {
	h := newHarness(t)
	host := h.addHost("doomed", "10.0.0.1")

	// Without a live session the prompt says only that history is kept.
	h.press("2", "d")
	h.mustContain("session history is kept")
	h.mustNotContain("running session is ended")
	h.press("n")

	// With one, the prompt says the session goes too.
	h.m.d.live = map[string]bool{sessionNameFor(host): true}
	h.press("d")
	h.mustContain("running session is ended")
	h.press("n")
}
