package ui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/cuonggt/omassh/internal/keymap"
	"github.com/cuonggt/omassh/internal/store"
	"github.com/cuonggt/omassh/internal/ui/theme"
)

func (m Model) openSnippets() (tea.Model, tea.Cmd) {
	m.mode = modeSnippets
	m.snipIdx = clamp(m.snipIdx, 0, len(m.d.snippets)-1)
	if n := len(m.d.snippets); n == 0 {
		m.setStatus("no snippets yet — n to add one")
	} else {
		m.setStatus(fmt.Sprintf("%d snippet%s", n, plural(n)))
	}
	return m, nil
}

func (m Model) closeSnippets() (tea.Model, tea.Cmd) {
	m.mode = modeBrowse
	m.setStatus("")
	return m, nil
}

func (m Model) handleSnippetsKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		return m.closeSnippets()
	case "j", "down":
		m.snipIdx = clamp(m.snipIdx+1, 0, len(m.d.snippets)-1)
	case "k", "up":
		m.snipIdx = clamp(m.snipIdx-1, 0, len(m.d.snippets)-1)
	case "n":
		return m.openSnippetForm(store.Snippet{})
	case "e":
		if s, ok := m.selectedSnippet(); ok {
			return m.openSnippetForm(s)
		}
	case "enter":
		return m.openSnippetRun()
	case "p":
		return m.pasteSnippet()
	case "d":
		return m.askDeleteSnippet()
	}
	return m, nil
}

func (m Model) selectedSnippet() (store.Snippet, bool) {
	if len(m.d.snippets) == 0 {
		return store.Snippet{}, false
	}
	return m.d.snippets[clamp(m.snipIdx, 0, len(m.d.snippets)-1)], true
}

func (m Model) openSnippetForm(s store.Snippet) (tea.Model, tea.Cmd) {
	m.form = newSnippetForm(s)
	m.returnTo, m.mode = modeSnippets, modeForm
	return m, m.form.focusCurrent()
}

func newSnippetForm(s store.Snippet) *form {
	title := "New snippet"
	if s.ID != "" {
		title = "Edit " + s.Name
	}
	f := &form{
		kind: formSnippet, title: title, editID: s.ID,
		fields: []field{
			newField("Name", "restart nginx", s.Name),
			newField("Script", "systemctl restart nginx", ""),
		},
		script: s.Script,
	}
	f.showScript()
	return f
}

// showScript puts the script into its field, or a description of it where it
// will not fit.
//
// A one-line script is the ordinary case and is simply typed. Anything longer
// cannot be held by a single-line input, so the field says how big it is and
// ctrl+e opens it in $EDITOR — which is also the only way back to one line,
// since deleting the lines is editing them.
func (f *form) showScript() {
	for i := range f.fields {
		if f.fields[i].label != "Script" {
			continue
		}
		// A trailing newline is what an editor leaves behind and is not a
		// second line, so a one-liner saved in vim stays typeable here.
		body := strings.TrimRight(f.script, "\n")
		if strings.Contains(body, "\n") {
			f.fields[i].readonly = true
			n := store.Snippet{Script: f.script}.Lines()
			f.fields[i].input.SetValue(fmt.Sprintf("%d lines — ctrl+e to edit", n))
			return
		}
		f.fields[i].readonly = false
		f.fields[i].input.SetValue(body)
		return
	}
}

// syncScript keeps the script in step with the field while it is being typed.
// The field holds the script itself while it is one line; once it is more than
// that the field holds a description of it, and this leaves it alone.
func (f *form) syncScript() {
	if f == nil || f.kind != formSnippet {
		return
	}
	for _, x := range f.fields {
		if x.label == "Script" && !x.readonly {
			f.script = x.input.Value()
			return
		}
	}
}

// handleScriptEdited takes back what $EDITOR wrote.
//
// An editor that failed has said nothing about what the script should be, so
// the one on the form stands and the complaint goes where the form's own
// complaints go. An editor that emptied the file has said something: the save
// below refuses it, which is the same answer a script deleted by hand gets.
func (m Model) handleScriptEdited(msg scriptEditedMsg) (tea.Model, tea.Cmd) {
	if m.form == nil || m.form.kind != formSnippet {
		return m, nil
	}
	if msg.err != nil {
		m.form.problem = bareEditorErr(msg.err)
		return m, nil
	}
	m.form.script = msg.script
	m.form.showScript()
	m.form.problem = ""
	return m, m.form.focusCurrent()
}

// bareEditorErr says what went wrong without the exit status of a program the
// person watching did not know was being run on their behalf.
func bareEditorErr(err error) string {
	if s := err.Error(); !strings.Contains(s, "exit status") {
		return s
	}
	return "the editor exited without saving — the script is as it was"
}

func (m Model) saveSnippetForm() (tea.Model, tea.Cmd) {
	f := m.form
	f.syncScript()
	s := store.Snippet{
		ID:     f.editID,
		Name:   strings.TrimSpace(f.value("Name")),
		Script: f.script,
	}
	if s.Name == "" {
		f.problem = "a name is required"
		return m, nil
	}
	if err := s.Valid(); err != nil {
		f.problem = err.Error()
		return m, nil
	}

	saved, err := m.st.PutSnippet(s)
	if err != nil {
		f.problem = err.Error()
		return m, nil
	}
	m.form, m.mode = nil, modeSnippets
	m.reload()
	m.selectSnippet(saved)
	m.setStatusOf(saved.Name, "saved")
	return m, nil
}

// askDeleteSnippet confirms, as deleting anything else here does.
//
// What it has to say is shorter than the others: nothing names a snippet, so
// deleting one cannot take anything else with it. Saying so is worth a line,
// because every other delete in this program does.
func (m Model) askDeleteSnippet() (tea.Model, tea.Cmd) {
	s, ok := m.selectedSnippet()
	if !ok {
		return m, nil
	}
	id, name := s.ID, s.Name
	m.confirm = &confirmation{
		prompt: "Delete snippet " + name + "?",
		detail: "nothing else refers to a snippet, so nothing else changes",
		run: func() (string, error) {
			if err := m.st.DeleteSnippet(id); err != nil {
				return "", err
			}
			return "deleted " + name, nil
		},
	}
	m.returnTo, m.mode = modeSnippets, modeConfirm
	return m, nil
}

// pasteSnippet types a snippet into the session in the pane, and stops there.
//
// This is the escape hatch for everything a run cannot do. A run allocates no
// terminal, so sudo asking for a password, anything full-screen, anything that
// wants a keyboard at all, fails rather than waits — and those are exactly the
// scripts you want in a session you are sitting in front of. Between the two
// there is nothing a snippet cannot be.
//
// It is pasted and not run. The script arrives in the shell's edit buffer
// where it can be read and changed before enter, which is the whole difference
// between this and the other half of the feature.
func (m Model) pasteSnippet() (tea.Model, tea.Cmd) {
	s, ok := m.selectedSnippet()
	if !ok {
		return m, nil
	}
	if m.attached == nil || !m.attached.Alive() {
		m.setStatus("no session in the pane — " + m.keys.Key(keymap.Pane) + " opens one")
		return m, nil
	}

	script := pasteText(s.Script)
	name := m.attached.Host.Name

	out, cmd := m.toRemote(func() { m.attached.Paste(script) })
	m = out.(Model)

	// Looking at what was just pasted, since reading it is the point — and
	// because of what the second message has to admit.
	m.mode, m.focus = modeBrowse, panelSession
	m.setStatusOf(s.Name, pasteResult(script, name))
	return m, cmd
}

// pasteText is the script as it should arrive in a shell's edit buffer.
//
// Without the trailing newline an editor leaves behind. A remote that has
// turned bracketed paste on would take it as one more line of the buffer,
// which is merely untidy; one that has not takes the whole paste as typing,
// and that newline runs the last line on the spot. Trimming it is what makes
// "pasted, not run" true in both cases.
func pasteText(script string) string { return strings.TrimRight(script, "\n") }

// pasteResult says what to expect of the script now sitting in the shell.
//
// One line waits wherever it lands, because what would run it is the newline
// pasteText took off. More than one depends on the shell: one that understands
// bracketed paste holds the whole of it for editing, and one that does not
// runs every line but the last as it arrives. Which of those happened cannot
// be known from here — see term.Pane.Paste — so it is not claimed. Saying "it
// is waiting for you" and being wrong is how a snippet gets run by surprise;
// saying to look, next to a pane this has just put in front of you, is not.
func pasteResult(script, host string) string {
	if strings.Contains(script, "\n") {
		return "pasted into " + host + " — an older shell may have run it already"
	}
	return "pasted into " + host + " — press enter to run it"
}

func (m *Model) selectSnippet(s store.Snippet) {
	for i, x := range m.d.snippets {
		if x.ID == s.ID {
			m.snipIdx = i
			return
		}
	}
}

func (m Model) snippetListRows(content int) int { return max(content-6, 1) }

func (m Model) snippetsBody(w, content int) string {
	if len(m.d.snippets) == 0 {
		return "\n" + theme.Dim.Render("  no snippets yet") + "\n\n" +
			theme.Dim.Render("  a snippet is a script worth keeping — named once,") + "\n" +
			theme.Dim.Render("  so it is not typed again the next time") + "\n\n" +
			theme.Dim.Render("  n new  ·  esc close")
	}

	rows := m.snippetListRows(content)
	start, end := listWindow(m.snipIdx, len(m.d.snippets), rows)

	// The names column is as wide as the names, up to a point: past that the
	// script has no room left to show anything of itself.
	nameW := 0
	for _, s := range m.d.snippets {
		nameW = max(nameW, ansi.StringWidth(s.Name))
	}
	nameW = min(nameW, 24)

	lines := []string{""}
	for i, s := range m.d.snippets[start:end] {
		i += start
		mark, colour := "  ", theme.Text
		if i == m.snipIdx {
			mark, colour = "▸ ", theme.Accent
		}
		row := pad(ansi.Truncate(s.Name, nameW, "…"), nameW) + "  " + s.Describe()
		lines = append(lines, theme.Fg(colour).Render("  "+mark+ansi.Truncate(row, max(w-6, 8), "…")))
	}

	lines = append(lines, "", theme.Dim.Render("  ↵ run  ·  p paste  ·  n/e/d new/edit/delete  ·  esc close"))
	return strings.Join(lines, "\n")
}
