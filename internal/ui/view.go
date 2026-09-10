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
	"github.com/cuonggt/omassh/internal/store"
	"github.com/cuonggt/omassh/internal/ui/theme"
)

func (m Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.WindowTitle = "omassh"
	// Clicking selects a group, a host or the session pane. Reporting is on
	// only where there is a list to click: a focused session needs the
	// terminal's own selection more than it needs click-to-focus, and every
	// terminal reverts to selecting text when the application is not asking
	// for the mouse.
	switch {
	case m.mode == modeBrowse && m.focus != panelSession, m.mode == modeSFTP:
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

// dialogWidth is how wide a modal is drawn.
//
// Wide enough for a path or a host address, but never edge to edge: the list
// showing through around it is what makes it read as a dialog. The upper
// bound is the frame itself, so a narrow terminal shrinks the dialog rather
// than letting it overhang.
func (m Model) dialogWidth() int { return clamp(64, 20, m.w-4) }

// pathTail keeps the end of a path when the whole of it will not fit.
//
// Which directory this is lives at the end of it. Cut from the right, which is
// what a title does to anything too long, every directory in a deep tree drew
// the same leading run of the path — so the title said where the tree began
// and never where you actually were.
func pathTail(p string, w int) string {
	if w <= 0 {
		return ""
	}
	if n := ansi.StringWidth(p); n > w {
		return ansi.TruncateLeft(p, n-w+1, "…")
	}
	return p
}

// dialog is the modal drawn over the browser, if one is open.
func (m Model) dialog(content int) (string, bool) {
	w := m.dialogWidth()

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
		body := m.confirmBody(w - 4)
		return box("Confirm", true, w, dialogHeight(body, content), body), true
	case modeForwards:
		body := m.forwardsBody(w-4, content)
		// The count says there is more than fits, since once the list is
		// windowed the border alone no longer shows it.
		title := "Forwards on " + m.forwardHost.Name +
			listPosition(m.forwardIdx, len(m.d.forwardsFor(m.forwardHost.ID)), m.forwardListRows(content))
		return box(title, true, w, dialogHeight(body, content), body), true
	case modeTheme:
		// Narrower than the rest: a dialog this size leaves more of the
		// coloured interface showing behind it, which is the thing actually
		// being previewed. It widens for a name that does not fit, since the
		// built-ins are one word but a palette from the config file is named
		// by whoever wrote it.
		tw := clamp(m.themeWidth(), 20, m.w-4)
		body := m.themeBody(tw - 4)
		return box("Theme", true, tw, dialogHeight(body, content), body), true
	}
	return "", false
}

// dialogHeight sizes a dialog to its content, capped so it always fits.
func dialogHeight(body string, content int) int {
	return clamp(strings.Count(body, "\n")+3, 3, content)
}

func (m Model) browserBody(content int) string {
	side := m.sidebar()
	main := m.w - side

	// Groups takes what it needs; Hosts gets the rest, since it is the list
	// you actually scroll.
	groupsH := clamp(len(m.d.tree)+2, 4, content/3)
	hostsH := content - groupsH

	hostsTitle := "Hosts"
	if m.filtering() || m.mode == modeFilter {
		hostsTitle = "Search"
	}
	groupRows, hostRows := max(groupsH-2, 1), max(hostsH-2, 1)
	hostsTitle += listPosition(m.hostIdx, len(m.visibleHosts()), hostRows-m.searchLines())

	sidebar := lipgloss.JoinVertical(lipgloss.Left,
		box("Groups"+listPosition(m.groupIdx, len(m.d.tree), groupRows),
			m.focus == panelGroups && m.mode == modeBrowse, side, groupsH, m.groupsBody(side-4, groupRows)),
		box(hostsTitle, m.focus == panelHosts || m.mode == modeFilter,
			side, hostsH, m.hostsBody(side-4, hostRows)),
	)

	title, detail := m.detailBody()
	// A live session takes the main pane; the sidebar stays usable beside it.
	mainFocused := false
	if m.attached != nil {
		title, detail = m.sessionTitle(main), m.attached.Render()
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
		title = "pick  ↑↓  space toggle  ↵ done"
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

// groupLabel indents a group by its depth, without letting the indent crowd
// out the name it is indenting.
//
// Two spaces a level filled a narrow sidebar by about the twelfth, and every
// group past that drew as an ellipsis — present and selectable, but with
// nothing to tell it from its neighbours. The indent stops growing at half
// the field; a group indented that far is marked, since past the cap two
// levels are drawn alike and the tree's order is what places them.
func (m Model) groupLabel(g store.GroupNode, field int) string {
	const perLevel = 2
	full := g.Depth * perLevel
	if capped := max(field/2, 0); full > capped {
		return strings.Repeat(" ", max(capped-1, 0)) + "·" + g.Name
	}
	return strings.Repeat(" ", full) + g.Name
}

func (m Model) groupsBody(w, rows int) string {
	if len(m.d.tree) == 0 {
		return theme.Dim.Render("no groups yet — n to add")
	}
	start, end := listWindow(m.groupIdx, len(m.d.tree), rows)
	lines := make([]string, 0, end-start)
	for i, g := range m.d.tree[start:end] {
		i += start
		n := len(m.d.hostsIn(g.ID))
		text := fmt.Sprintf("%s %d", pad(m.groupLabel(g, max(w-4, 1)), max(w-4, 1)), n)

		selected := i == m.groupIdx && m.focus == panelGroups && !m.filtering()
		if isSynthetic(g.ID) && !selected {
			lines = append(lines, theme.Fg(theme.Magenta).Render(ansi.Truncate(text, w, "…")))
			continue
		}
		lines = append(lines, row(text, selected, w))
	}
	return strings.Join(lines, "\n")
}

func (m Model) hostsBody(w, rows int) string {
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

	start, end := listWindow(m.hostIdx, len(hosts), rows-len(lines))
	for i, h := range hosts[start:end] {
		i += start
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
		// A tunnel is the one thing here with nothing to look at, so the list
		// is where it has to be visible: it is running whether or not anyone
		// is connected, and long after the window that started it has gone.
		tunnel := ""
		if m.d.forwardsUp(h.ID) > 0 {
			tunnel = "▶"
		}
		selected := i == m.hostIdx && (m.focus == panelHosts || m.mode == modeFilter)

		// The marks come off the width first. They are fixed and they are the
		// only thing the row says that is not in the name — a session waiting,
		// a tunnel up — so truncating the composed line dropped them off the
		// end, and a long name silently hid the very thing they were there to
		// show. The name gives way instead.
		marks := ""
		if badge != "" {
			marks += " " + badge
		}
		if tunnel != "" {
			marks += " " + tunnel
		}
		keep := max(w-ansi.StringWidth(marks), 0)

		if selected {
			lines = append(lines, row(ansi.Truncate(mark+" "+label, keep, "…")+marks, true, w))
			continue
		}

		line := theme.Fg(markColour).Render(mark) + theme.Normal.Render(" "+h.Name)
		if m.filtering() {
			if g := m.d.groupName(h.GroupID); g != "" {
				line += theme.Dim.Render("  " + g)
			}
		}
		line = ansi.Truncate(line, keep, "…")
		if badge != "" {
			line += theme.Fg(badgeColour).Render(" " + badge)
		}
		if tunnel != "" {
			line += theme.Fg(theme.Green).Render(" " + tunnel)
		}
		lines = append(lines, line)
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
	// Forwards belong to the host and run without it being connected, so the
	// pane that describes a host has to describe them too — otherwise the only
	// way to learn a tunnel is up is to go looking for it.
	if fs := m.d.forwardsFor(h.ID); len(fs) > 0 {
		lines = append(lines, "", theme.Dim.Render("  forwards"))
		const shown = 4
		for i, f := range fs {
			if i == shown && len(fs) > shown+1 {
				lines = append(lines, theme.Dim.Render(
					fmt.Sprintf("    +%d more — %s to see them", len(fs)-shown, m.keys.Key(keymap.Forward))))
				break
			}
			st, known := m.d.forwardStatus(f)
			mark, col := forwardMarker(st, known, forwardStale(r.Host, f, st))
			// The kind, in the same aligned column the forwards view uses: a
			// local and a remote rule over the same two ports are opposite
			// directions, and without it they draw as the same line twice.
			lines = append(lines, "    "+theme.Fg(col).Render(mark)+
				theme.Dim.Render(" "+pad(string(f.Kind), 7))+theme.Normal.Render(" "+f.Route()))
		}
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

func (m Model) confirmBody(w int) string {
	// Wrapped rather than cut. This is the sentence that says what a
	// destructive action is about to do, and what it says last is the part
	// that reassures — "nothing is deleted", "session history is kept".
	// Truncating at the border took exactly that off, and left the group
	// things were moving to half spelled, so the dialog stopped answering
	// either of the questions it exists to answer.
	return "\n  " + wrapIndented(m.confirm.prompt, w, theme.Fg(theme.TextBrt).Bold(true)) +
		"\n\n  " + wrapIndented(m.confirm.detail, w, theme.Dim) +
		"\n\n  " + hint("y", "yes") + theme.Dim.Render("  ·  ") + hint("n", "no")
}

// wrapIndented wraps text to the width inside a two-space indent, styling each
// line and indenting the ones after the first to line up under it.
//
// ansi.Wrap rather than Wordwrap: a host or group name is one long token with
// no spaces to break at, and Wordwrap would let it run past the border to be
// truncated there — which is the thing being fixed.
func wrapIndented(s string, w int, st lipgloss.Style) string {
	lines := strings.Split(ansi.Wrap(s, max(w-2, 1), ""), "\n")
	for i, l := range lines {
		lines[i] = st.Render(l)
	}
	return strings.Join(lines, "\n  ")
}

// helpRows is how many lines of the cheatsheet fit inside its border.
func (m Model) helpRows() int { return max(m.h-statusHeight-2, 1) }

// helpOverflow is the furthest the cheatsheet can usefully scroll, and zero
// when the whole of it already fits.
func (m Model) helpOverflow() int { return max(len(m.helpLines())-m.helpRows(), 0) }

// helpBody shows the part of the cheatsheet that has been scrolled to.
func (m Model) helpBody() string {
	lines := m.helpLines()
	start := clamp(m.helpScroll, 0, max(len(lines)-m.helpRows(), 0))
	return strings.Join(lines[start:min(start+m.helpRows(), len(lines))], "\n")
}

func (m Model) helpLines() []string {
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
			{m.keys.Key(keymap.Forward), "port forwarding for the selected host"},
			{m.keys.Key(keymap.Theme), "choose a colour theme, previewing as you move"},
		}},
		{"Main-pane session (" + m.keys.Key(keymap.Pane) + ", then the " + prefixKey + " prefix)", [][2]string{
			{"prefix w", "back to the host list; the session keeps running"},
			{"prefix k / j", "scroll back and forward a page through the output"},
			{"prefix G", "return to the live view"},
			{"prefix d", "detach — the session keeps running, " + m.keys.Key(keymap.Pane) + " to reattach"},
			{"prefix X", "end the session for good"},
			{"esc", "when a session has ended, return to the list"},
			{"prefix r", "redraw the screen"},
			{"prefix " + prefixKey, "send a literal " + prefixKey + " to the remote"},
			{m.keys.Key(keymap.Pane), "back to the session, once it is the one connected"},
			{"", "a green ● beside a host means it is connected here;"},
			{"", "a yellow ● means a detached session is waiting"},
			{"", "while the session has focus every other key goes to"},
			{"", "the remote, so the prefix is the way back out — and"},
			{"", "it works from the host list too, so detaching or"},
			{"", "ending one does not mean going back in to do it"},
		}},
		{"Port forwarding (" + m.keys.Key(keymap.Forward) + ")", [][2]string{
			{"↵", "start or stop the highlighted tunnel"},
			{"n / e / d", "new / edit / delete a rule"},
			{m.keys.Key(keymap.Reload), "reload the rules, and re-ask what is still up"},
			{"", "local  binds a port here and carries it out of the host"},
			{"", "remote binds a port on the host and carries it back"},
			{"", "dynamic binds a SOCKS proxy here"},
			{"", "▶ running · ■ stopped · ✖ stopped because it failed,"},
			{"", "and the line beneath says what ssh said"},
			{"", "▷ running, but carrying what it was started with —"},
			{"", "the rule or its host changed under it, and ↵ restarts"},
			{"", "it on what they say now"},
			{"", "a tunnel runs on omassh's own tmux server, so it"},
			{"", "outlives the window that started it — and a green ▶"},
			{"", "beside a host in the list means one is up"},
		}},
		{"SFTP (" + m.keys.Key(keymap.SFTP) + ")", [][2]string{
			{"tab / shift+tab", "switch between the local and remote pane"},
			{"click", "select a file, and focus the pane it is in"},
			{"double click", "enter a directory, as ↵ does"},
			{"↵ / -", "enter a directory / go up"},
			{"c", "copy the highlighted file to the other pane"},
			{"m / r / M / d", "mkdir / rename / chmod / delete"},
			{"", "OpenSSH performs the connection, so jump hosts,"},
			{"", "certificates and ProxyCommand all apply as usual"},
		}},
		{"Forms", [][2]string{
			{"tab / shift+tab", "next and previous field"},
			{"↓", "pick what you already use: a host's jump host, group"},
			{"", "or tags, and a group's parent or jump host"},
			{"", "Tags is a set: space toggles an entry, ✓ marks the"},
			{"", "ones chosen, and ↵ finishes"},
			{"↵ / esc", "save / cancel"},
			{"", "both stay free text: any ssh destination works as a"},
			{"", "jump host, and an unknown group name creates it"},
		}},
		{"Groups", [][2]string{
			{"", "a group holds the hosts beneath it as well as its own,"},
			{"", "so selecting one shows everything in it and the count"},
			{"", "beside it says the same"},
			{"", "a host inherits user, identity and jump host from its"},
			{"", "group chain; its own values always win, and the detail"},
			{"", "pane marks inherited ones with ← group"},
		}},
		{"Moving to another machine", [][2]string{
			{"", "omassh export > hosts.yaml   the list, as text"},
			{"", "omassh import hosts.yaml     fold one in"},
			{"", "omassh import-ssh-config     take ~/.ssh/config"},
			{"", "records match by name, so the ids each machine mints"},
			{"", "for itself need not agree, and importing a list twice"},
			{"", "changes nothing the second time"},
		}},
	}

	name := "omassh"
	if m.opts.Version != "" {
		name += " " + m.opts.Version
	}
	// The status bar carries "any key back", so the body spends none of its
	// rows repeating it.
	// The key column is as wide as the widest key it has to show, and never
	// narrower than 16 — the width the built-in labels settled at, where
	// "tab / shift+tab" ran straight into its own description at less.
	//
	// It has to be measured rather than fixed because a key comes from the
	// config file: one longer than the column welded itself to the text
	// beside it, on the very line whoever changed the binding would look for.
	col := 16
	for _, s := range sections {
		for _, r := range s.rows {
			col = max(col, ansi.StringWidth(r[0])+1)
		}
	}

	lines := []string{theme.Title.Render(name) + theme.Dim.Render("  keyboard-driven SSH client")}
	for _, s := range sections {
		lines = append(lines, "", theme.Fg(theme.Yellow).Render("  "+s.title))
		for _, r := range s.rows {
			pad := strings.Repeat(" ", max(col-ansi.StringWidth(r[0]), 0))
			lines = append(lines, theme.Key.Render("  "+r[0]+pad)+theme.Normal.Render(r[1]))
		}
	}
	return lines
}

func (m Model) statusBar() string {
	var hints string
	switch m.mode {
	case modeForm:
		hints = hint("tab", "field") + sep() + hint("↵", "save") + sep() + hint("esc", "cancel")
	case modeConfirm:
		hints = hint("y", "confirm") + sep() + hint("n", "cancel")
	case modeTheme:
		hints = hint("↑↓", "preview") + sep() + hint("↵", "keep") + sep() + hint("esc", "cancel")
	case modeFilter:
		hints = hint("↑↓", "select") + sep() + hint("↵", "keep") + sep() + hint("esc", "clear")
	case modeHelp:
		hints = hint("any key", "back")
		if m.helpOverflow() > 0 {
			hints = hint("↑↓", "scroll") + sep() + hints
		}
	case modeSFTP:
		// The file browser keeps its keys on the transfer strip, which has a
		// whole row for them; repeating a shorter version here said the same
		// thing twice and named different keys each time.
	case modeForwards:
		// Likewise: the forwards dialog carries its own keys, and the status
		// is where what just happened to a tunnel is reported.
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
	// The subject only if it fits beside the reason. Cut, the prefix ate the
	// half that says what to do — and named a rule the screen was already
	// showing.
	msg := m.status
	if m.statusCtx != "" {
		if with := m.statusCtx + ": " + msg; ansi.StringWidth(with)+1 <= m.w {
			msg = with
		}
	}
	right := theme.Fg(col).Render(msg) + " "

	// The space between the two is reserved before the hints are measured,
	// not left over after them. Padding the row to full width is not the same
	// as separating its ends: at the width where the hints fitted exactly,
	// "q quit" ran into "27 hosts" and read as one word.
	const minGap = 2

	// When the terminal is too narrow for both, the hints give way: they are
	// a fixed reminder, while the status is the one thing that just changed.
	if ansi.StringWidth(right) >= m.w {
		return ansi.Truncate(right, m.w, "…")
	}
	if keep := m.w - ansi.StringWidth(right) - minGap; ansi.StringWidth(left) > keep {
		left = ansi.Truncate(left, max(keep, 0), "…")
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

// searchLines is how many rows the search box takes from the Hosts panel.
func (m Model) searchLines() int {
	if m.filtering() || m.mode == modeFilter {
		return 2 // the input and the blank line under it
	}
	return 0
}

// listPosition labels a list that does not fit, since once it scrolls the
// borders alone no longer show that there is more below.
func listPosition(idx, n, rows int) string {
	if n <= rows || n == 0 {
		return ""
	}
	return theme.Dim.Render(fmt.Sprintf("  %d/%d", idx+1, n))
}
