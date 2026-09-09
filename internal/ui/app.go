// Package ui implements Omassh's terminal interface.
package ui

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/sahilm/fuzzy"

	"github.com/cuonggt/omassh/internal/keymap"
	"github.com/cuonggt/omassh/internal/probe"
	"github.com/cuonggt/omassh/internal/sftpx"
	"github.com/cuonggt/omassh/internal/sshx"
	"github.com/cuonggt/omassh/internal/store"
	"github.com/cuonggt/omassh/internal/term"
	"github.com/cuonggt/omassh/internal/ui/theme"
)

type panel int

const (
	panelGroups panel = iota
	panelHosts
	panelSession
	numPanels
)

type mode int

const (
	modeBrowse mode = iota
	modeFilter
	modeForm
	modeConfirm
	modeHelp
	modeSFTP
	modeTheme
	modeForwards
)

const (
	// The sidebar scales with the frame between these. Below the first a name
	// and its marker stop fitting; above the second the extra width is mostly
	// padding, and the pane beside it has better uses for it.
	sidebarMin   = 32
	sidebarMax   = 44
	statusHeight = 1

	// The smallest frame the layout can be drawn in: narrower or shorter than
	// this and the boxes cannot fit their own borders.
	minWidth  = 30
	minHeight = 5
)

// confirmation is a yes/no gate in front of a destructive action.
type confirmation struct {
	prompt string
	detail string
	run    func() (string, error)
}

// Options are the settings the interface takes from the config file.
type Options struct {
	Keys         keymap.Map
	ProbeTimeout time.Duration
	// Version is what -version reports, shown on the help screen so the
	// running build can be identified without quitting to ask.
	Version string

	// Theme is the palette in effect at startup, and Themes are the ones the
	// config file defines, so the picker can offer them alongside the
	// built-ins and resolve a name the same way the config file does.
	Theme  string
	Themes map[string]theme.Palette
	// SaveTheme records a theme chosen in the interface. main supplies it, so
	// the interface needs to know nothing about where configuration lives —
	// and leaving it nil makes the choice last only for the session.
	SaveTheme func(name string) error
}

// Model is the root Bubble Tea model.
type Model struct {
	w, h  int
	focus panel
	mode  mode

	opts Options
	keys keymap.Map
	st   *store.Store
	d    data

	groupIdx, hostIdx int

	filter  textinput.Model
	matches []store.Host

	// helpScroll is how far the cheatsheet has been scrolled. It is longer
	// than a short terminal, and there is nothing to select on it, so an
	// offset is all the state it needs.
	helpScroll int

	form    *form
	confirm *confirmation
	// themes is the open theme picker, nil when there is none.
	themes *themePicker
	// returnTo is the view a modal came from, so closing one does not always
	// dump the user back at the host list.
	returnTo mode

	// forwardHost is the host whose forwarding rules are open, and forwardIdx
	// the highlighted one. The host is remembered rather than re-read from the
	// selection, so the rules a form saves belong to the host the form was
	// opened on.
	forwardHost store.Host
	forwardIdx  int

	probes map[string]probe.State
	// probeCounts is this sweep's tally, reset when a sweep starts.
	probeCounts map[probe.State]int
	probeCh     chan probeEvent
	probing     bool

	runCancel context.CancelFunc

	// attached is the live session in the main pane, nil when there is none.
	attached    *term.Pane
	prefixArmed bool
	sftpSess    *sftpx.Session
	panes       [2]filePane
	paneFocus   int
	// lastClick is where and when the pointer last went down, which is all a
	// double click is: terminals report each press separately and carry no
	// click count of their own.
	lastClick clickAt
	transfers chan transferMsg
	transfer  transferMsg

	status string
	failed bool

	// ready gates input until the first frame has been sized. Anything the
	// terminal delivers before then — a buffered newline from the shell that
	// launched us, a bracketed-paste remnant, a replayed key — would otherwise
	// be acted on, and enter now opens an SSH connection.
	ready bool
}

func New(st *store.Store, opts Options) Model {
	ti := textinput.New()
	ti.Prompt = ""
	ti.Placeholder = "fuzzy search all hosts"

	if opts.ProbeTimeout <= 0 {
		opts.ProbeTimeout = 2 * time.Second
	}

	m := Model{opts: opts, keys: opts.Keys, st: st,
		focus: panelHosts, filter: ti,
		transfers: make(chan transferMsg, 32),
		probeCh:   make(chan probeEvent, 64), probes: map[string]probe.State{}}
	m.reload()
	if m.status == "" {
		m.status = fmt.Sprintf("%d host%s", len(m.d.hosts), plural(len(m.d.hosts)))
	}
	return m
}

func (m Model) Init() tea.Cmd { return nil }

func (m *Model) reload() {
	d, err := load(m.st)
	m.d = d
	if err != nil {
		m.setErr(err)
	}
	m.clampSelection()
}

func (m *Model) setErr(err error) {
	m.status, m.failed = err.Error(), true
}

func (m *Model) setStatus(s string) {
	m.status, m.failed = s, false
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.ready = true
		// The search box lives in the sidebar, so size it to that, not the screen.
		m.filter.SetWidth(max(m.sidebar()-8, 8))

	case probeEvent:
		return m.handleProbeEvent(msg)

	case paneTickMsg:
		return m.handlePaneTick()

	case forwardTickMsg:
		return m.handleForwardTick()

	case forwardDoneMsg:
		return m.handleForwardDone(msg)

	case sftpConnectedMsg:
		return m.sftpConnected(msg)

	case transferMsg:
		m.transfer = msg
		if msg.finished {
			// However it ended. Reloading only after a transfer that worked
			// left the listing describing a file that was no longer there,
			// and a listing that disagrees with the disk is worse than one
			// that is merely a moment out of date.
			m.panes[msg.dst].reload()
		}
		return m, waitTransfer(m.transfers)

	case sshx.SessionEndedMsg:
		if err := m.st.RecordSession(msg.Key, time.Now()); err != nil {
			m.setErr(err)
		} else {
			m.setStatus(sessionSummary(msg))
		}
		m.reload()

	case tea.KeyPressMsg:
		if !m.ready {
			// Nothing is drawn yet; there is no selection to act on and no way
			// for the user to have meant this.
			return m, nil
		}
		return m.handleKey(msg)

	case tea.PasteMsg:
		return m.handlePaste(string(msg.Content))

	case tea.MouseClickMsg:
		if !m.ready {
			return m, nil
		}
		return m.handleMouseClick(msg.Mouse())
	}
	return m, nil
}

// handlePaste delivers bracketed-paste text to whatever is taking input.
//
// The terminal sends a paste as one message rather than as key presses, so
// without this it is silently dropped — the paste appears to do nothing at
// all. Text inputs sanitise it themselves, collapsing newlines and tabs to
// spaces, which is what a single-line field wants.
func (m Model) handlePaste(text string) (tea.Model, tea.Cmd) {
	if text == "" {
		return m, nil
	}
	switch {
	case m.mode == modeForm:
		return m, m.form.update(tea.PasteMsg{Content: text})
	case m.mode == modeFilter:
		var cmd tea.Cmd
		m.filter, cmd = m.filter.Update(tea.PasteMsg{Content: text})
		m.recomputeMatches()
		m.clampSelection()
		return m, cmd
	case m.focus == panelSession && m.attached != nil && m.attached.Alive():
		// A paste into a terminal is just input, newlines and all.
		m.attached.SendText(text)
		return m, nil
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch m.mode {
	case modeHelp:
		return m.handleHelpKey(msg)
	case modeConfirm:
		return m.handleConfirmKey(msg)
	case modeForm:
		return m.handleFormKey(msg)
	case modeFilter:
		return m.handleFilterKey(msg)
	case modeSFTP:
		return m.handleSFTPKey(msg)
	case modeTheme:
		return m.handleThemeKey(msg)
	case modeForwards:
		return m.handleForwardsKey(msg)
	}
	return m.handleBrowseKey(msg)
}

// sidebar is how wide the group and host lists are drawn.
//
// Everything asks here rather than working it out again. The mouse hit-test,
// the session pane's size and the cursor position all have to agree with what
// was drawn, and five copies of one expression is four chances for them not to.
//
// It scales because host names are as long as whoever named them: a list of
// prod-be-delivery-console and prod-VPC_CAPICHI_SUPERSET_BI ended every row in
// an ellipsis while the pane beside it had width to spare.
func (m Model) sidebar() int {
	// Half the frame is the ceiling: on a narrow terminal the sidebar gives
	// way rather than squeezing out what it sits beside.
	half := max(m.w/2, 1)
	return clamp(m.w/3, min(sidebarMin, half), min(sidebarMax, half))
}

// nextPanel cycles focus, skipping the session slot when nothing is connected
// so tab never lands on an empty pane.
func (m Model) nextPanel(d int) panel {
	p := m.focus
	for range int(numPanels) {
		p = (p + panel(d) + numPanels) % numPanels
		if p != panelSession || m.attached != nil {
			return p
		}
	}
	return m.focus
}

// handleBrowseKey dispatches on the configured action rather than the raw key,
// so bindings can be changed without the handlers knowing.
func (m Model) handleBrowseKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// A focused session owns the keyboard; its prefix is the way back out.
	if m.focus == panelSession && m.attached != nil {
		return m.handleSessionKey(msg)
	}
	key := msg.String()

	// The prefix reaches the session from here too, so detaching or ending it
	// does not mean going back into the pane to do it.
	if m.attached != nil && (m.prefixArmed || key == prefixKey) {
		return m.sessionCommand(msg)
	}
	if key == prefixKey {
		// Nothing to command. Saying so beats the silence that made the key
		// look broken, and the d that usually follows open a delete.
		m.setStatus("no session — " + m.keys.Key(keymap.Pane) + " opens one in the main pane")
		return m, nil
	}
	// esc is not an action: it always backs out of whatever is in effect.
	if key == "esc" {
		if m.filtering() {
			m.clearFilter()
		}
		return m, nil
	}

	switch m.keys.Lookup(key) {
	case keymap.Quit:
		return m, tea.Quit
	case keymap.Help:
		m.mode = modeHelp
	case keymap.Theme:
		return m.openThemePicker()
	case keymap.NextPanel:
		m.focus = m.nextPanel(1)
	case keymap.PrevPanel:
		m.focus = m.nextPanel(-1)
	case keymap.PanelGroups:
		m.focus = panelGroups
	case keymap.PanelHosts:
		m.focus = panelHosts
	case keymap.Down:
		m.move(1)
	case keymap.Up:
		m.move(-1)

	case keymap.Search:
		m.mode = modeFilter
		m.focus = panelHosts
		return m, m.filter.Focus()

	case keymap.Connect:
		return m.connect()
	case keymap.NewItem:
		return m.openNewForm()
	case keymap.Edit:
		return m.openEditForm()
	case keymap.Delete:
		return m.askDelete()

	case keymap.Probe:
		return m.startProbe()
	case keymap.SFTP:
		return m.openSFTP()
	case keymap.Forward:
		return m.openForwards()
	case keymap.Pane:
		return m.attachSession()
	case keymap.Redraw:
		// The terminal can clear the screen without telling us — iTerm2's
		// cmd+K, for one. The renderer still believes its last frame is on
		// screen and writes only deltas, so the display stays blank until
		// something forces a full repaint.
		return m, tea.ClearScreen
	case keymap.Reload:
		m.reload()
		m.setStatus(fmt.Sprintf("reloaded — %d host%s", len(m.d.hosts), plural(len(m.d.hosts))))
	}
	return m, nil
}

// handleHelpKey scrolls the cheatsheet, and closes it on anything else.
//
// Help is taller than a short terminal, and closing on literally any key put
// the end of it out of reach: pressing ↓ to read the rest dismissed the screen
// instead of moving down it. Only the keys that mean "move" are kept, so every
// other key still means "done", which is what makes help feel like a glance
// rather than a mode.
func (m Model) handleHelpKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	rows, end := m.helpRows(), m.helpOverflow()
	switch {
	case m.keys.Lookup(key) == keymap.Down:
		m.helpScroll = clamp(m.helpScroll+1, 0, end)
	case m.keys.Lookup(key) == keymap.Up:
		m.helpScroll = clamp(m.helpScroll-1, 0, end)
	case key == "pgdown", key == "ctrl+f", key == " ":
		m.helpScroll = clamp(m.helpScroll+rows, 0, end)
	case key == "pgup", key == "ctrl+b":
		m.helpScroll = clamp(m.helpScroll-rows, 0, end)
	case key == "home":
		m.helpScroll = 0
	case key == "G", key == "end":
		m.helpScroll = end
	default:
		m.mode, m.helpScroll = modeBrowse, 0
	}
	return m, nil
}

func (m Model) handleFilterKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.clearFilter()
		m.mode = modeBrowse
		return m, nil
	case "enter":
		m.mode = modeBrowse
		m.filter.Blur()
		return m, nil
	case "ctrl+c":
		return m, tea.Quit
	case "up", "down":
		d := 1
		if msg.String() == "up" {
			d = -1
		}
		m.move(d)
		return m, nil
	}

	var cmd tea.Cmd
	m.filter, cmd = m.filter.Update(msg)
	m.recomputeMatches()
	return m, cmd
}

func (m Model) handleConfirmKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		status, err := m.confirm.run()
		back := m.returnTo
		m.confirm, m.mode = nil, back
		if err != nil {
			m.setErr(err)
		} else {
			m.setStatus(status)
		}
		if back == modeSFTP {
			m.panes[m.paneFocus].reload()
		} else {
			m.reload()
		}
	case "n", "N", "esc", "q":
		m.confirm, m.mode = nil, m.returnTo
		m.setStatus("cancelled")
	}
	return m, nil
}

func (m Model) handleFormKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	// An open picker takes the keys that drive a list, since those are the
	// same ones that would otherwise leave the field. Anything else dismisses
	// it and is handled normally, so the list never traps the keyboard.
	if m.form.picking {
		switch key {
		case "down", "ctrl+n":
			m.form.movePicker(1)
			return m, nil
		case "up", "ctrl+p":
			m.form.movePicker(-1)
			return m, nil
		case " ", "space":
			// Space toggles a set's entries; on a single-value picker it is
			// just a character, so it falls through and dismisses the list.
			if m.form.fields[m.form.idx].list {
				m.form.togglePicked()
				return m, nil
			}
		case "enter", "tab":
			m.form.choosePicked()
			return m, nil
		case "esc":
			m.form.closePicker()
			return m, nil
		}
		m.form.closePicker()
	}

	switch key {
	case "esc":
		m.form, m.mode = nil, m.returnTo
		m.setStatus("cancelled")
		return m, nil
	case "down":
		// On a field that offers known values, down opens the list rather
		// than skipping past it; tab is still how you move on.
		if m.form.hasChoices() {
			m.form.openPicker()
			return m, nil
		}
		return m, m.form.move(1)
	case "tab":
		return m, m.form.move(1)
	case "shift+tab", "up":
		return m, m.form.move(-1)
	case "enter":
		return m.saveForm()
	}
	return m, m.form.update(msg)
}

// --- selection ---------------------------------------------------------

func (m Model) filtering() bool { return strings.TrimSpace(m.filter.Value()) != "" }

func (m *Model) clearFilter() {
	m.filter.Reset()
	m.filter.Blur()
	m.matches = nil
	m.clampSelection()
}

func (m *Model) recomputeMatches() {
	q := strings.TrimSpace(m.filter.Value())
	if q == "" {
		m.matches = nil
		return
	}
	targets := make([]string, len(m.d.hosts))
	for i, h := range m.d.hosts {
		targets[i] = h.Name + " " + h.Addr + " " + strings.Join(h.Tags, " ")
	}
	m.matches = m.matches[:0]
	for _, r := range fuzzy.Find(q, targets) {
		m.matches = append(m.matches, m.d.hosts[r.Index])
	}
	m.hostIdx = 0
}

func (m *Model) move(d int) {
	if m.focus == panelGroups && !m.filtering() {
		m.groupIdx = clamp(m.groupIdx+d, 0, len(m.d.tree)-1)
		m.hostIdx = 0
		return
	}
	m.hostIdx = clamp(m.hostIdx+d, 0, len(m.visibleHosts())-1)
}

func (m *Model) clampSelection() {
	m.groupIdx = clamp(m.groupIdx, 0, len(m.d.tree)-1)
	m.hostIdx = clamp(m.hostIdx, 0, len(m.visibleHosts())-1)
}

func (m Model) currentGroup() (store.GroupNode, bool) {
	if len(m.d.tree) == 0 {
		return store.GroupNode{}, false
	}
	return m.d.tree[clamp(m.groupIdx, 0, len(m.d.tree)-1)], true
}

func (m Model) visibleHosts() []store.Host {
	if m.filtering() {
		return m.matches
	}
	g, ok := m.currentGroup()
	if !ok {
		return nil
	}
	return m.d.hostsIn(g.ID)
}

func (m Model) selectedHost() (store.Host, bool) {
	hosts := m.visibleHosts()
	if len(hosts) == 0 {
		return store.Host{}, false
	}
	return hosts[clamp(m.hostIdx, 0, len(hosts)-1)], true
}

// --- actions -----------------------------------------------------------

func (m Model) connect() (tea.Model, tea.Cmd) {
	h, ok := m.selectedHost()
	if !ok {
		return m, nil
	}
	resolved := m.d.resolver.Resolve(h)
	m.setStatus("connecting to " + h.Name + "…")
	return m, sshx.Connect(resolved.Host)
}

func (m Model) openNewForm() (tea.Model, tea.Cmd) {
	if m.focus == panelGroups {
		m.form = newGroupForm(store.Group{}, "", m.groupChoices(""))
	} else {
		g, _ := m.currentGroup()
		name := ""
		if g.ID != UngroupedID {
			name = g.Name
		}
		m.form = newHostForm(store.Host{}, name, m.hostChoices(""))
	}
	m.returnTo, m.mode = backFor(m.mode), modeForm
	return m, m.form.focusCurrent()
}

func (m Model) openEditForm() (tea.Model, tea.Cmd) {
	if m.focus == panelGroups {
		g, ok := m.currentGroup()
		if !ok || isSynthetic(g.ID) {
			m.setStatus("that group is generated, not stored")
			return m, nil
		}
		m.form = newGroupForm(g.Group, m.d.groupName(g.ParentID), m.groupChoices(g.ID))
		m.returnTo, m.mode = backFor(m.mode), modeForm
		return m, m.form.focusCurrent()
	}

	h, ok := m.selectedHost()
	if !ok {
		return m, nil
	}
	m.form = newHostForm(h, m.d.groupName(h.GroupID), m.hostChoices(h.ID))
	m.returnTo, m.mode = backFor(m.mode), modeForm
	return m, m.form.focusCurrent()
}

func (m Model) askDelete() (tea.Model, tea.Cmd) {
	if m.focus == panelGroups {
		g, ok := m.currentGroup()
		if !ok || isSynthetic(g.ID) {
			m.setStatus("that group is generated, not stored")
			return m, nil
		}
		gs, hs := m.st.Counts(g.ID)
		into := m.d.groupName(g.ParentID)
		if into == "" {
			into = "Ungrouped"
		}
		detail := "nothing else is affected"
		if gs+hs > 0 {
			detail = fmt.Sprintf("%d group(s) and %d host(s) move to %s — nothing is deleted", gs, hs, into)
		}
		id := g.ID
		m.confirm = &confirmation{
			prompt: "Delete group " + g.Name + "?",
			detail: detail,
			run: func() (string, error) {
				return "deleted group " + g.Name, m.st.DeleteGroup(id)
			},
		}
		m.returnTo, m.mode = backFor(m.mode), modeConfirm
		return m, nil
	}

	h, ok := m.selectedHost()
	if !ok {
		return m, nil
	}
	// A forwarding rule names a host and means nothing without it, so the
	// rules go with it — and their tunnels, which would otherwise keep their
	// ports with nothing left in the interface that could reach them.
	var extra []string
	if m.d.hasSession(h) {
		extra = append(extra, "its running session is ended")
	}
	if n := len(m.d.forwardsFor(h.ID)); n > 0 {
		extra = append(extra, fmt.Sprintf("%d forward%s and any tunnel of theirs go too", n, plural(n)))
	}
	detail := "session history is kept"
	if len(extra) > 0 {
		detail = strings.Join(extra, "; ") + "; session history is kept"
	}
	m.confirm = &confirmation{
		prompt: "Delete host " + h.Name + "?",
		detail: detail,
		run: func() (string, error) {
			if m.d.hasSession(h) {
				if err := term.KillSession(term.SessionName(h)); err != nil {
					return "", err
				}
			}
			if err := m.stopForwardsFor(h.ID); err != nil {
				return "", err
			}
			return "deleted " + h.Name, m.st.DeleteHost(h.ID)
		},
	}
	m.returnTo, m.mode = backFor(m.mode), modeConfirm
	return m, nil
}

func (m Model) saveForm() (tea.Model, tea.Cmd) {
	f := m.form
	switch f.kind {
	case formMkdir, formRename, formChmod:
		return m.saveFileForm()
	case formForward:
		return m.saveForwardForm()
	}
	if f.kind == formGroup {
		name := f.value("Name")
		if name == "" {
			f.problem = "a group needs a name"
			return m, nil
		}
		g := store.Group{
			ID: f.editID, Name: name,
			User: f.value("User"), Identity: f.value("Identity"),
			ProxyJump: f.value("Jump host"),
		}
		if p := f.value("Parent"); p != "" {
			parent, ok := m.d.groupByName(p)
			if !ok {
				f.problem = "no group named " + p
				return m, nil
			}
			g.ParentID = parent.ID
		}
		if _, err := m.st.PutGroup(g); err != nil {
			f.problem = err.Error()
			return m, nil
		}
		m.form, m.mode = nil, modeBrowse
		m.reload()
		m.setStatus("saved group " + name)
		return m, nil
	}

	name, addr := f.value("Name"), f.value("Address")
	if name == "" {
		f.problem = "a host needs a name"
		return m, nil
	}
	if addr == "" {
		f.problem = "a host needs an address"
		return m, nil
	}
	port := 0
	if p := f.value("Port"); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			f.problem = "port must be a number between 1 and 65535"
			return m, nil
		}
		port = n
	}

	h := store.Host{
		ID: f.editID, Name: name, Addr: addr, Port: port,
		User: f.value("User"), Identity: f.value("Identity"),
		ProxyJump: f.value("Jump host"),
		Tags:      splitTags(f.value("Tags")),
	}
	// Typing an unknown group name creates it, so adding a host to a new group
	// never means backing out to make the group first.
	created := ""
	if gname := f.value("Group"); gname != "" {
		g, ok := m.d.groupByName(gname)
		if !ok {
			var err error
			if g, err = m.st.PutGroup(store.Group{Name: gname}); err != nil {
				f.problem = err.Error()
				return m, nil
			}
			created = " (created group " + gname + ")"
		}
		h.GroupID = g.ID
	}
	saved, err := m.st.PutHost(h)
	if err != nil {
		f.problem = err.Error()
		return m, nil
	}

	m.form, m.mode = nil, modeBrowse
	m.reload()
	m.selectHost(saved)
	m.setStatus("saved " + name + created)
	return m, nil
}

// --- helpers -----------------------------------------------------------

// hostChoices are the known values the host form offers as pickers. Bundled
// rather than passed one slice at a time, so adding another picker later does
// not mean threading a new argument through every caller.
type hostChoices struct {
	jumpHosts []string
	groups    []string
	tags      []string
}

func newHostForm(h store.Host, groupName string, c hostChoices) *form {
	port := ""
	if h.Port != 0 {
		port = strconv.Itoa(h.Port)
	}
	title := "New host"
	if h.ID != "" {
		title = "Edit " + h.Name
	}
	return &form{
		kind: formHost, title: title, editID: h.ID,
		fields: []field{
			newField("Name", "prod-web-01", h.Name),
			newField("Address", "10.0.1.14", h.Addr),
			newField("Port", "22", port),
			newField("User", "inherited from group", h.User),
			newField("Identity", "path to a private key", h.Identity),
			asSuggestion(withChoices(newField("Jump host", "inherited from group — ↓ to pick", h.ProxyJump), c.jumpHosts)),
			asList(withChoices(newField("Tags", "prod, web — ↓ to pick", strings.Join(h.Tags, ", ")), c.tags)),
			asSuggestion(withChoices(newField("Group", "↓ to pick, or type to create", groupName), c.groups)),
		},
	}
}

func newGroupForm(g store.Group, parentName string, c groupChoices) *form {
	title := "New group"
	if g.ID != "" {
		title = "Edit " + g.Name
	}
	return &form{
		kind: formGroup, title: title, editID: g.ID,
		fields: []field{
			newField("Name", "Production", g.Name),
			asSuggestion(withChoices(newField("Parent", "none — ↓ to pick", parentName), c.parents)),
			newField("User", "applies to hosts below", g.User),
			newField("Identity", "path to a private key", g.Identity),
			asSuggestion(withChoices(newField("Jump host", "applies to hosts below — ↓ to pick", g.ProxyJump), c.jumpHosts)),
		},
	}
}

// groupChoices are the values a group form offers to fill itself in from.
type groupChoices struct {
	parents   []string
	jumpHosts []string
}

// groupChoices lists what a group's parent and jump host can be set to.
//
// excludeID is the group being edited. A group cannot be its own parent, and
// neither can anything already beneath it, so both are left out: the store
// refuses such a tree anyway, and a picker that offers a choice it will not
// accept is worse than one that does not offer it.
func (m Model) groupChoices(excludeID string) groupChoices {
	c := groupChoices{
		parents:   []string{noChoice},
		jumpHosts: []string{noChoice},
	}
	// A group cannot go under itself, and withDescendants includes it.
	below := m.d.withDescendants(excludeID)
	for _, g := range m.d.groups {
		if below[g.ID] {
			continue
		}
		c.parents = append(c.parents, g.Name)
	}
	for _, h := range m.d.hosts {
		c.jumpHosts = append(c.jumpHosts, h.Name)
	}
	return c
}

// backFor records which view a modal was opened from, so esc returns there.
// A modal opened from another modal still returns to the underlying view.
func backFor(current mode) mode {
	switch current {
	case modeSFTP, modeForwards:
		return current
	default:
		return modeBrowse
	}
}

func isSynthetic(id string) bool { return id == UngroupedID }

// selectHost moves the group and host cursors onto h, following it into
// whichever group it was saved in, so a host you just created is selected
// rather than left off-screen in another group.
func (m *Model) selectHost(h store.Host) {
	want := h.GroupID
	if want == "" {
		want = UngroupedID
	}
	for i, g := range m.d.tree {
		if g.ID == want {
			m.groupIdx = i
			break
		}
	}
	for i, x := range m.visibleHosts() {
		if x.ID == h.ID {
			m.hostIdx = i
			break
		}
	}
}

func splitTags(s string) []string {
	var out []string
	for _, t := range strings.Split(s, ",") {
		if t = strings.TrimSpace(t); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func sessionSummary(msg sshx.SessionEndedMsg) string {
	switch {
	case msg.Err != nil:
		return fmt.Sprintf("%s failed: %v", msg.HostName, msg.Err)
	case msg.ExitCode == sshx.ConnectionFailed && msg.Detail != "":
		// The code and what ssh said, in that order: the code is certain, and
		// the last line of stderr is context rather than a claim about the
		// cause. Without it every failure to connect read the same, however
		// different the fix — refused, rejected, timed out, host key changed.
		return fmt.Sprintf("%s exited %d — %s", msg.HostName, msg.ExitCode, msg.Detail)
	case msg.ExitCode != 0:
		return fmt.Sprintf("%s exited %d after %s", msg.HostName, msg.ExitCode, dur(msg.Duration))
	default:
		return fmt.Sprintf("%s session ended after %s", msg.HostName, dur(msg.Duration))
	}
}

func dur(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%.0fs", d.Seconds())
	}
	return d.Round(time.Second).String()
}

func clamp(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	return min(max(v, lo), hi)
}

func strOr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

// hostChoices gathers what the host form's pickers offer.
//
// The host being edited is left out of the jump hosts: a host cannot jump
// through itself, and offering it would only produce a connection that hangs.
// Groups come from the store, so the synthetic "Ungrouped" heading is not
// among them — leaving a host ungrouped is what the empty choice is for.
func (m Model) hostChoices(excludeID string) hostChoices {
	// A single-value picker leads with the empty choice, which is how such a
	// field is cleared without deleting characters. A set has no use for it:
	// toggling every entry off is what emptying it means.
	c := hostChoices{
		jumpHosts: []string{noChoice},
		groups:    []string{noChoice},
	}
	for _, h := range m.d.hosts {
		if h.ID == excludeID {
			continue
		}
		c.jumpHosts = append(c.jumpHosts, h.Name)
	}
	for _, g := range m.d.groups {
		c.groups = append(c.groups, g.Name)
	}

	// Every tag in use, so the vocabulary stays shared rather than drifting
	// into prod/Prod/prd. A mistyped tag fails silently — the host simply
	// stops matching a search — which is what makes picking worth offering.
	seen := map[string]bool{}
	var tags []string
	for _, h := range m.d.hosts {
		for _, t := range h.Tags {
			if !seen[t] {
				seen[t] = true
				tags = append(tags, t)
			}
		}
	}
	slices.Sort(tags)
	c.tags = append(c.tags, tags...)
	return c
}
