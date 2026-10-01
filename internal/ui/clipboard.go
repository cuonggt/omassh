package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// A selection in the session pane goes to the clipboard of whoever is at the
// keyboard, and there are two ways of reaching it.
//
// This machine's own clipboard program comes first — pbcopy, wl-copy, xclip,
// xsel — because when one is there it is certain: it either took the text or
// said it did not. The other way is OSC 52, which asks the terminal to do the
// copying, and a terminal is free to ignore that or to ask first, with nothing
// coming back to say which. So it is what is left when no program will do, and
// the status says it asked rather than that it copied.

// errNoClipboard is a machine with no clipboard program that took the text.
var errNoClipboard = errors.New("no clipboard program on this machine")

// clipboardTimeout bounds a clipboard program. They answer at once or not at
// all: xclip with no display left to talk to waits for one.
const clipboardTimeout = 2 * time.Second

// clipboardMsg says how a copy went.
type clipboardMsg struct {
	text string
	// err is set when nothing on this machine took the text, which leaves
	// the terminal to be asked.
	err error
}

// copyText puts text on the clipboard. Off the event loop, since a clipboard
// program is a process launch, and one that waits should not take the
// interface with it.
func (m Model) copyText(text string) tea.Cmd {
	write := m.opts.Clipboard
	if write == nil {
		write = systemClipboard
	}
	return func() tea.Msg {
		return clipboardMsg{text: text, err: write(text)}
	}
}

// handleCopied says what was copied, and asks the terminal for it when nothing
// on the machine would take it.
func (m Model) handleCopied(msg clipboardMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.setStatus("asked the terminal to copy " + describeCopy(msg.text))
		return m, tea.SetClipboard(msg.text)
	}
	m.setStatus("copied " + describeCopy(msg.text))
	return m, nil
}

// describeCopy names what was copied: the text itself when it is a line, cut
// short so the status keeps room for anything else, and a count when it is
// more. A copy cannot be seen on the clipboard, and the one thing worth
// checking is that it holds what was meant.
func describeCopy(text string) string {
	text = strings.TrimSuffix(text, "\n")
	if n := strings.Count(text, "\n") + 1; n > 1 {
		return fmt.Sprintf("%d lines", n)
	}
	return `"` + ansi.Truncate(text, 40, "…") + `"`
}

// systemClipboard gives text to this machine's clipboard program.
//
// Over ssh this machine's clipboard is not the one at the keyboard — pbcopy on
// a Mac reached from somewhere else fills the Mac's — so none is tried there,
// and the terminal is asked instead.
func systemClipboard(text string) error {
	if os.Getenv("SSH_CONNECTION") != "" || os.Getenv("SSH_TTY") != "" {
		return errNoClipboard
	}
	for _, argv := range clipboardPrograms(runtime.GOOS, os.Getenv) {
		path, err := exec.LookPath(argv[0])
		if err != nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), clipboardTimeout)
		cmd := exec.CommandContext(ctx, path, argv[1:]...)
		cmd.Stdin = strings.NewReader(text)
		err = cmd.Run()
		cancel()
		if err == nil {
			return nil
		}
	}
	return errNoClipboard
}

// clipboardPrograms are what would put text on this machine's clipboard, in
// the order to try them. On Linux that depends on the display server, and
// none of them can do anything without one.
func clipboardPrograms(goos string, getenv func(string) string) [][]string {
	if goos == "darwin" {
		return [][]string{{"pbcopy"}}
	}
	var out [][]string
	if getenv("WAYLAND_DISPLAY") != "" {
		out = append(out, []string{"wl-copy"})
	}
	if getenv("DISPLAY") != "" {
		out = append(out,
			[]string{"xclip", "-selection", "clipboard"},
			[]string{"xsel", "--clipboard", "--input"})
	}
	return out
}
