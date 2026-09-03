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

// A destructive-looking command must not run on a single keystroke.
func TestDangerousSnippetDemandsTypedConfirmation(t *testing.T) {
	h := newHarness(t)
	h.addHost("web", "10.0.0.1")
	h.store.PutSnippet(store.Snippet{Name: "cleanup", Command: "rm -rf /tmp/somewhere"})
	h.reload()

	h.press("S")
	if h.m.mode != modeSnippets {
		t.Fatalf("S did not open snippets, got %v", h.m.mode)
	}
	h.mustContain("cleanup")

	h.press("enter")
	if h.m.mode != modeForm || h.m.form == nil || h.m.form.kind != formTypedRun {
		t.Fatalf("a destructive snippet ran without a typed confirmation (mode %v)", h.m.mode)
	}
	h.mustContain("looks destructive")
	h.mustContain("rm -rf")

	// The wrong number is refused and the form stays up.
	h.type_("9")
	h.press("enter")
	if h.m.mode != modeForm {
		t.Fatal("a wrong host count was accepted")
	}
	h.mustContain("exactly to confirm")
}

// A harmless snippet on a single host needs no confirmation, but a fan-out
// across a group always does.
func TestFanOutAlwaysConfirms(t *testing.T) {
	h := newHarness(t)
	g, _ := h.store.PutGroup(store.Group{Name: "Fleet"})
	for _, n := range []string{"a", "b", "c"} {
		h.store.PutHost(store.Host{Name: n, Addr: "10.0.0.1", GroupID: g.ID})
	}
	h.store.PutSnippet(store.Snippet{Name: "uptime", Command: "uptime"})
	h.reload()

	h.press("S", "f")
	if h.m.mode != modeConfirm {
		t.Fatalf("fan-out did not confirm, got mode %v", h.m.mode)
	}
	h.mustContain("on 3 hosts?")
	// The hosts are named, so the blast radius is visible before agreeing.
	for _, n := range []string{"a", "b", "c"} {
		h.mustContain(n)
	}
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
