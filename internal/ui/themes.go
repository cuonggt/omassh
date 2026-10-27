package ui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/cuonggt/omassh/internal/keymap"
	"github.com/cuonggt/omassh/internal/ui/theme"
)

// The theme picker recolours the whole interface as the cursor moves, because
// a palette is something you judge by looking at it rather than by reading its
// name. Keeping one writes it to the config file, so the next start looks like
// this one; leaving puts back whatever was there before.

type themePicker struct {
	names []string
	idx   int
	// was is the theme in effect when the picker opened, to restore on esc
	// and to mark in the list.
	was string
}

func (m Model) openThemePicker() (tea.Model, tea.Cmd) {
	names := m.themeNames()
	if len(names) == 0 {
		return m, nil // impossible while there are built-ins, but not a crash
	}
	p := &themePicker{names: names, was: m.opts.Theme}
	for i, n := range names {
		if n == p.was {
			p.idx = i
			break
		}
	}
	m.themes = p
	m.returnTo = m.mode
	m.mode = modeTheme
	m.setStatus("theme — ↑↓ to look, ↵ to keep, esc to put it back")
	return m, nil
}

// themeNames is every palette that can be picked: the built-ins plus anything
// the config file defines. A custom palette sharing a built-in's name is one
// entry, and wins, exactly as it does at startup.
func (m Model) themeNames() []string {
	seen := map[string]bool{}
	var names []string
	for _, n := range theme.BuiltinNames() {
		seen[n], names = true, append(names, n)
	}
	// Omarchy's own current theme, where there is an Omarchy to read it from.
	// Offered only there, because a name in this list is a thing you can look
	// at: one that previewed as nothing on every machine but a few would be a
	// row that does not answer to the key that moved onto it.
	if theme.OmarchyAvailable() {
		seen[theme.OmarchyName], names = true, append(names, theme.OmarchyName)
	}
	for n := range m.opts.Themes {
		if !seen[n] {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return names
}

// palette resolves a name the same way the config file does.
//
// A name that will not resolve previews as the empty palette, which Apply
// draws in the terminal's own colours. That is what an unknown name has always
// done here, and it is the right answer for omarchy on a machine that has lost
// its theme file between the list being built and the cursor reaching it.
func (m Model) palette(name string) theme.Palette {
	if p, ok := m.opts.Themes[name]; ok {
		return p
	}
	p, err := theme.Resolve(name)
	if err != nil {
		return theme.Palette{}
	}
	return p
}

// omarchyWatch is how often Omassh asks whether Omarchy's theme has changed.
//
// Two seconds, like the forwards list, and for the same reason: it is long
// enough to cost nothing and short enough that nobody catches Omassh still
// wearing the old colours. The watch runs only while Omarchy's own theme is
// the one in effect — a timer waking the program on every machine, for a
// setting that means nothing on most of them, is a cost with nothing to show
// for it.
const omarchyWatch = 2 * time.Second

type omarchyTickMsg struct{}

func omarchyTick() tea.Cmd {
	return tea.Tick(omarchyWatch, func(time.Time) tea.Msg { return omarchyTickMsg{} })
}

// watchOmarchy starts following Omarchy's theme, if that is the theme in
// effect and nothing is following it already.
//
// The flag is what keeps there being exactly one of these. Picking omarchy,
// then something else, then omarchy again inside one tick would otherwise
// leave the first tick alive beside the one the second pick started, and the
// pair of them would go on waking the program for as long as it ran.
func (m *Model) watchOmarchy() tea.Cmd {
	if m.watchingOmarchy || m.opts.Theme != theme.OmarchyName {
		return nil
	}
	m.watchingOmarchy = true
	m.omarchyStamp = theme.OmarchyStamp()
	return omarchyTick()
}

// handleOmarchyTick repaints if Omarchy has been set to another theme since
// the last look.
func (m Model) handleOmarchyTick() (tea.Model, tea.Cmd) {
	// The theme in effect can have become one of ours since the watch
	// started, and then there is nothing left to follow.
	if m.opts.Theme != theme.OmarchyName {
		m.watchingOmarchy = false
		return m, nil
	}

	stamp := theme.OmarchyStamp()
	if stamp == m.omarchyStamp {
		return m, omarchyTick()
	}
	m.omarchyStamp = stamp

	// Recorded whether or not the colours could be read. A theme file that has
	// gone — Omarchy removed while Omassh runs — leaves the palette as it is
	// rather than throwing the interface back to the terminal's colours, and
	// the stamp changes again if it comes back.
	if p, err := theme.Resolve(theme.OmarchyName); err == nil {
		m.applyTheme(p)
	}
	return m, omarchyTick()
}

// applyTheme recolours the interface. Apply rebuilds the styles the theme
// package keeps; the inputs are what hold colours of their own, and they are
// handed the new ones here.
//
// The filter lives through a trip to the picker, and so does a form now that a
// theme can change underneath one: a form's fields take their colours when it
// opens, which was the whole story while a theme only ever changed in the
// picker, and is not one while Omarchy can change it from outside.
func (m *Model) applyTheme(p theme.Palette) {
	theme.Apply(p)
	m.filter.SetStyles(inputStyles())
	m.find.SetStyles(inputStyles())
	if m.form != nil {
		for i := range m.form.fields {
			m.form.fields[i].input.SetStyles(inputStyles())
		}
	}
}

// inputStyles colours a text input from the palette.
//
// bubbles gives an input colours of its own that no theme reached: the
// terminal's white for every field not being typed in, and for its cursor, and
// a fixed grey for a hint. On a light terminal white is the colour of the
// page, so a form's values were next to invisible everywhere but under the
// cursor — in the default palette, whose point is suiting that background.
func inputStyles() textinput.Styles {
	s := textinput.DefaultDarkStyles()
	for _, st := range []*textinput.StyleState{&s.Focused, &s.Blurred} {
		st.Text, st.Prompt = theme.Normal, theme.Normal
		st.Placeholder, st.Suggestion = theme.Dim, theme.Dim
	}
	s.Cursor.Color = theme.Foreground
	return s
}

func (m Model) handleThemeKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	p := m.themes
	key := msg.String()
	switch {
	case m.keys.Lookup(key) == keymap.Down:
		p.idx = (p.idx + 1) % len(p.names)
		m.applyTheme(m.palette(p.names[p.idx]))
	case m.keys.Lookup(key) == keymap.Up:
		p.idx = (p.idx - 1 + len(p.names)) % len(p.names)
		m.applyTheme(m.palette(p.names[p.idx]))
	case key == "enter":
		return m.keepTheme()
	case key == "esc", key == "q":
		// Put back what was showing before, including the case where the
		// config names a palette this list resolved differently.
		m.applyTheme(m.palette(p.was))
		m.themes, m.mode = nil, m.returnTo
		m.setStatus("theme unchanged")
	}
	return m, nil
}

func (m Model) keepTheme() (tea.Model, tea.Cmd) {
	name := m.themes.names[m.themes.idx]
	m.themes, m.mode = nil, m.returnTo
	m.opts.Theme = name

	// Picking Omarchy's own theme starts following it, as naming it in the
	// config file does. Picking away from it stops at the next tick, which
	// finds the theme is no longer this one.
	watch := m.watchOmarchy()

	if m.opts.SaveTheme == nil {
		m.setStatus("theme " + name + " for this session")
		return m, watch
	}
	if err := m.opts.SaveTheme(name); err != nil {
		// The colours are already applied, so say what is true: it worked,
		// and it will not survive a restart.
		m.setErr(fmt.Errorf("using %s, but saving it failed: %w", name, err))
		return m, watch
	}
	m.setStatus("theme " + name)
	return m, watch
}

// themeWidth is how wide the picker has to be for what it is listing.
//
// The built-in names all fit the 34 it has always been, and that width is
// deliberate: the point of the picker is the interface showing around it. A
// palette from the config file is named by hand, though, and one long enough
// to be cut lost the end of itself in the only place it is written — so two
// that differ late in the name drew as the same row, in a list whose whole job
// is telling them apart.
func (m Model) themeWidth() int {
	w := 34
	for _, n := range m.themes.names {
		// One cell of cursor, two of marker, four of border and padding.
		w = max(w, ansi.StringWidth(n)+7)
	}
	return w
}

// themeMaxRows caps the picker's height. The point of the picker is the
// recoloured interface around it, so it is not allowed to grow until it covers
// the thing being previewed: there are twenty-odd themes, and a list of all of
// them filled a terminal from top to bottom and left nothing to look at.
const themeMaxRows = 12

// themeListRows is how many palettes fit inside the picker's border.
func (m Model) themeListRows(content int) int {
	return clamp(themeMaxRows, 1, max(content-2, 1))
}

// themeBody lists the palettes, marking the one that is actually configured
// so that looking through the list does not lose track of where you started.
// That mark is a separate thing from the cursor, and says so: one is the
// theme in effect and the other is the one being looked at.
//
// Windowed, like every other list here. It drew all of them, which was fine
// while there were five built in and is not now there are twenty — and was
// never fine for a config file with a dozen palettes of its own, where the
// list ran out of the bottom of the dialog.
func (m Model) themeBody(w, content int) string {
	p := m.themes
	start, end := listWindow(p.idx, len(p.names), m.themeListRows(content))
	rows := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		mark := "  "
		if p.names[i] == p.was {
			mark = "● "
		}
		rows = append(rows, row(mark+p.names[i], cursorState(i == p.idx, true), w))
	}
	return strings.Join(rows, "\n")
}
