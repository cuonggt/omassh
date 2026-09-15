package smoke

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ran is what the snippet prints, and what the screen is searched for to know
// the script reached a real host rather than merely being saved.
const ran = "SMOKE-SNIPPET-RAN"

// A snippet made in the interface runs on a real host, and what it said comes
// back to the screen.
//
// The whole chain in one go: the shipped binary, a terminal, the form, the
// store, sshx.Build, ssh, a server that runs what it is sent, and the result
// rendered back. Each link has a test of its own; this is the wiring between
// them, which is where what has shipped broken has broken.
func TestASnippetMadeInTheInterfaceRunsOnAHost(t *testing.T) {
	if !tmuxAvailable() {
		t.Skip("no tmux; this drives the interface through one")
	}
	dir := t.TempDir()
	bin := build(t)
	srv := startServer(t, dir)
	host, port, _ := strings.Cut(srv.addr, ":")
	seed(t, bin, dir, fmt.Sprintf(
		"version: 1\nhosts:\n  - name: box\n    addr: %s\n    port: %s\n    user: tester\n",
		host, port))

	p := start(t, bin, dir)
	p.waitFor("box")

	// Make it.
	p.send("S")
	p.waitFor("no snippets yet")
	p.send("n")
	p.waitFor("New snippet")
	p.send("say hello", "Tab", "echo "+ran, "Enter")
	p.waitFor("say hello")

	// Where, and the script whole, before anything runs.
	p.send("Enter")
	p.waitFor(`Run "say hello"`)
	p.waitFor("this host")
	p.waitFor("echo " + ran)
	p.waitFor("no terminal there")

	// Run it. One host needs no table, so what it said is what comes up.
	p.send("y")
	p.waitFor("say hello on box")
	if s := p.screen(); !strings.Contains(s, ran) {
		t.Errorf("the run said nothing the far end printed:\n%s", s)
	}
}

// A script exiting non-zero is an ordinary result carrying its status, not a
// failure in omassh — and what it wrote to stderr is what says why.
func TestASnippetThatFailsShowsItsStatusAndItsStderr(t *testing.T) {
	if !tmuxAvailable() {
		t.Skip("no tmux; this drives the interface through one")
	}
	dir := t.TempDir()
	bin := build(t)
	srv := startServer(t, dir)
	host, port, _ := strings.Cut(srv.addr, ":")
	seed(t, bin, dir, fmt.Sprintf(
		"version: 1\nhosts:\n  - name: box\n    addr: %s\n    port: %s\n    user: tester\n"+
			"snippets:\n  - name: goes wrong\n    script: echo %s >&2; exit 3\n",
		host, port, ran))

	p := start(t, bin, dir)
	p.waitFor("box")
	p.send("S")
	p.waitFor("goes wrong")
	p.send("Enter")
	p.waitFor(`Run "goes wrong"`)
	p.send("y")

	p.waitFor("exit 3")
	if s := p.screen(); !strings.Contains(s, ran) {
		t.Errorf("stderr never reached the screen, which is where the reason is:\n%s", s)
	}
}

// ctrl+e hands the terminal to $EDITOR and takes it back.
//
// tea.Exec releases the terminal to another program and restores it after,
// which is the same mechanism a full-screen session runs on and the same one
// no unit test can see: Update returns a command, and nothing here runs it.
// A form that could not get the terminal back, or an editor whose work never
// arrived, would both look like a saved snippet until someone ran it.
func TestCtrlEEditsASnippetInTheEditorAndComesBack(t *testing.T) {
	if !tmuxAvailable() {
		t.Skip("no tmux; this drives the interface through one")
	}
	dir := t.TempDir()
	bin := build(t)

	// An editor that appends a line, so what comes back is visibly not what
	// went out.
	editor := filepath.Join(dir, "fake-editor")
	const script = "#!/bin/sh\necho 'systemctl restart nginx' >> \"$1\"\n"
	if err := os.WriteFile(editor, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	p := startWith(t, bin, dir, []string{"VISUAL=", "EDITOR=" + editor})
	p.send("S")
	p.waitFor("no snippets yet")
	p.send("n")
	p.waitFor("New snippet")
	p.send("rotate certs", "Tab", "set -e")
	p.waitFor("ctrl+e opens $EDITOR")

	p.send("C-e")
	// Back from the editor with a line more than it had, and the field now
	// says so rather than holding a script it cannot show.
	p.waitFor("2 lines — ctrl+e to edit")

	p.send("Enter")
	p.waitFor("rotate certs")
	p.waitFor("2 lines")
}
