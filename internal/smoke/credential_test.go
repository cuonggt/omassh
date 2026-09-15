package smoke

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A credential made in the interface reaches the connection it is for.
//
// This is the whole chain in one go: the shipped binary, a terminal, the
// keychain code path, tmux, sshx.Build, and a server that answers. Every link
// in it is covered by a test of its own, and the wiring between them was not —
// which is how a password credential shipped unable to reach the pane at all,
// because the environment it needs was set on a command tmux then replaced.
func TestACredentialMadeInTheInterfaceReachesTheConnection(t *testing.T) {
	if !tmuxAvailable() {
		t.Skip("no tmux; this drives the interface through one")
	}
	dir := t.TempDir()
	bin := build(t)
	srv := startServer(t, dir)

	// A key of our own, so the credential names something that exists.
	key := filepath.Join(dir, "id_test")
	if out, err := exec.Command("ssh-keygen", "-t", "ed25519", "-f", key, "-N", "", "-q").CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v\n%s", err, out)
	}
	host, port, _ := strings.Cut(srv.addr, ":")
	seed(t, bin, dir, fmt.Sprintf(
		"version: 1\nhosts:\n  - name: box\n    addr: %s\n    port: %s\n", host, port))

	p := start(t, bin, dir)

	// Make the credential.
	p.send("C")
	p.waitFor("no credentials yet")
	p.send("n")
	p.waitFor("New credential")
	p.send("Test key", "Tab", "Tab", "tester", "Tab", key, "Enter")
	p.waitFor("Test key")

	// security(1) prompts on the terminal when it can find one. If that ever
	// comes back, its prompt lands in the middle of the frame and the write
	// hangs — so the screen is the place to catch it.
	p.mustNotSay("password data for new item")

	// Point the host at it.
	p.send("Escape")
	p.waitFor("box")
	p.send("2", "e")
	p.waitFor("Edit box")
	p.send("Tab", "Tab", "Tab", "Down", "Down", "Enter")
	// The form says what the credential supplies before it is even saved.
	p.waitFor("from credential: tester")
	p.send("Enter")

	// Resolved, and said so.
	p.waitFor("tester@" + host)
	p.waitFor("← Test key")

	// And it actually connects, through tmux, with the credential's key.
	p.send("t")
	p.waitFor(connected)
}

// The same credential reaches a connection that nobody is watching.
//
// A pane has a terminal and a person in front of it; sftp has neither, and
// takes a different route through sshx. Both are worth walking because they
// are wired separately and have been wrong separately.
func TestACredentialReachesAnUnattendedConnection(t *testing.T) {
	if !tmuxAvailable() {
		t.Skip("no tmux; this drives the interface through one")
	}
	dir := t.TempDir()
	bin := build(t)
	srv := startServer(t, dir)

	key := filepath.Join(dir, "id_test")
	if out, err := exec.Command("ssh-keygen", "-t", "ed25519", "-f", key, "-N", "", "-q").CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v\n%s", err, out)
	}
	host, port, _ := strings.Cut(srv.addr, ":")
	seed(t, bin, dir, fmt.Sprintf(
		"version: 1\ncredentials:\n  - name: Test key\n    kind: key\n    user: tester\n    identity: %s\n"+
			"hosts:\n  - name: box\n    addr: %s\n    port: %s\n    credential: Test key\n", key, host, port))

	p := start(t, bin, dir)
	p.waitFor("box")

	// This server has no sftp subsystem, so the connection is expected to fail
	// at the subsystem — but only after authenticating. What is being tested
	// is that it got that far with the credential's user and key, which the
	// message names, rather than being refused before it started.
	p.send("2", "s")
	p.waitFor("sftp is not enabled on this host")
	// The old message, which named a channel nobody asked about.
	p.mustNotSay("subsystem request failed")
}
