package ui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/cuonggt/omassh/internal/runner"
	"github.com/cuonggt/omassh/internal/store"
	"github.com/cuonggt/omassh/internal/ui/theme"
)

// runTimeout bounds a whole fan-out, so a wedged host cannot pin the results
// view open indefinitely.
const runTimeout = 5 * time.Minute

// pendingRun is a run awaiting confirmation.
type pendingRun struct {
	snippet store.Snippet
	hosts   []store.Host
	danger  string
}

// runEvent carries either one host's result or the end of the run.
type runEvent struct {
	result runner.Result
	done   bool
}

func waitResult(ch <-chan runEvent) tea.Cmd {
	return func() tea.Msg { return <-ch }
}

// --- entry -------------------------------------------------------------

func (m Model) openSnippets() (tea.Model, tea.Cmd) {
	m.mode = modeSnippets
	m.snippetIdx = clamp(m.snippetIdx, 0, len(m.d.snippets)-1)
	return m, nil
}

func (m Model) handleSnippetsKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc", "q", "S":
		m.mode = modeBrowse
		return m, nil
	case "j", "down":
		m.snippetIdx = clamp(m.snippetIdx+1, 0, len(m.d.snippets)-1)
	case "k", "up":
		m.snippetIdx = clamp(m.snippetIdx-1, 0, len(m.d.snippets)-1)
	case "n":
		m.form = newSnippetForm(store.Snippet{})
		m.returnTo, m.mode = modeSnippets, modeForm
		return m, m.form.focusCurrent()
	case "e":
		sn, ok := m.selectedSnippet()
		if !ok {
			return m, nil
		}
		m.form = newSnippetForm(sn)
		m.returnTo, m.mode = modeSnippets, modeForm
		return m, m.form.focusCurrent()
	case "d":
		return m.askDeleteSnippet()
	case "enter":
		return m.prepareRun(false)
	case "f":
		return m.prepareRun(true)
	case "R":
		if len(m.results) > 0 {
			m.mode = modeResults
		}
	}
	return m, nil
}

func (m Model) selectedSnippet() (store.Snippet, bool) {
	if len(m.d.snippets) == 0 {
		return store.Snippet{}, false
	}
	return m.d.snippets[clamp(m.snippetIdx, 0, len(m.d.snippets)-1)], true
}

// runTargets is the host or hosts a run would reach.
func (m Model) runTargets(fanOut bool) []store.Host {
	if !fanOut {
		h, ok := m.selectedHost()
		if !ok {
			return nil
		}
		return []store.Host{m.d.resolver.Resolve(h).Host}
	}
	g, ok := m.currentGroup()
	if !ok {
		return nil
	}
	var out []store.Host
	for _, h := range m.d.hostsIn(g.ID) {
		out = append(out, m.d.resolver.Resolve(h).Host)
	}
	return out
}

// prepareRun gathers targets and decides how much confirmation is warranted.
func (m Model) prepareRun(fanOut bool) (tea.Model, tea.Cmd) {
	sn, ok := m.selectedSnippet()
	if !ok {
		return m, nil
	}
	hosts := m.runTargets(fanOut)
	if len(hosts) == 0 {
		m.setStatus("no hosts selected to run on")
		return m, nil
	}

	pr := pendingRun{snippet: sn, hosts: hosts}
	pr.danger, _ = runner.Dangerous(sn.Command)
	m.pending = pr

	// A destructive-looking command always asks, and asks for the host count
	// to be typed: the number is the blast radius, so typing it is the point.
	if pr.danger != "" {
		m.form = newTypedConfirmForm(pr)
		m.returnTo, m.mode = modeSnippets, modeForm
		return m, m.form.focusCurrent()
	}
	// A single host is an ordinary action; a fan-out is not.
	if len(hosts) == 1 {
		return m.startRun()
	}

	names := hostNames(hosts, 6)
	m.confirm = &confirmation{
		prompt: fmt.Sprintf("Run %q on %d hosts?", sn.Name, len(hosts)),
		detail: names,
		run:    func() (string, error) { return "", nil },
	}
	m.confirmRun = true
	m.returnTo, m.mode = modeSnippets, modeConfirm
	return m, nil
}

func (m Model) startRun() (tea.Model, tea.Cmd) {
	pr := m.pending
	ctx, cancel := context.WithTimeout(context.Background(), runTimeout)

	m.runCancel = cancel
	m.results = nil
	m.resultIdx, m.outputTop = 0, 0
	m.runTotal, m.runDone = len(pr.hosts), 0
	m.running = true
	m.runLabel = pr.snippet.Name
	m.mode = modeResults

	ch := m.resultsCh
	hosts, command := pr.hosts, pr.snippet.Command
	go func() {
		defer cancel()
		runner.RunAll(ctx, hosts, command, runner.DefaultLimit, func(r runner.Result) {
			ch <- runEvent{result: r}
		})
		ch <- runEvent{done: true}
	}()

	m.setStatus(fmt.Sprintf("running %q on %d host(s)…", pr.snippet.Name, len(pr.hosts)))
	return m, waitResult(ch)
}

func (m Model) handleRunEvent(ev runEvent) (tea.Model, tea.Cmd) {
	if ev.done {
		m.running = false
		failed := 0
		for _, r := range m.results {
			if !r.OK() {
				failed++
			}
		}
		if failed == 0 {
			m.setStatus(fmt.Sprintf("%s — %d host%s ok", m.runLabel, len(m.results), plural(len(m.results))))
		} else {
			m.setErr(fmt.Errorf("%s — %d of %d hosts failed", m.runLabel, failed, len(m.results)))
		}
		return m, nil
	}
	m.results = append(m.results, ev.result)
	m.runDone++
	return m, waitResult(m.resultsCh)
}

func (m Model) handleResultsKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		if m.runCancel != nil {
			m.runCancel()
		}
		return m, tea.Quit
	case "esc", "q":
		if m.running && m.runCancel != nil {
			m.runCancel()
			m.setStatus("run cancelled")
			return m, nil
		}
		m.mode = modeSnippets
		return m, nil
	case "j", "down":
		m.resultIdx = clamp(m.resultIdx+1, 0, len(m.results)-1)
		m.outputTop = 0
	case "k", "up":
		m.resultIdx = clamp(m.resultIdx-1, 0, len(m.results)-1)
		m.outputTop = 0
	case "ctrl+d":
		m.outputTop += 10
	case "ctrl+u":
		m.outputTop = max(m.outputTop-10, 0)
	}
	return m, nil
}

func (m Model) askDeleteSnippet() (tea.Model, tea.Cmd) {
	sn, ok := m.selectedSnippet()
	if !ok {
		return m, nil
	}
	id, name := sn.ID, sn.Name
	m.confirm = &confirmation{
		prompt: "Delete snippet " + name + "?",
		detail: "nothing is run; only the saved command is removed",
		run:    func() (string, error) { return "deleted " + name, m.st.DeleteSnippet(id) },
	}
	m.returnTo, m.mode = modeSnippets, modeConfirm
	return m, nil
}

// --- forms -------------------------------------------------------------

func newSnippetForm(sn store.Snippet) *form {
	title := "New snippet"
	if sn.ID != "" {
		title = "Edit " + sn.Name
	}
	return &form{
		kind: formSnippet, title: title, editID: sn.ID,
		fields: []field{
			newField("Name", "restart nginx", sn.Name),
			newField("Command", "sudo systemctl restart nginx", sn.Command),
		},
	}
}

func newTypedConfirmForm(pr pendingRun) *form {
	want := strconv.Itoa(len(pr.hosts))
	// The label asks and the placeholder explains; repeating the number as a
	// pre-filled-looking value made it read as though it were already answered.
	f := singleFieldForm(formTypedRun,
		fmt.Sprintf("%q looks destructive", pr.snippet.Name),
		"Confirm", "type "+want+" to run on "+want+" host"+plural(len(pr.hosts)), "")
	f.danger = pr.danger
	f.expect = want
	return f
}

func (m Model) saveSnippetForm() (tea.Model, tea.Cmd) {
	f := m.form
	sn := store.Snippet{ID: f.editID, Name: f.value("Name"), Command: f.value("Command")}
	if _, err := m.st.PutSnippet(sn); err != nil {
		f.problem = err.Error()
		return m, nil
	}
	m.form, m.mode = nil, modeSnippets
	m.reload()
	m.setStatus("saved snippet " + sn.Name)
	return m, nil
}

func (m Model) saveTypedRunForm() (tea.Model, tea.Cmd) {
	f := m.form
	if f.value(f.fields[0].label) != f.expect {
		f.problem = "type " + f.expect + " exactly to confirm"
		return m, nil
	}
	m.form = nil
	return m.startRun()
}

// --- rendering ---------------------------------------------------------

func (m Model) snippetsBody(w int) string {
	var b strings.Builder
	b.WriteString("\n")

	target := "no host selected"
	if h, ok := m.selectedHost(); ok {
		target = h.Name
	}
	group := "—"
	n := 0
	if g, ok := m.currentGroup(); ok {
		group, n = g.Name, len(m.d.hostsIn(g.ID))
	}
	b.WriteString("  " + theme.Dim.Render("↵ runs on ") + theme.Fg(theme.Accent).Render(target) +
		theme.Dim.Render("   f fans out over ") + theme.Fg(theme.Accent).Render(group) +
		theme.Dim.Render(fmt.Sprintf(" (%d host%s)", n, plural(n))) + "\n\n")

	if len(m.d.snippets) == 0 {
		b.WriteString(theme.Dim.Render("  no snippets yet — ") + theme.Key.Render("n") +
			theme.Dim.Render(" to add one\n"))
	}
	for i, sn := range m.d.snippets {
		marker, nameStyle := "  ", theme.Normal
		if i == m.snippetIdx {
			marker, nameStyle = theme.Fg(theme.Accent).Render("▸ "), theme.Fg(theme.TextBrt).Bold(true)
		}
		line := "  " + marker + nameStyle.Render(sn.Name)
		if p, risky := runner.Dangerous(sn.Command); risky {
			line += theme.Fg(theme.Red).Render("  ⚠ " + p)
		}
		b.WriteString(line + "\n")
		b.WriteString("      " + theme.Fg(theme.Green).Render(ansi.Truncate(sn.Command, max(w-8, 10), "…")) + "\n\n")
	}

	b.WriteString("\n  " + hint("↵", "run here") + sep() + hint("f", "fan out") + sep() +
		hint("n/e/d", "new/edit/delete") + sep() + hint("R", "last results") + sep() + hint("esc", "back"))
	return b.String()
}

func (m Model) resultsView(content int) string {
	listW := clamp(m.w/3, 24, 44)
	outW := m.w - listW

	title := "Results · " + m.runLabel
	if m.running {
		title = fmt.Sprintf("Running · %s · %d/%d", m.runLabel, m.runDone, m.runTotal)
	}
	return lipgloss.JoinHorizontal(lipgloss.Top,
		box(title, true, listW, content, m.resultListBody(listW-4)),
		box(m.outputTitle(), false, outW, content, m.outputBody(outW-4, content-2)),
	)
}

func (m Model) resultListBody(w int) string {
	if len(m.results) == 0 {
		return theme.Dim.Render("waiting…")
	}
	lines := make([]string, 0, len(m.results))
	for i, r := range m.results {
		mark, col := "✔", theme.Green
		if !r.OK() {
			mark, col = "✖", theme.Red
		}
		text := mark + " " + r.HostName
		if i == m.resultIdx {
			lines = append(lines, row(text, true, w))
			continue
		}
		lines = append(lines, theme.Fg(col).Render(mark)+theme.Normal.Render(" "+r.HostName))
	}
	return strings.Join(lines, "\n")
}

func (m Model) outputTitle() string {
	if len(m.results) == 0 {
		return "Output"
	}
	r := m.results[clamp(m.resultIdx, 0, len(m.results)-1)]
	return fmt.Sprintf("%s · exit %d · %s", r.HostName, r.ExitCode, dur(r.Duration))
}

func (m Model) outputBody(w, h int) string {
	if len(m.results) == 0 {
		return theme.Dim.Render("no results yet")
	}
	r := m.results[clamp(m.resultIdx, 0, len(m.results)-1)]

	var lines []string
	if r.Err != nil {
		lines = append(lines, theme.Fg(theme.Red).Render(r.Err.Error()), "")
	}
	for _, l := range splitNonEmpty(r.Stdout) {
		lines = append(lines, theme.Normal.Render(l))
	}
	if s := splitNonEmpty(r.Stderr); len(s) > 0 {
		lines = append(lines, "", theme.Dim.Render("stderr"))
		for _, l := range s {
			lines = append(lines, theme.Fg(theme.Yellow).Render(l))
		}
	}
	if len(lines) == 0 {
		return theme.Dim.Render("no output")
	}

	top := clamp(m.outputTop, 0, max(len(lines)-1, 0))
	lines = lines[top:]
	if len(lines) > h {
		lines = lines[:h]
	}
	return strings.Join(lines, "\n")
}

func splitNonEmpty(s string) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func hostNames(hosts []store.Host, limit int) string {
	names := make([]string, 0, len(hosts))
	for i, h := range hosts {
		if i == limit {
			names = append(names, fmt.Sprintf("and %d more", len(hosts)-limit))
			break
		}
		names = append(names, h.Name)
	}
	return strings.Join(names, ", ")
}
