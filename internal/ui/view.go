package ui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/cuonggt/omassh/internal/keymap"
	"github.com/cuonggt/omassh/internal/sshx"
	"github.com/cuonggt/omassh/internal/ui/theme"
)

func (m Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.WindowTitle = "omassh"
	// Clicking selects a group, a host or the session pane. Reporting is on
	// only while browsing: a focused session needs the terminal's own
	// selection more than it needs click-to-focus, and every terminal reverts
	// to selecting text when the application is not asking for the mouse.
	if m.mode == modeBrowse && m.focus != panelSession {
		v.MouseMode = tea.MouseModeCellMotion
	}
	if m.mode == modeBrowse {
		v.Cursor = m.sessionCursor()
	}
	return v
}

func (m Model) render() string {
	if m.w == 0 || m.h == 0 {
		return "" // first frame, before the size message arrives
	}

	content := m.h - statusHeight

	// Below this there is not enough room for a bordered box, and drawing one
	// anyway spills past the edges of the terminal. Say so instead.
	if m.w < minWidth || content < minHeight {
		return tooSmall(m.w, m.h)
	}

	// Help and the file browser are whole screens of their own; forms and
	// confirmations are dialogs, and fall through to be drawn over the list.
	switch m.mode {
	case modeHelp:
		return box("Help", true, m.w, content, m.helpBody()) + "\n" + m.statusBar()
	case modeSFTP:
		return m.sftpView(content) + "\n" + m.statusBar()
	}

	body := m.browserBody(content)
	if d, ok := m.dialog(content); ok {
		dw := ansi.StringWidth(strings.Split(d, "\n")[0])
		dh := strings.Count(d, "\n") + 1
		x, y := centre(dw, dh, m.w, content)
		body = overlay(body, d, x, y)
	}
	return body + "\n" + m.statusBar()
}

// dialog is the modal drawn over the browser, if one is open.
func (m Model) dialog(content int) (string, bool) {
	// Wide enough for a path or a host address, but never edge to edge: the
	// list showing through around it is what makes it read as a dialog. The
	// upper bound is the frame itself, so a narrow terminal shrinks the dialog
	// rather than letting it overhang.
	w := clamp(64, 20, m.w-4)

	switch m.mode {
	case modeForm:
		body := m.form.render(w - 4)
		h := dialogHeight(body, content)
		if !m.form.picking {
			return box(m.form.title, true, w, h, body), true
		}
		// The list sits on the field it belongs to, so the choice and the
		// thing being chosen for are visible together. The dialog grows if it
		// has to, rather than letting the list spill past its own border.
		list := m.pickerList(w - 8)
		top := m.form.idx + 3
		h = clamp(max(h, top+strings.Count(list, "\n")+2), 3, content)
		d := box(m.form.title, true, w, h, body)
		return overlay(d, list, 4, top), true
	case modeConfirm:
		body := m.confirmBody()
		return box("Confirm", true, w, dialogHeight(body, content), body), true
	}
	return "", false
}

// dialogHeight sizes a dialog to its content, capped so it always fits.
func dialogHeight(body string, content int) int {
	return clamp(strings.Count(body, "\n")+3, 3, content)
}

func (m Model) browserBody(content int) string {
	side := clamp(sidebarWidth, 20, m.w/2)
	main := m.w - side

	// Groups takes what it needs; Hosts gets the rest, since it is the list
	// you actually scroll.
	groupsH := clamp(len(m.d.tree)+2, 4, content/3)
	hostsH := content - groupsH

	hostsTitle := "Hosts"
	if m.filtering() || m.mode == modeFilter {
		hostsTitle = "Search"
	}

	sidebar := lipgloss.JoinVertical(lipgloss.Left,
		box("Groups", m.focus == panelGroups && m.mode == modeBrowse, side, groupsH, m.groupsBody(side-4)),
		box(hostsTitle, m.focus == panelHosts || m.mode == modeFilter, side, hostsH, m.hostsBody(side-4)),
	)

	title, detail := m.detailBody()
	// A live session takes the main pane; the sidebar stays usable beside it.
	mainFocused := false
	if m.attached != nil {
		title, detail = m.sessionTitle(), m.attached.Render()
		mainFocused = m.focus == panelSession
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, sidebar, box(title, mainFocused, main, content, detail))
}

// pickerList renders the open choice list for the focused field.
func (m Model) pickerList(w int) string {
	f := m.form
	choices := f.fields[f.idx].choices

	// Keep the list short enough to leave the form readable behind it.
	const maxRows = 6
	start := 0
	if f.pickIdx >= maxRows {
		start = f.pickIdx - maxRows + 1
	}
	end := min(start+maxRows, len(choices))

	// A list field shows what is already chosen, so the picker reads as a set
	// rather than as a menu you have to remember your way through.
	x := f.fields[f.idx]
	label := func(c string) string {
		if !x.list || c == noChoice {
			return c
		}
		if inList(x.input.Value(), c) {
			return "✓ " + c
		}
		return "  " + c
	}

	// A row sits inside the list's border (w-4) behind a two-space indent.
	rows := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		rows = append(rows, "  "+row(label(choices[i]), i == f.pickIdx, w-6))
	}
	if end < len(choices) {
		rows = append(rows, "  "+theme.Dim.Render(fmt.Sprintf("  +%d more", len(choices)-end)))
	}

	title := "pick  ↑↓ ↵"
	if x.list {
		title = "pick  ↑↓  ↵ toggle  esc done"
	}
	return box(title, true, w, len(rows)+2, strings.Join(rows, "\n"))
}

// tooSmall fills the frame with a single line, since the real layout cannot
// fit. It still returns exactly h lines of at most w columns, because the
// renderer's contract does not bend for small terminals.
func tooSmall(w, h int) string {
	msg := ansi.Truncate("terminal too small", w, "")
	lines := make([]string, h)
	for i := range lines {
		lines[i] = ""
	}
	lines[h/2] = msg
	return strings.Join(lines, "\n")
}

func (m Model) groupsBody(w int) string {
	if len(m.d.tree) == 0 {
		return theme.Dim.Render("no groups yet — n to add")
	}
	lines := make([]string, 0, len(m.d.tree))
	for i, g := range m.d.tree {
		n := len(m.d.hostsIn(g.ID))
		label := strings.Repeat("  ", g.Depth) + g.Name
		text := fmt.Sprintf("%s %d", pad(label, max(w-4, 1)), n)

		selected := i == m.groupIdx && m.focus == panelGroups && !m.filtering()
		if isSynthetic(g.ID) && !selected {
			lines = append(lines, theme.Fg(theme.Magenta).Render(ansi.Truncate(text, w, "…")))
			continue
		}
		lines = append(lines, row(text, selected, w))
	}
	return strings.Join(lines, "\n")
}

func (m Model) hostsBody(w int) string {
	var lines []string
	if m.filtering() || m.mode == modeFilter {
		lines = append(lines, theme.Fg(theme.Accent).Render("/ ")+m.filter.View(), "")
	}

	hosts := m.visibleHosts()
	if len(hosts) == 0 {
		empty := "no hosts here — n to add"
		if m.filtering() {
			empty = "no matches"
		}
		return strings.Join(append(lines, theme.Dim.Render(empty)), "\n")
	}

	for i, h := range hosts {
		mark, markColour := m.hostMarker(h.StatKey())
		label := h.Name
		if m.filtering() {
			if g := m.d.groupName(h.GroupID); g != "" {
				label += "  " + g
			}
		}

		// Badges must appear whether or not the row is selected. The selection
		// style paints the whole line, so a coloured badge would be half
		// overridden; the selected row gets the same marks unstyled.
		//
		// The live session is the more useful fact, and unlike d.live it is
		// always current, so it wins when a host is both connected and has a
		// session waiting.
		badge, badgeColour := "", theme.Yellow
		if m.attachedTo(h) {
			badge, badgeColour = "●", theme.Green
		} else if m.d.hasSession(h) {
			badge = "●"
		}
		selected := i == m.hostIdx && (m.focus == panelHosts || m.mode == modeFilter)

		if selected {
			text := mark + " " + label
			if badge != "" {
				text += " " + badge
			}
			lines = append(lines, row(text, true, w))
			continue
		}

		line := theme.Fg(markColour).Render(mark) + theme.Normal.Render(" "+h.Name)
		if m.filtering() {
			if g := m.d.groupName(h.GroupID); g != "" {
				line += theme.Dim.Render("  " + g)
			}
		}
		if badge != "" {
			line += theme.Fg(badgeColour).Render(" " + badge)
		}
		lines = append(lines, ansi.Truncate(line, w, "…"))
	}
	return strings.Join(lines, "\n")
}

func (m Model) detailBody() (string, string) {
	h, ok := m.selectedHost()
	if !ok {
		return "Host", "\n" + theme.Dim.Render("  nothing selected")
	}
	r := m.d.resolver.Resolve(h)

	addr := r.Target()
	if r.Port != 0 && r.Port != 22 {
		addr = fmt.Sprintf("%s:%d", addr, r.Port)
	}

	lines := []string{
		"",
		detailField("ssh", addr, ""),
		detailField("key", strOr(r.Identity, "(agent)"), r.IdentityFrom),
		detailField("via", strOr(r.ProxyJump, "—"), r.ProxyJumpFrom),
		detailField("tags", strOr(strings.Join(r.Tags, ", "), "—"), ""),
	}
	// The user is already visible in the ssh line; call it out separately only
	// when it was inherited, so the provenance is not invisible.
	if r.UserFrom != "" {
		lines = append(lines, detailField("user", r.User, r.UserFrom))
	}
	st := m.d.stats[h.StatKey()]
	history := "never connected"
	if st.Count > 0 {
		history = fmt.Sprintf("last %s · %d session%s", relTime(st.LastSeen), st.Count, plural(st.Count))
	}
	lines = append(lines,
		"",
		theme.Dim.Render("  history"),
		theme.Normal.Render("    "+history),
		"",
		theme.Dim.Render("  command"),
		theme.Fg(theme.Green).Render("    ssh "+strings.Join(sshx.Build(r.Host), " ")),
	)
	return h.Name, strings.Join(lines, "\n")
}

func detailField(k, v, from string) string {
	s := theme.Dim.Render(fmt.Sprintf("  %-8s", k)) + theme.Normal.Render(v)
	if from != "" {
		s += theme.Dim.Render("  ← " + from)
	}
	return s
}

func (m Model) confirmBody() string {
	return "\n  " + theme.Fg(theme.TextBrt).Bold(true).Render(m.confirm.prompt) +
		"\n\n  " + theme.Dim.Render(m.confirm.detail) +
		"\n\n  " + hint("y", "yes") + theme.Dim.Render("  ·  ") + hint("n", "no")
}

func (m Model) helpBody() string {
	sections := []struct {
		title string
		rows  [][2]string
	}{
		{"Navigate", [][2]string{
			{"click", "select a group or host, or focus the session pane"},
			{m.keys.Key(keymap.Down) + " / ↓, " + m.keys.Key(keymap.Up) + " / ↑", "move within the focused panel"},
			{m.keys.Key(keymap.NextPanel) + ", 1-2", "switch panel: groups, hosts"},
			{m.keys.Key(keymap.Search), "fuzzy search every host by name, address or tag"},
			{"esc", "clear the search"},
		}},
		{"Act", [][2]string{
			{m.keys.Key(keymap.Connect), "connect — ssh takes the whole terminal, exit returns here"},
			{m.keys.Key(keymap.Pane), "connect in the main pane instead, keeping the host list"},
			{m.keys.Key(keymap.NewItem), "new host, or new group when Groups is focused"},
			{m.keys.Key(keymap.Edit), "edit the selection"},
			{m.keys.Key(keymap.Delete), "delete the selection"},
			{m.keys.Key(keymap.Probe), "probe reachability of the hosts in this group"},
			{m.keys.Key(keymap.Reload), "reload the store from disk"},
			{m.keys.Key(keymap.Redraw), "redraw, if the terminal cleared the screen underneath"},
			{m.keys.Key(keymap.SFTP), "sftp: browse and transfer files on the selected host"},
		}},
		{"Main-pane session (" + m.keys.Key(keymap.Pane) + ", then the " + prefixKey + " prefix)", [][2]string{
			{"prefix w", "back to the host list; the session keeps running"},
			{"prefix k / j", "scroll back and forward a page through the output"},
			{"prefix G", "return to the live view"},
			{"prefix d", "detach — the session keeps running, " + m.keys.Key(keymap.Pane) + " to reattach"},
			{"prefix X", "end the session for good"},
			{"prefix r", "redraw the screen"},
			{"prefix " + prefixKey, "send a literal " + prefixKey + " to the remote"},
			{"", "a green ● beside a host means it is connected here;"},
			{"", "a yellow ● means a detached session is waiting"},
			{"", "while the session has focus every other key goes to"},
			{"", "the remote, so the prefix is the way back out"},
		}},
		{"SFTP (" + m.keys.Key(keymap.SFTP) + ")", [][2]string{
			{"tab", "switch between the local and remote pane"},
			{"↵ / -", "enter a directory / go up"},
			{"c", "copy the highlighted file to the other pane"},
			{"m / r / M / d", "mkdir / rename / chmod / delete"},
			{"", "OpenSSH performs the connection, so jump hosts,"},
			{"", "certificates and ProxyCommand all apply as usual"},
		}},
		{"Forms", [][2]string{
			{"tab / shift+tab", "next and previous field"},
			{"↓", "on Jump host, Group or Tags, pick what you already use"},
			{"", "Tags is a set: ↵ toggles an entry, ✓ marks the ones"},
			{"", "chosen, and esc finishes. The empty choice clears it"},
			{"↵ / esc", "save / cancel"},
			{"", "both stay free text: any ssh destination works as a"},
			{"", "jump host, and an unknown group name creates it"},
		}},
		{"Inheritance", [][2]string{
			{"", "a host inherits user, identity and jump host from its"},
			{"", "group chain; its own values always win, and the detail"},
			{"", "pane marks inherited ones with ← group"},
		}},
	}

	var b strings.Builder
	b.WriteString(theme.Title.Render("omassh") + theme.Dim.Render("  keyboard-driven SSH client") + "\n")
	for _, s := range sections {
		b.WriteString("\n" + theme.Fg(theme.Yellow).Render("  "+s.title) + "\n")
		for _, r := range s.rows {
			b.WriteString(theme.Key.Render(fmt.Sprintf("  %-14s", r[0])) + theme.Normal.Render(r[1]) + "\n")
		}
	}
	b.WriteString("\n" + theme.Dim.Render("  press any key to return"))
	return b.String()
}

func (m Model) statusBar() string {
	var hints string
	switch m.mode {
	case modeForm:
		hints = hint("tab", "field") + sep() + hint("↵", "save") + sep() + hint("esc", "cancel")
	case modeConfirm:
		hints = hint("y", "confirm") + sep() + hint("n", "cancel")
	case modeFilter:
		hints = hint("↑↓", "select") + sep() + hint("↵", "keep") + sep() + hint("esc", "clear")
	case modeHelp:
		hints = hint("any key", "back")
	case modeSFTP:
		hints = hint("tab", "pane") + sep() + hint("c", "copy") +
			sep() + hint("↵/-", "in/up") + sep() + hint("esc", "close")
	default:
		switch {
		case m.prefixArmed:
			hints = theme.Fg(theme.Yellow).Render("prefix: ") +
				hint("w", "host list") + sep() + hint("d", "detach") + sep() +
				hint("X", "end") + sep() + hint("k/j", "scroll") +
				sep() + hint("G", "live")
		case m.focus == panelSession:
			hints = hint(prefixKey+" w", "host list") + sep() +
				hint(prefixKey+" d", "detach") + sep() +
				theme.Dim.Render("every other key goes to the remote")
		default:
			hints = hint(m.keys.Key(keymap.Connect), "connect") + sep() +
				hint(m.keys.Key(keymap.Pane), "in pane") + sep() +
				hint(m.keys.Key(keymap.Search), "search") + sep() +
				hint(m.keys.Key(keymap.Probe), "probe") + sep() +
				hint("n/e/d", "new/edit/delete") + sep() +
				hint(m.keys.Key(keymap.Help), "help") + sep() +
				hint(m.keys.Key(keymap.Quit), "quit")
		}
	}

	left := " " + hints
	col := theme.Yellow
	if m.failed {
		col = theme.Red
	}
	right := theme.Fg(col).Render(m.status) + " "

	// When the terminal is too narrow for both, the hints give way: they are
	// a fixed reminder, while the status is the one thing that just changed.
	if ansi.StringWidth(right) >= m.w {
		return ansi.Truncate(right, m.w, "…")
	}
	if keep := m.w - ansi.StringWidth(right); ansi.StringWidth(left) > keep {
		left = ansi.Truncate(left, max(keep-1, 0), "…")
	}
	gap := max(m.w-ansi.StringWidth(left)-ansi.StringWidth(right), 0)
	return left + strings.Repeat(" ", gap) + right
}

func sep() string { return theme.Dim.Render("  ·  ") }

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func relTime(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
