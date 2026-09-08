package ui

import (
	"slices"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/cuonggt/omassh/internal/ui/theme"
)

type formKind int

const (
	formHost formKind = iota
	formGroup
	formMkdir
	formRename
	formChmod
)

type field struct {
	label string
	hint  string
	input textinput.Model

	// choices are the known values for this field, offered as a picker. The
	// field stays free text — a jump host can be any ssh destination, not only
	// one Omassh happens to know about.
	choices []string

	// list marks a field holding a comma-separated set rather than one value,
	// so picking adds to it instead of replacing it. Opening the picker again
	// is then how a second tag is added.
	list bool

	// suggested marks a value that was filled in for you rather than typed.
	// Typing over it replaces it, the way selected text would: appending to a
	// suggestion silently produces things like "FleetFleet", and with unknown
	// group names being created on save, that makes a group out of a typo.
	suggested bool
}

// form is the modal used to add and edit hosts and groups.
type form struct {
	kind    formKind
	title   string
	editID  string // empty when creating
	fields  []field
	idx     int
	problem string

	// picking is whether the choice list for the current field is open, and
	// pickIdx the highlighted entry. A bool rather than a sentinel index so a
	// form built somewhere new starts closed without having to remember to say
	// so.
	picking bool
	pickIdx int
}

// newSecretField is a masked input for passphrases and passwords.
func newSecretField(label, hint string) field {
	f := newField(label, hint, "")
	f.input.EchoMode = textinput.EchoPassword
	f.input.EchoCharacter = '•'
	return f
}

// withChoices offers a picker of known values on a field.
func withChoices(f field, choices []string) field {
	f.choices = choices
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
	x := &f.fields[f.idx]
	if x.suggested {
		// Only text replaces. Arriving with an arrow key or backspace means
		// you intend to edit what is there, so the value stays.
		if replacesSuggestion(msg) {
			x.input.SetValue("")
		}
		x.suggested = false
	}

	var cmd tea.Cmd
	x.input, cmd = x.input.Update(msg)
	return cmd
}

// replacesSuggestion reports whether a message is the user typing text, as
// opposed to moving around or deleting within the field.
func replacesSuggestion(msg tea.Msg) bool {
	switch m := msg.(type) {
	case tea.KeyPressMsg:
		return m.Text != ""
	case tea.PasteMsg:
		return true
	}
	return false
}

// asSuggestion marks a field's starting value as filled in rather than typed.
// A field that starts empty has nothing to replace.
func asSuggestion(f field) field {
	f.suggested = f.input.Value() != ""
	return f
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

// --- choice picker -----------------------------------------------------

// hasChoices reports whether the focused field offers a picker.
func (f *form) hasChoices() bool { return len(f.fields[f.idx].choices) > 0 }

// openPicker starts on whatever the field already holds, so reopening it lands
// on the current value rather than at the top of the list.
func (f *form) openPicker() {
	cur := strings.TrimSpace(f.fields[f.idx].input.Value())
	f.picking, f.pickIdx = true, 0
	for i, c := range f.fields[f.idx].choices {
		if c == cur {
			f.pickIdx = i
			break
		}
	}
}

func (f *form) closePicker() { f.picking = false }

func (f *form) movePicker(d int) {
	n := len(f.fields[f.idx].choices)
	f.pickIdx = (f.pickIdx + d + n) % n
}

// choosePicked commits the picker.
//
// For a set there is nothing to commit — space has already toggled each entry
// — so this just finishes. A single-value field takes the highlighted choice
// and closes, since there is nothing more to say.
func (f *form) choosePicked() {
	x := &f.fields[f.idx]
	x.suggested = false // now a deliberate value

	if x.list {
		f.closePicker()
		return
	}
	c := x.choices[f.pickIdx]
	if c == noChoice {
		c = "" // the empty choice clears the field
	}
	x.input.SetValue(c)
	x.input.CursorEnd()
	f.closePicker()
}

// togglePicked adds or removes the highlighted entry, for a field holding a
// set. The list stays open, so the whole set is chosen in one pass.
func (f *form) togglePicked() {
	x := &f.fields[f.idx]
	if !x.list {
		return
	}
	x.suggested = false
	x.input.SetValue(toggleInList(x.input.Value(), x.choices[f.pickIdx]))
	x.input.CursorEnd()
}

// inList reports whether a comma-separated field already holds an entry.
func inList(current, want string) bool {
	return slices.Contains(splitTags(current), want)
}

// toggleInList adds an entry to a comma-separated field, or removes it if it
// is already there, so one key both selects and deselects.
func toggleInList(current, entry string) string {
	items := splitTags(current)
	if i := slices.Index(items, entry); i >= 0 {
		items = slices.Delete(items, i, i+1)
	} else {
		items = append(items, entry)
	}
	return strings.Join(items, ", ")
}

// asList marks a field as holding a set, so its picker adds rather than
// replaces.
func asList(f field) field {
	f.list = true
	return f
}

// noChoice is the list entry that clears the field.
const noChoice = "— none —"

// currentChoice is the highlighted entry, for tests and for rendering.
func (f *form) currentChoice() string {
	c := f.fields[f.idx].choices
	if !f.picking || len(c) == 0 {
		return ""
	}
	return c[f.pickIdx]
}
