package ui

import (
	"testing"
)

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
