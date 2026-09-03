package ui

import (
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/cuonggt/omassh/internal/ui/theme"
)

type formKind int

const (
	formHost formKind = iota
	formGroup
	formIdentity
	formGenKey
	formForward
	formMkdir
	formRename
	formChmod
	formSnippet
	formTypedRun
)

type field struct {
	label string
	hint  string
	input textinput.Model
}

// form is the modal used to add and edit hosts and groups.
type form struct {
	kind    formKind
	title   string
	editID  string // empty when creating
	fields  []field
	idx     int
	problem string

	// hostKey carries the owning host through a forward form.
	hostKey string

	// danger and expect drive the typed confirmation for a destructive run.
	danger string
	expect string
}

// newSecretField is a masked input for passphrases and passwords.
func newSecretField(label, hint string) field {
	f := newField(label, hint, "")
	f.input.EchoMode = textinput.EchoPassword
	f.input.EchoCharacter = '•'
	return f
}

func newField(label, hint, value string) field {
	ti := textinput.New()
	ti.Prompt = ""
	ti.Placeholder = hint
	ti.SetValue(value)
	return field{label: label, hint: hint, input: ti}
}

func (f *form) focusCurrent() tea.Cmd {
	for i := range f.fields {
		f.fields[i].input.Blur()
	}
	return f.fields[f.idx].input.Focus()
}

func (f *form) value(label string) string {
	for _, x := range f.fields {
		if x.label == label {
			return strings.TrimSpace(x.input.Value())
		}
	}
	return ""
}

func (f *form) move(d int) tea.Cmd {
	f.idx = (f.idx + d + len(f.fields)) % len(f.fields)
	return f.focusCurrent()
}

func (f *form) update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	f.fields[f.idx].input, cmd = f.fields[f.idx].input.Update(msg)
	return cmd
}

func (f *form) render(w int) string {
	labelW := 0
	for _, x := range f.fields {
		labelW = max(labelW, len(x.label))
	}
	// Inputs default to zero width, which renders placeholders as a single
	// character. Size them once the box width is known.
	inputW := clamp(w-labelW-8, 12, 48)
	for i := range f.fields {
		f.fields[i].input.SetWidth(inputW)
	}

	var b strings.Builder
	b.WriteString("\n")
	for i, x := range f.fields {
		marker := "  "
		if i == f.idx {
			marker = theme.Fg(theme.Accent).Render("▸ ")
		}
		label := theme.Dim.Render(pad(x.label, labelW) + "  ")
		if i == f.idx {
			label = theme.Fg(theme.TextBrt).Render(pad(x.label, labelW) + "  ")
		}
		b.WriteString("  " + marker + label + x.input.View() + "\n")
	}
	if f.danger != "" {
		b.WriteString("\n  " + theme.Fg(theme.Red).Render("⚠ matches "+f.danger) +
			theme.Dim.Render("  — type the host count to confirm") + "\n")
	}
	if f.problem != "" {
		b.WriteString("\n  " + theme.Fg(theme.Red).Render("✖ "+f.problem) + "\n")
	}
	b.WriteString("\n" + theme.Dim.Render("  ") +
		hint("tab", "next field") + theme.Dim.Render("  ·  ") +
		hint("↵", "save") + theme.Dim.Render("  ·  ") +
		hint("esc", "cancel"))
	return b.String()
}

func pad(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return s + strings.Repeat(" ", n-len(s))
}

func equalFold(a, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}
