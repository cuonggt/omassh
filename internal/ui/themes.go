package ui

import (
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

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
	for n := range m.opts.Themes {
		if !seen[n] {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	return names
}

// palette resolves a name the same way the config file does.
func (m Model) palette(name string) theme.Palette {
	if p, ok := m.opts.Themes[name]; ok {
		return p
	}
	return theme.Builtin[name]
}

func (m Model) handleThemeKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	p := m.themes
	key := msg.String()
	switch {
	case m.keys.Lookup(key) == keymap.Down:
		p.idx = (p.idx + 1) % len(p.names)
		theme.Apply(m.palette(p.names[p.idx]))
	case m.keys.Lookup(key) == keymap.Up:
		p.idx = (p.idx - 1 + len(p.names)) % len(p.names)
		theme.Apply(m.palette(p.names[p.idx]))
	case key == "enter":
		return m.keepTheme()
	case key == "esc", key == "q":
		// Put back what was showing before, including the case where the
		// config names a palette this list resolved differently.
		theme.Apply(m.palette(p.was))
		m.themes, m.mode = nil, m.returnTo
		m.setStatus("theme unchanged")
	}
	return m, nil
}

func (m Model) keepTheme() (tea.Model, tea.Cmd) {
	name := m.themes.names[m.themes.idx]
	m.themes, m.mode = nil, m.returnTo
	m.opts.Theme = name

	if m.opts.SaveTheme == nil {
		m.setStatus("theme " + name + " for this session")
		return m, nil
	}
	if err := m.opts.SaveTheme(name); err != nil {
		// The colours are already applied, so say what is true: it worked,
		// and it will not survive a restart.
		m.setErr(fmt.Errorf("using %s, but saving it failed: %w", name, err))
		return m, nil
	}
	m.setStatus("theme " + name)
	return m, nil
}

// themeBody lists the palettes, marking the one that is actually configured
// so that looking through the list does not lose track of where you started.
func (m Model) themeBody(w int) string {
	p := m.themes
	rows := make([]string, 0, len(p.names))
	for i, n := range p.names {
		mark := "  "
		if n == p.was {
			mark = "● "
		}
		rows = append(rows, row(mark+n, i == p.idx, w))
	}
	return strings.Join(rows, "\n")
}
