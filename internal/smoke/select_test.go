package smoke

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// mouse sends omassh a mouse event the way a terminal does: in SGR, the
// encoding Bubble Tea asks for, counting cells from one. button is SGR's —
// 0 the left button, 32 the left button held while the pointer moves.
func (p *pane) mouse(button, x, y int, release bool) {
	p.t.Helper()
	final := 'M'
	if release {
		final = 'm'
	}
	seq := fmt.Sprintf("\x1b[<%d;%d;%d%c", button, x+1, y+1, final)
	if out, err := exec.Command("tmux", "-L", driveSocket, "send-keys", "-t", "v", "-l", seq).CombinedOutput(); err != nil {
		p.t.Fatalf("sending a mouse event: %v\n%s", err, out)
	}
	// As far apart as a hand would send them; see handPace in internal/term.
	time.Sleep(120 * time.Millisecond)
}

// dragOver presses on the first cell of text where the screen shows it, and
// lets go just past its last.
func (p *pane) dragOver(text string) {
	p.t.Helper()
	for y, row := range strings.Split(p.waitFor(text), "\n") {
		if i := strings.Index(row, text); i >= 0 {
			x := ansi.StringWidth(row[:i])
			end := x + ansi.StringWidth(text)
			p.mouse(0, x, y, false)
			p.mouse(32, end, y, false)
			p.mouse(0, end, y, true)
			return
		}
	}
}

// connectToBox opens the smoke server's session in the pane, whose banner is
// then on screen to be selected.
func connectToBox(t *testing.T, env []string) (*pane, string) {
	t.Helper()
	if !tmuxAvailable() {
		t.Skip("no tmux; this drives the interface through one")
	}
	dir := t.TempDir()
	bin := build(t)
	srv := startServer(t, dir)
	host, port, _ := strings.Cut(srv.addr, ":")
	key := genKey(t, dir)
	seed(t, bin, dir, fmt.Sprintf(
		"version: 1\nhosts:\n  - name: box\n    addr: %s\n    port: %s\n    user: tester\n    identity: %s\n",
		host, port, key))

	p := startWith(t, bin, dir, env)
	p.waitFor("box")
	p.send("t")
	p.waitFor(connected)
	return p, dir
}

// A drag over the session puts what it covered on the clipboard, through the
// machine's own clipboard program.
//
// The whole of it in the shipped binary: the terminal's mouse report, the
// pane, tmux's selection, its OSC 52 coming back through the emulator, and a
// program run with the text — every hop of which a unit test stops short of.
// The program is the test's own, first on PATH under the names the real ones
// have, so the suite never touches the clipboard of whoever runs it.
func TestADragInTheSessionReachesTheClipboard(t *testing.T) {
	fake := filepath.Join(t.TempDir(), "bin")
	clip := filepath.Join(fake, "clipboard")
	if err := os.Mkdir(fake, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"pbcopy", "wl-copy"} {
		script := "#!/bin/sh\ncat > " + clip + "\n"
		if err := os.WriteFile(filepath.Join(fake, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// wl-copy is only tried with a display to copy to. Over ssh none is
	// tried at all, so a run started from an ssh session clears that too.
	p, _ := connectToBox(t, []string{
		"PATH=" + fake + ":$PATH", "WAYLAND_DISPLAY=smoke", "SSH_CONNECTION=", "SSH_TTY=",
	})

	p.dragOver(connected)
	p.waitFor(`copied "` + connected + `"`)
	if got, err := os.ReadFile(clip); err != nil || string(got) != connected {
		t.Errorf("the clipboard program was given %q (%v), want %q", got, err, connected)
	}
}

// Over ssh the clipboard is the terminal's to set, and OSC 52 asks it to. A
// tmux of the test's own stands in for the terminal: told to accept the
// sequence, it keeps what it was sent as a buffer, which is somewhere to read
// it back from.
func TestOverSSHADragAsksTheTerminalToCopy(t *testing.T) {
	p, _ := connectToBox(t, []string{"SSH_CONNECTION=smoke"})
	if out, err := run("tmux", "-L", driveSocket, "set-option", "-g", "set-clipboard", "on"); err != nil {
		t.Fatalf("set-clipboard: %v\n%s", err, out)
	}

	p.dragOver(connected)
	p.waitFor(`asked the terminal to copy "` + connected + `"`)
	deadline := time.Now().Add(waitTimeout)
	var got string
	for time.Now().Before(deadline) {
		out, err := exec.Command("tmux", "-L", driveSocket, "show-buffer").Output()
		if got = string(out); err == nil && got == connected {
			return
		}
		time.Sleep(150 * time.Millisecond)
	}
	t.Errorf("the terminal was sent %q, want %q", got, connected)
}
