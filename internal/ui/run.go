package ui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/cuonggt/omassh/internal/sshx"
	"github.com/cuonggt/omassh/internal/store"
	"github.com/cuonggt/omassh/internal/ui/theme"
)

// snippetRun is a snippet being run on a host: what it is, where, and how far
// it has got.
//
// It starts stopped. Pressing enter on the list opens this screen showing the
// script and the host, and nothing runs until y — because the list shows a
// long snippet by its size, so enter alone would be running something the
// person pressing it cannot see, on a machine they have to be right about.
type snippetRun struct {
	snippet store.Snippet
	host    store.Host
	// token tells this run's result from one belonging to a run already
	// closed, whose ssh is still on its way back.
	token   int
	started bool
	// stopping is whether a stop has already been asked for. A second one
	// leaves without waiting: cancelling kills ssh, but Wait does not return
	// until the output pipes close, and a background process the script
	// started holds them open long after ssh itself is gone.
	stopping bool
	result   *sshx.Result
	cancel   context.CancelFunc
	scroll   int
}

// snippetDoneMsg is what a run came to.
type snippetDoneMsg struct {
	token  int
	result sshx.Result
}

func (m Model) openSnippetRun() (tea.Model, tea.Cmd) {
	s, ok := m.selectedSnippet()
	if !ok {
		return m, nil
	}
	h, ok := m.selectedHost()
	if !ok {
		m.setStatus("no host to run it on — close this and pick one")
		return m, nil
	}
	m.runToken++
	m.running = &snippetRun{
		snippet: s,
		host:    m.d.resolver.Resolve(h).Host,
		token:   m.runToken,
	}
	m.returnTo, m.mode = modeSnippets, modeSnippetRun
	m.setStatus("")
	return m, nil
}

func (m Model) handleSnippetRunKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	r := m.running
	switch {
	case r == nil:
		m.mode = modeSnippets
		return m, nil

	case r.result != nil:
		// Finished, so the keys are for reading what it said.
		switch msg.String() {
		case "esc", "q":
			return m.closeSnippetRun()
		case "j", "down":
			r.scroll++
		case "k", "up":
			r.scroll = max(r.scroll-1, 0)
		case "g":
			r.scroll = 0
		}
		return m, nil

	case r.started:
		if k := msg.String(); k == "esc" || k == "q" {
			if r.stopping {
				// Asked twice, and the result has still not come back.
				// Leaving without it beats being held here by whatever is
				// holding the pipe open; the token is what makes the result
				// harmless when it finally lands.
				return m.closeSnippetRun()
			}
			// The first press stops the run without leaving: what it managed
			// to say before being stopped is worth reading, and closing the
			// screen would take that away at the same moment.
			r.stopping = true
			r.cancel()
			m.setStatus("")
		}
		return m, nil

	default:
		switch msg.String() {
		case "y", "Y":
			return m.startSnippetRun()
		case "esc", "q", "n", "N":
			return m.closeSnippetRun()
		}
		return m, nil
	}
}

func (m Model) startSnippetRun() (tea.Model, tea.Cmd) {
	r := m.running
	ctx, cancel := context.WithCancel(context.Background())
	r.started, r.cancel = true, cancel

	host, script, token := r.host, r.snippet.Script, r.token
	return m, func() tea.Msg {
		res := sshx.Run(ctx, host, script)
		cancel()
		return snippetDoneMsg{token: token, result: res}
	}
}

func (m Model) handleSnippetDone(msg snippetDoneMsg) (tea.Model, tea.Cmd) {
	// A result from a run already closed, whose ssh was still on its way back.
	// Nothing on screen belongs to it any more.
	if m.running == nil || m.running.token != msg.token {
		return m, nil
	}
	res := msg.result
	m.running.result = &res
	m.setStatus("")
	return m, nil
}

func (m Model) closeSnippetRun() (tea.Model, tea.Cmd) {
	if m.running != nil && m.running.cancel != nil {
		m.running.cancel()
	}
	m.running = nil
	m.mode = modeSnippets
	m.setStatus("")
	return m, nil
}

// snippetRunTitle says what is being run, where, and how it went.
func (m Model) snippetRunTitle() string {
	r := m.running
	if r == nil {
		return "Run"
	}
	where := r.snippet.Name + " on " + r.host.Name
	switch {
	case r.result == nil && !r.started:
		return "Run " + strconv.Quote(r.snippet.Name) + " on " + r.host.Name + "?"
	case r.result == nil && r.stopping:
		return where + "  ·  stopping…"
	case r.result == nil:
		return where + "  ·  running…"
	case r.result.Err != nil:
		return where + "  ·  stopped"
	case r.result.ExitCode != 0:
		return fmt.Sprintf("%s  ·  exit %d", where, r.result.ExitCode)
	case r.result.Duration < time.Second:
		// "ok in 0s" is what rounding a fast script comes to, and it reads as
		// a measurement that failed rather than as a script that was quick.
		return where + "  ·  ok"
	default:
		return fmt.Sprintf("%s  ·  ok in %s", where, dur(r.result.Duration))
	}
}

func (m Model) snippetRunRows(content int) int { return max(content-6, 1) }

func (m Model) snippetRunBody(w, content int) string {
	r := m.running
	if r == nil {
		return ""
	}
	switch {
	case r.result == nil && !r.started:
		return m.runPrompt(w)
	case r.result == nil && r.stopping:
		return "\n" + theme.Dim.Render("  stopping…") + "\n\n" +
			theme.Dim.Render("  esc again to leave without waiting")
	case r.result == nil:
		return "\n" + theme.Dim.Render("  running on "+r.host.Name+"…") + "\n\n" +
			theme.Dim.Render("  esc stop")
	default:
		return m.runOutput(w, content)
	}
}

// runPrompt is the screen before anything has run: the script, whole, and the
// one thing about how it will be run that changes what can be written in one.
func (m Model) runPrompt(w int) string {
	r := m.running
	lines := []string{""}
	for _, l := range strings.Split(strings.TrimRight(r.snippet.Script, "\n"), "\n") {
		lines = append(lines, "  "+theme.Fg(theme.Text).Render(ansi.Truncate(l, max(w-4, 8), "…")))
	}
	lines = append(lines,
		"",
		theme.Dim.Render("  on "+r.host.Name+" — "+r.host.Target()),
		"",
		theme.Fg(theme.Yellow).Render("  no terminal there, so a script that asks a question fails"),
		"",
		theme.Dim.Render("  y run  ·  esc cancel"))
	return strings.Join(lines, "\n")
}

// runOutput is what it said, scrollable, with what is not on screen accounted
// for rather than simply absent.
func (m Model) runOutput(w, content int) string {
	r := m.running
	body := strings.Split(strings.TrimRight(r.result.Output, "\n"), "\n")
	if r.result.Output == "" {
		body = []string{theme.Dim.Render("  it said nothing")}
	} else {
		for i, l := range body {
			body[i] = "  " + ansi.Truncate(l, max(w-4, 8), "…")
		}
	}
	if r.result.Truncated {
		body = append(body, theme.Fg(theme.Yellow).Render("  …the rest was more than omassh keeps"))
	}

	rows := m.snippetRunRows(content)
	r.scroll = clamp(r.scroll, 0, max(len(body)-rows, 0))
	end := min(r.scroll+rows, len(body))

	lines := append([]string{""}, body[r.scroll:end]...)
	lines = append(lines, "")
	if more := len(body) - end; more > 0 {
		lines = append(lines, theme.Dim.Render(fmt.Sprintf("  %d more line%s  ·  j/k scroll  ·  esc back", more, plural(more))))
	} else {
		lines = append(lines, theme.Dim.Render("  j/k scroll  ·  esc back"))
	}
	return strings.Join(lines, "\n")
}
