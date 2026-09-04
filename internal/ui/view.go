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
	if m.mode == modeBrowse {
		v.Cursor = m.sessionCursor()
	}
	return v
}

func (m Model) render() string {
	if m.w == 0 || m.h == 0 {
		return "" // first frame, before the size message arrives
	}

	// Every view sits below the tab bar, so the geometry is the same whatever
	// is being shown.
	content := m.h - statusHeight - tabBarHeight
	bar := m.tabBar()

	switch m.mode {
	case modeHelp:
		return bar + "\n" + box("Help", true, m.w, content, m.helpBody()) + "\n" + m.statusBar()
	case modeForm:
		return bar + "\n" + box(m.form.title, true, m.w, content, m.form.render(m.w-4)) + "\n" + m.statusBar()
	case modeConfirm:
		return bar + "\n" + box("Confirm", true, m.w, content, m.confirmBody()) + "\n" + m.statusBar()
	case modeIdentities:
		return bar + "\n" + box("Credentials", true, m.w, content, m.identitiesBody()) + "\n" + m.statusBar()
	case modeSFTP:
		return bar + "\n" + m.sftpView(content) + "\n" + m.statusBar()
	case modeSnippets:
		return bar + "\n" + box("Snippets", true, m.w, content, m.snippetsBody(m.w-4)) + "\n" + m.statusBar()
	case modeResults:
		return bar + "\n" + m.resultsView(content) + "\n" + m.statusBar()
	}

	// A session tab shows its session; the first tab is the host browser.
	if m.activeIsSession() {
		return bar + "\n" + m.sessionView(content) + "\n" + m.statusBar()
	}

	side := clamp(sidebarWidth, 20, m.w/2)
	main := m.w - side

	// Groups and Forwards take what they need; Hosts gets the rest, since it
	// is the list you actually scroll.
	groupsH := clamp(len(m.d.tree)+2, 4, content/3)
	fwdH := clamp(len(m.visibleForwards())+2, 4, content/3)
	hostsH := content - groupsH - fwdH
	if hostsH < 4 {
		hostsH, fwdH = 4, max(content-groupsH-4, 3)
	}

	hostsTitle := "Hosts"
	if m.filtering() || m.mode == modeFilter {
		hostsTitle = "Search"
	}
	fwdTitle := "Forwards"
	if n := m.sup.Count(); n > 0 {
		fwdTitle = fmt.Sprintf("Forwards · %d up", n)
	}

	sidebar := lipgloss.JoinVertical(lipgloss.Left,
		box("Groups", m.focus == panelGroups && m.mode == modeBrowse, side, groupsH, m.groupsBody(side-4)),
		box(hostsTitle, m.focus == panelHosts || m.mode == modeFilter, side, hostsH, m.hostsBody(side-4)),
		box(fwdTitle, m.focus == panelForwards && m.mode == modeBrowse, side, fwdH, m.forwardsBody(side-4)),
	)

	title, detail := m.detailBody()
	if m.focus == panelForwards {
		title, detail = m.forwardDetail()
	}
	body := lipgloss.JoinHorizontal(lipgloss.Top, sidebar, box(title, false, main, content, detail))
	return bar + "\n" + body + "\n" + m.statusBar()
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
		cfg := h.Source == store.SourceSSHConfig
		// An open tab is the more useful fact, and unlike d.live it is always
		// current, so it wins when a host is both open and has a session.
		badge, badgeColour := "", theme.Yellow
		if n := m.tabForHost(h); n > 0 {
			badge, badgeColour = fmt.Sprintf("●%d", n), theme.Green
		} else if m.d.hasSession(h) {
			badge = "●"
		}
		selected := i == m.hostIdx && (m.focus == panelHosts || m.mode == modeFilter)

		if selected {
			text := mark + " " + label
			if badge != "" {
				text += " " + badge
			}
			if cfg {
				text += "  cfg"
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
		if cfg {
			line += theme.Fg(theme.Magenta).Render("  cfg")
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
		detailField("source", r.Source.String(), ""),
	}
	// The user is already visible in the ssh line; call it out separately only
	// when it was inherited, so the provenance is not invisible.
	if r.UserFrom != "" {
		lines = append(lines, detailField("user", r.User, r.UserFrom))
	}
	if r.Credential.ID != "" {
		state := "no key"
		if info, ok := m.d.keyInfo[r.Credential.ID]; ok {
			state = info.Type
			switch {
			case m.agentKeys[info.Fingerprint]:
				state += ", in agent"
			case info.Encrypted && r.Credential.HasSecret:
				state += ", locked — K then u to unlock"
			case info.Encrypted:
				state += ", locked, no secret stored"
			}
		}
		lines = append(lines, detailField("cred", r.Credential.Name+"  "+state, ""))
	}
	if h.Note != "" {
		lines = append(lines, detailField("note", h.Note, ""))
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
	if h.Source == store.SourceSSHConfig {
		lines = append(lines, "",
			theme.Dim.Render("  read-only — defined in ~/.ssh/config; press ")+
				theme.Key.Render("i")+theme.Dim.Render(" to import a copy"))
	}
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
			{m.keys.Key(keymap.Down) + " / ↓, " + m.keys.Key(keymap.Up) + " / ↑", "move within the focused panel"},
			{m.keys.Key(keymap.NextPanel) + ", 1-3", "switch panel: groups, hosts, forwards"},
			{m.keys.Key(keymap.Search), "fuzzy search every host by name, address or tag"},
			{"esc", "clear the search"},
		}},
		{"Act", [][2]string{
			{m.keys.Key(keymap.Connect), "connect — the session opens in a new tab"},
			{m.keys.Key(keymap.Handoff), "hand the whole terminal to ssh instead (highest fidelity)"},
			{m.keys.Key(keymap.NewItem), "new host, or new group when Groups is focused"},
			{m.keys.Key(keymap.Edit), "edit the selection"},
			{m.keys.Key(keymap.Delete), "delete the selection"},
			{m.keys.Key(keymap.Import), "import an ssh_config host so it can be edited"},
			{m.keys.Key(keymap.Probe), "probe reachability of the hosts in this group"},
			{m.keys.Key(keymap.Reload), "reload the store and re-read ~/.ssh/config"},
			{m.keys.Key(keymap.Redraw), "redraw, if the terminal cleared the screen underneath"},
			{m.keys.Key(keymap.Credentials), "credentials: keys, stored secrets and the ssh-agent"},
			{m.keys.Key(keymap.SFTP), "sftp: browse and transfer files on the selected host"},
			{m.keys.Key(keymap.Snippets), "snippets: saved commands, run here or across a group"},
		}},
		{"Tabs (" + prefixKey + " prefix)", [][2]string{
			{"prefix n / p", "next and previous tab"},
			{"prefix 1-9", "jump straight to a tab"},
			{"prefix w", "back to the host list; the session keeps running"},
			{"prefix x", "close the tab — the session keeps running, reconnect to reattach"},
			{"prefix X", "end the session for good"},
			{"", "green ●2 beside a host means it is open in tab 2;"},
			{"", "a yellow ● means a detached session is waiting"},
		}},
		{"Inside a session", [][2]string{
			{"prefix k / j", "scroll back and forward a page through the output"},
			{"prefix G", "return to the live view"},
			{"prefix r", "redraw the screen"},
			{"prefix " + prefixKey, "send a literal " + prefixKey + " to the remote"},
			{"", "every other key goes to the remote, ctrl+c included,"},
			{"", "so the prefix is the way back out"},
		}},
		{"Snippets (" + m.keys.Key(keymap.Snippets) + ")", [][2]string{
			{"↵", "run on the selected host"},
			{"f", "fan out across every host in the selected group"},
			{"", "a fan-out always confirms; a command matching a"},
			{"", "destructive pattern makes you type the host count"},
			{"ctrl+d/u", "scroll one host's output in the results view"},
		}},
		{"SFTP (" + m.keys.Key(keymap.SFTP) + ")", [][2]string{
			{"tab", "switch between the local and remote pane"},
			{"↵ / -", "enter a directory / go up"},
			{"c", "copy the highlighted file to the other pane"},
			{"m / r / M / d", "mkdir / rename / chmod / delete"},
			{"", "OpenSSH performs the connection, so jump hosts,"},
			{"", "certificates and ProxyCommand all apply as usual"},
		}},
		{"Forwards (panel 3)", [][2]string{
			{"↵", "start or stop the selected tunnel"},
			{"n", "add a rule: local (-L), remote (-R) or dynamic SOCKS (-D)"},
			{"", "a dropped tunnel is retried with backoff; tunnels are"},
			{"", "children of omassh and close when it exits"},
		}},
		{"Credentials (" + m.keys.Key(keymap.Credentials) + ")", [][2]string{
			{"n / g", "add a credential, or generate a key and a credential"},
			{"u", "load its key into the ssh-agent, unlocking with the"},
			{"", "stored passphrase — no prompt, nothing typed"},
			{"x", "remove its key from the agent"},
			{"", "bind one to a host or group by naming it in Identity"},
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
	case modeIdentities:
		hints = hint("n/g", "new/generate") + sep() + hint("u", "unlock") +
			sep() + hint("e/d", "edit/delete") + sep() + hint("esc", "back")
	case modeSFTP:
		hints = hint("tab", "pane") + sep() + hint("c", "copy") +
			sep() + hint("↵/-", "in/up") + sep() + hint("esc", "close")
	case modeSnippets:
		hints = hint("↵", "run here") + sep() + hint("f", "fan out") +
			sep() + hint("n/e/d", "new/edit/delete") + sep() + hint("esc", "back")
	case modeResults:
		hints = hint("j/k", "host") + sep() + hint("ctrl+d/u", "scroll") + sep() + hint("esc", "back")
	default:
		switch {
		case m.prefixArmed:
			hints = theme.Fg(theme.Yellow).Render("prefix: ") +
				hint("n/p", "tab") + sep() + hint("w", "hosts") + sep() +
				hint("x/X", "close/end") + sep() + hint("k/j", "scroll") +
				sep() + hint("G", "live")
		case m.activeIsSession():
			hints = hint(prefixKey+" n/p", "switch tab") + sep() +
				hint(prefixKey+" w", "hosts") + sep() +
				theme.Dim.Render("every other key goes to the remote")
		case m.focus == panelForwards:
			hints = hint("↵", "start/stop") + sep() + hint("n/e/d", "new/edit/delete") +
				sep() + hint("1-3", "panel") + sep() + hint("?", "help") + sep() + hint("q", "quit")
		default:
			hints = hint(m.keys.Key(keymap.Connect), "connect") + sep() +
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
