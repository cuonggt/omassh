package ui

import (
	"context"
	"fmt"
	"image/color"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/cuonggt/omassh/internal/sshx"
	"github.com/cuonggt/omassh/internal/store"
	"github.com/cuonggt/omassh/internal/ui/theme"
)

// runTarget is one way of saying where a snippet should run.
type runTarget struct {
	label  string
	detail string
	hosts  []store.Host
	// pick opens the host list instead of running, for a set that is none of
	// the ready-made ones.
	pick bool
}

// snippetRun is a snippet being run: what it is, where, and how far it got.
//
// Three phases and one screen. Before anything runs it shows the script and
// the ways of saying where — the list shows a long snippet by its size, so
// enter alone would run something the person pressing it cannot see, on
// machines they have to be right about. Then it is a table of hosts filling in
// as they finish. Then it is one host's output, read in full.
type snippetRun struct {
	snippet store.Snippet

	// token tells this run's results from those of a run already closed whose
	// ssh is still on its way back.
	token int

	// Choosing.
	choices []runTarget
	target  int
	picking bool
	pickIdx int
	chosen  map[string]bool
	all     []store.Host

	// Running.
	hosts    []store.Host
	begun    map[string]bool
	started  bool
	stopping bool
	cancel   context.CancelFunc
	results  map[string]*sshx.Result

	// Reading. showing is the host whose output is on screen, empty while the
	// table is.
	idx     int
	showing string
	scroll  int
}

// snippetEvent is a host starting, a host's result, or the end of a run.
type snippetEvent struct {
	token int
	host  store.Host
	// began is a host whose ssh has just been started. With four workers and
	// forty hosts most of them have not, and a table showing every one as
	// running would be describing sessions that are not open.
	began  bool
	result sshx.Result
	done   bool
}

func waitRun(ch <-chan snippetEvent) tea.Cmd {
	return func() tea.Msg { return <-ch }
}

func (m Model) openSnippetRun() (tea.Model, tea.Cmd) {
	s, ok := m.selectedSnippet()
	if !ok {
		return m, nil
	}
	choices := m.runChoices()
	if len(choices) == 0 {
		m.setStatus("no host to run it on — close this and add one")
		return m, nil
	}
	m.runToken++
	m.running = &snippetRun{
		snippet: s,
		token:   m.runToken,
		choices: choices,
		chosen:  map[string]bool{},
		all:     m.d.hosts,
	}
	m.returnTo, m.mode = modeSnippets, modeSnippetRun
	m.setStatus("")
	return m, nil
}

// runChoices are the ready-made answers to "where", in the order someone is
// most likely to want them: the host in front of them, then the ones beside
// it, then any set at all.
func (m Model) runChoices() []runTarget {
	var out []runTarget
	if h, ok := m.selectedHost(); ok {
		out = append(out, runTarget{
			label:  "this host",
			detail: h.Name,
			hosts:  []store.Host{m.d.resolver.Resolve(h).Host},
		})
	}
	if visible := m.visibleHosts(); len(visible) > 1 {
		// Named for what the list is actually showing. Under a search it is
		// the matches and not a group at all, and calling that "this group"
		// would be inviting someone to run a script on a set other than the
		// one in front of them.
		label, detail := "these hosts", fmt.Sprintf("%d shown", len(visible))
		if !m.filtering() {
			if g, ok := m.currentGroup(); ok {
				label = "this group"
				detail = fmt.Sprintf("%s — %d host%s", g.Name, len(visible), plural(len(visible)))
			}
		}
		out = append(out, runTarget{label: label, detail: detail, hosts: m.resolveAll(visible)})
	}
	if len(m.d.hosts) > 0 {
		out = append(out, runTarget{
			label:  "pick hosts…",
			detail: "space to choose, ↵ when done",
			pick:   true,
		})
	}
	return out
}

func (m Model) resolveAll(hosts []store.Host) []store.Host {
	out := make([]store.Host, 0, len(hosts))
	for _, h := range hosts {
		out = append(out, m.d.resolver.Resolve(h).Host)
	}
	return out
}

func (m Model) handleSnippetRunKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	r := m.running
	switch {
	case r == nil:
		m.mode = modeSnippets
		return m, nil
	case r.picking:
		return m.handleRunPickKey(msg)
	case r.showing != "":
		return m.handleRunOutputKey(msg)
	case r.started:
		return m.handleRunningKey(msg)
	default:
		return m.handleRunChoiceKey(msg)
	}
}

func (m Model) handleRunChoiceKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	r := m.running
	switch msg.String() {
	case "j", "down":
		r.target = clamp(r.target+1, 0, len(r.choices)-1)
	case "k", "up":
		r.target = clamp(r.target-1, 0, len(r.choices)-1)
	case "y", "Y", "enter":
		if r.choices[r.target].pick {
			r.picking, r.pickIdx = true, 0
			return m, nil
		}
		return m.startSnippetRun()
	case "esc", "q", "n", "N":
		return m.closeSnippetRun()
	}
	return m, nil
}

func (m Model) handleRunPickKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	r := m.running
	switch msg.String() {
	case "j", "down":
		r.pickIdx = clamp(r.pickIdx+1, 0, len(r.all)-1)
	case "k", "up":
		r.pickIdx = clamp(r.pickIdx-1, 0, len(r.all)-1)
	case " ", "space":
		if h, ok := r.pickedHost(); ok {
			r.chosen[h.ID] = !r.chosen[h.ID]
		}
	case "a":
		// All or none, because choosing eleven of twelve is otherwise eleven
		// presses and choosing none again is another eleven.
		none := len(r.chosenHosts()) == 0
		for _, h := range r.all {
			r.chosen[h.ID] = none
		}
	case "enter":
		if len(r.chosenHosts()) == 0 {
			return m, nil
		}
		r.picking = false
		n := len(r.chosenHosts())
		r.choices[r.target] = runTarget{
			label:  "chosen",
			detail: fmt.Sprintf("%d host%s", n, plural(n)),
			hosts:  m.resolveAll(r.chosenHosts()),
		}
	case "esc":
		r.picking = false
	}
	return m, nil
}

func (r *snippetRun) pickedHost() (store.Host, bool) {
	if len(r.all) == 0 {
		return store.Host{}, false
	}
	return r.all[clamp(r.pickIdx, 0, len(r.all)-1)], true
}

func (r *snippetRun) chosenHosts() []store.Host {
	var out []store.Host
	for _, h := range r.all {
		if r.chosen[h.ID] {
			out = append(out, h)
		}
	}
	return out
}

func (m Model) handleRunningKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	r := m.running
	switch msg.String() {
	case "j", "down":
		r.idx = clamp(r.idx+1, 0, len(r.hosts)-1)
	case "k", "up":
		r.idx = clamp(r.idx-1, 0, len(r.hosts)-1)
	case "enter":
		if h, ok := r.hostAt(r.idx); ok && r.results[h.ID] != nil {
			r.showing, r.scroll = h.ID, 0
		}
	case "esc", "q":
		switch {
		case r.finished():
			return m.closeSnippetRun()
		case r.stopping:
			// Asked twice, and results are still not back. Leaving beats
			// being held here by whatever is holding a pipe open; the token
			// is what makes the stray results harmless when they land.
			return m.closeSnippetRun()
		default:
			// The first press stops the run without leaving: what the hosts
			// that did finish said is worth reading, and closing the screen
			// would take it away at the same moment.
			r.stopping = true
			r.cancel()
			m.setStatus("")
		}
	}
	return m, nil
}

func (m Model) handleRunOutputKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	r := m.running
	switch msg.String() {
	case "j", "down":
		r.scroll++
	case "k", "up":
		r.scroll = max(r.scroll-1, 0)
	case "g":
		r.scroll = 0
	case "esc", "q":
		// Back to the table, unless there was never one worth showing.
		if len(r.hosts) == 1 {
			return m.closeSnippetRun()
		}
		r.showing, r.scroll = "", 0
	}
	return m, nil
}

func (m Model) startSnippetRun() (tea.Model, tea.Cmd) {
	r := m.running
	hosts := r.choices[r.target].hosts
	if len(hosts) == 0 {
		return m, nil
	}
	r.hosts, r.started = hosts, true
	r.results, r.begun = map[string]*sshx.Result{}, map[string]bool{}

	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel

	ch, token, script := m.runCh, r.token, r.snippet.Script
	go func() {
		defer cancel()
		sshx.RunAll(ctx, hosts, script, 0, sshx.RunEvents{
			Began: func(h store.Host) {
				ch <- snippetEvent{token: token, host: h, began: true}
			},
			Done: func(h store.Host, res sshx.Result) {
				ch <- snippetEvent{token: token, host: h, result: res}
			},
		})
		ch <- snippetEvent{token: token, done: true}
	}()
	return m, waitRun(ch)
}

func (m Model) handleSnippetEvent(ev snippetEvent) (tea.Model, tea.Cmd) {
	// An event from a run already closed, whose ssh was still on its way back.
	// Nothing on screen is its — but the channel is shared, so reading has to
	// go on or the run that is current would stop being heard.
	if m.running == nil || m.running.token != ev.token {
		return m, waitRun(m.runCh)
	}
	r := m.running
	if ev.done {
		m.setStatus("")
		// One host needs no table: what was wanted is what it said.
		if len(r.hosts) == 1 && r.showing == "" {
			r.showing = r.hosts[0].ID
		}
		return m, nil
	}
	if ev.began {
		r.begun[ev.host.ID] = true
		return m, waitRun(m.runCh)
	}
	res := ev.result
	r.results[ev.host.ID] = &res
	return m, waitRun(m.runCh)
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

func (r *snippetRun) hostAt(i int) (store.Host, bool) {
	if len(r.hosts) == 0 {
		return store.Host{}, false
	}
	return r.hosts[clamp(i, 0, len(r.hosts)-1)], true
}

func (r *snippetRun) finished() bool { return len(r.results) == len(r.hosts) }

// tally is how the run is going: finished and well, finished and not,
// stopped, and still to come.
//
// Stopped is its own count and not a kind of failure. Result.Failed groups
// them, which is right for deciding whether a row is worth looking at and
// wrong here: a sweep someone stopped after two hosts reported "6 failed"
// over six rows each of which said "stopped".
func (r *snippetRun) tally() (ok, failed, stopped, pending int) {
	for _, h := range r.hosts {
		switch res := r.results[h.ID]; {
		case res == nil:
			pending++
		case res.Err != nil:
			stopped++
		case res.ExitCode != 0:
			failed++
		default:
			ok++
		}
	}
	return ok, failed, stopped, pending
}

func (m Model) snippetRunTitle() string {
	r := m.running
	if r == nil {
		return "Run"
	}
	if r.showing != "" {
		return m.outputTitle()
	}
	if !r.started {
		return "Run " + strconv.Quote(r.snippet.Name)
	}

	ok, failed, stopped, pending := r.tally()
	var parts []string
	if ok > 0 {
		parts = append(parts, fmt.Sprintf("%d ok", ok))
	}
	if failed > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", failed))
	}
	if stopped > 0 {
		parts = append(parts, fmt.Sprintf("%d stopped", stopped))
	}
	switch {
	case pending > 0 && r.stopping:
		parts = append(parts, fmt.Sprintf("%d stopping", pending))
	case pending > 0:
		running, queued := 0, 0
		for _, h := range r.hosts {
			if r.results[h.ID] != nil {
				continue
			}
			if r.begun[h.ID] {
				running++
			} else {
				queued++
			}
		}
		if running > 0 {
			parts = append(parts, fmt.Sprintf("%d running", running))
		}
		if queued > 0 {
			parts = append(parts, fmt.Sprintf("%d queued", queued))
		}
	}
	if len(parts) == 0 {
		parts = append(parts, "starting")
	}
	return r.snippet.Name + "  ·  " + strings.Join(parts, ", ")
}

// outputTitle names the host being read and how its run ended.
//
// The snippet's name comes too where this is the whole run, since there is no
// table behind it carrying that; with several hosts the table has it, and
// repeating it here would push the host's own name out of a narrow box.
func (m Model) outputTitle() string {
	r := m.running
	res := r.results[r.showing]
	name := r.showing
	for _, h := range r.hosts {
		if h.ID == r.showing {
			name = h.Name
		}
	}
	if len(r.hosts) == 1 {
		name = r.snippet.Name + " on " + name
	}
	switch {
	case res == nil:
		return name
	case res.Err != nil:
		return name + "  ·  stopped"
	case res.ExitCode != 0:
		return fmt.Sprintf("%s  ·  exit %d", name, res.ExitCode)
	case res.Duration < time.Second:
		// "ok in 0s" is what rounding a fast script comes to, and it reads as
		// a measurement that failed rather than as a script that was quick.
		return name + "  ·  ok"
	default:
		return fmt.Sprintf("%s  ·  ok in %s", name, dur(res.Duration))
	}
}

func (m Model) snippetRunRows(content int) int { return max(content-6, 1) }

func (m Model) snippetRunBody(w, content int) string {
	r := m.running
	switch {
	case r == nil:
		return ""
	case r.picking:
		return m.runPickBody(w, content)
	case r.showing != "":
		return m.runOutput(w, content)
	case !r.started:
		return m.runPrompt(w)
	default:
		return m.runTable(w, content)
	}
}

// runPrompt is the screen before anything runs: the script, whole, the ways of
// saying where, and the one thing about how it will be run that changes what
// can be written in a snippet at all.
func (m Model) runPrompt(w int) string {
	r := m.running
	lines := []string{""}
	for _, l := range strings.Split(strings.TrimRight(r.snippet.Script, "\n"), "\n") {
		lines = append(lines, "  "+theme.Fg(theme.Text).Render(ansi.Truncate(l, max(w-4, 8), "…")))
	}
	lines = append(lines, "")

	labelW := 0
	for _, c := range r.choices {
		labelW = max(labelW, ansi.StringWidth(c.label))
	}
	for i, c := range r.choices {
		mark, colour := "  ", theme.Text
		if i == r.target {
			mark, colour = "▸ ", theme.Accent
		}
		lines = append(lines, theme.Fg(colour).Render("  "+mark+pad(c.label, labelW))+
			"  "+theme.Dim.Render(c.detail))
	}

	lines = append(lines,
		"",
		theme.Fg(theme.Yellow).Render("  no terminal there, so a script that asks a question fails"),
		"",
		theme.Dim.Render("  y run  ·  j/k where  ·  esc cancel"))
	return strings.Join(lines, "\n")
}

// runPickBody is the host list, for a set that is none of the ready-made ones.
func (m Model) runPickBody(w, content int) string {
	r := m.running
	rows := max(m.snippetRunRows(content)-1, 1)
	start, end := listWindow(r.pickIdx, len(r.all), rows)

	nameW := 0
	for _, h := range r.all {
		nameW = max(nameW, ansi.StringWidth(h.Name))
	}
	nameW = min(nameW, 20)

	lines := []string{""}
	for i, h := range r.all[start:end] {
		i += start
		mark, colour := "  ", theme.Text
		if i == r.pickIdx {
			mark, colour = "▸ ", theme.Accent
		}
		box := "☐ "
		if r.chosen[h.ID] {
			box = "☑ "
		}
		name := pad(ansi.Truncate(h.Name, nameW, "…"), nameW)
		lines = append(lines, theme.Fg(colour).Render("  "+mark+box+name)+
			"  "+theme.Dim.Render(ansi.Truncate(h.Addr, max(w-nameW-10, 8), "…")))
	}
	// Two lines, because one holding the tally and four keys runs past the
	// width of the box and loses the last of them.
	n := len(r.chosenHosts())
	lines = append(lines, "",
		theme.Dim.Render(fmt.Sprintf("  %d of %d chosen", n, len(r.all))),
		theme.Dim.Render("  space toggle  ·  a all/none  ·  ↵ done  ·  esc back"))
	return strings.Join(lines, "\n")
}

// runTable is one row per host, filling in as they finish.
func (m Model) runTable(w, content int) string {
	r := m.running
	rows := m.snippetRunRows(content)
	start, end := listWindow(r.idx, len(r.hosts), rows)

	nameW := 0
	for _, h := range r.hosts {
		nameW = max(nameW, ansi.StringWidth(h.Name))
	}
	nameW = min(nameW, 20)

	lines := []string{""}
	for i, h := range r.hosts[start:end] {
		i += start
		cursor := "  "
		if i == r.idx {
			cursor = "▸ "
		}
		mark, colour, status := runMark(r, h)
		name := pad(ansi.Truncate(h.Name, nameW, "…"), nameW)
		body := theme.Fg(colour).Render(cursor+mark+" ") + theme.Fg(theme.Text).Render(name) + "  " +
			theme.Dim.Render(ansi.Truncate(status, max(w-nameW-10, 8), "…"))
		lines = append(lines, "  "+body)
	}

	lines = append(lines, "")
	switch {
	case r.finished():
		lines = append(lines, theme.Dim.Render("  ↵ full output  ·  j/k move  ·  esc close"))
	case r.stopping:
		lines = append(lines, theme.Dim.Render("  stopping  ·  esc again to leave without waiting"))
	default:
		lines = append(lines, theme.Dim.Render("  ↵ full output  ·  esc stop"))
	}
	return strings.Join(lines, "\n")
}

// runMark is the symbol, colour and one-line summary for a host's row.
func runMark(r *snippetRun, h store.Host) (string, color.Color, string) {
	res := r.results[h.ID]
	switch {
	case res == nil && r.stopping:
		return "○", theme.TextDim, "stopping…"
	case res == nil && !r.begun[h.ID]:
		// Not started: the sweep runs a few at a time, and saying "running"
		// here would claim an ssh session that has not been opened.
		return "○", theme.TextDim, "queued"
	case res == nil:
		return "○", theme.TextDim, "running…"
	case res.Err != nil:
		return "◌", theme.Yellow, "stopped"
	case res.ExitCode != 0:
		return "✖", theme.Red, strOr(res.Summary(), fmt.Sprintf("exited %d, saying nothing", res.ExitCode))
	default:
		return "●", theme.Green, strOr(res.Summary(), "said nothing")
	}
}

// runOutput is what one host said, scrollable, with what is not on screen
// accounted for rather than simply absent.
func (m Model) runOutput(w, content int) string {
	r := m.running
	res := r.results[r.showing]
	if res == nil {
		return ""
	}

	var body []string
	if res.Output == "" {
		body = []string{theme.Dim.Render("  it said nothing")}
	} else {
		for _, l := range strings.Split(strings.TrimRight(res.Output, "\n"), "\n") {
			body = append(body, "  "+ansi.Truncate(l, max(w-4, 8), "…"))
		}
	}
	if res.Truncated {
		body = append(body, theme.Fg(theme.Yellow).Render("  …the rest was more than omassh keeps"))
	}

	rows := m.snippetRunRows(content)
	r.scroll = clamp(r.scroll, 0, max(len(body)-rows, 0))
	end := min(r.scroll+rows, len(body))

	back := "esc back"
	if len(r.hosts) == 1 {
		back = "esc close"
	}
	lines := append([]string{""}, body[r.scroll:end]...)
	lines = append(lines, "")
	if more := len(body) - end; more > 0 {
		lines = append(lines, theme.Dim.Render(fmt.Sprintf("  %d more line%s  ·  j/k scroll  ·  %s", more, plural(more), back)))
	} else {
		lines = append(lines, theme.Dim.Render("  j/k scroll  ·  "+back))
	}
	return strings.Join(lines, "\n")
}
