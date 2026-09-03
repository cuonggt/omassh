// Package ui implements Omassh's terminal interface.
package ui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/sahilm/fuzzy"

	"github.com/cuonggt/omassh/internal/forward"
	"github.com/cuonggt/omassh/internal/runner"
	"github.com/cuonggt/omassh/internal/secrets"
	"github.com/cuonggt/omassh/internal/sftpx"
	"github.com/cuonggt/omassh/internal/sshx"
	"github.com/cuonggt/omassh/internal/store"
)

type panel int

const (
	panelGroups panel = iota
	panelHosts
	panelForwards
	numPanels
)

type mode int

const (
	modeBrowse mode = iota
	modeFilter
	modeForm
	modeConfirm
	modeHelp
	modeIdentities
	modeSFTP
	modeSnippets
	modeResults
)

const (
	sidebarWidth = 32
	statusHeight = 1
)

// confirmation is a yes/no gate in front of a destructive action.
type confirmation struct {
	prompt string
	detail string
	run    func() (string, error)
}

// Model is the root Bubble Tea model.
type Model struct {
	w, h  int
	focus panel
	mode  mode

	st    *store.Store
	vault secrets.Vault
	sup   *forward.Supervisor
	index *hostIndex
	d     data

	groupIdx, hostIdx, identityIdx, forwardIdx int

	// agentKeys is the set of fingerprints the ssh-agent holds, refreshed on
	// demand rather than per frame: reading it runs ssh-add.
	agentKeys map[string]bool
	agentErr  error

	filter  textinput.Model
	matches []store.Host

	form    *form
	confirm *confirmation
	// returnTo is the view a modal came from, so closing one does not always
	// dump the user back at the host list.
	returnTo mode

	snippetIdx int
	pending    pendingRun
	confirmRun bool
	results    []runner.Result
	resultIdx  int
	outputTop  int
	resultsCh  chan runEvent
	running    bool
	runTotal   int
	runDone    int
	runLabel   string
	runCancel  context.CancelFunc

	sftpSess  *sftpx.Session
	panes     [2]filePane
	paneFocus int
	transfers chan transferMsg
	transfer  transferMsg

	status string
	failed bool
}

func New(st *store.Store, vault secrets.Vault, sup *forward.Supervisor) Model {
	ti := textinput.New()
	ti.Prompt = ""
	ti.Placeholder = "fuzzy search all hosts"

	index := newHostIndex()
	sup.SetBuilder(forwardBuilder(index))

	m := Model{st: st, vault: vault, sup: sup, index: index,
		focus: panelHosts, filter: ti, agentKeys: map[string]bool{},
		transfers: make(chan transferMsg, 32), resultsCh: make(chan runEvent, 64)}
	m.reload()
	if m.status == "" {
		m.status = fmt.Sprintf("%d host%s", len(m.d.hosts), plural(len(m.d.hosts)))
	}
	return m
}

func (m Model) Init() tea.Cmd { return waitForwardEvent(m.sup.Events()) }

func (m *Model) reload() {
	d, err := load(m.st)
	m.d = d
	// Keep the supervisor's view of hosts current; it resolves them off the
	// UI goroutine when a tunnel starts or restarts.
	m.index.set(d.hosts, d.resolver)
	if err != nil {
		m.setErr(fmt.Errorf("ssh_config: %w", err))
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
		// The search box lives in the sidebar, so size it to that, not the screen.
		m.filter.SetWidth(max(clamp(sidebarWidth, 20, m.w/2)-8, 8))

	case runEvent:
		return m.handleRunEvent(msg)

	case sftpConnectedMsg:
		return m.sftpConnected(msg)

	case transferMsg:
		m.transfer = msg
		if msg.finished && msg.err == nil {
			// The destination pane has a new file in it.
			m.panes[1-m.paneFocus].reload()
		}
		return m, waitTransfer(m.transfers)

	case forwardEventMsg:
		// Status is read live from the supervisor, so the event only needs to
		// prompt a redraw and re-arm the listener.
		return m, waitForwardEvent(m.sup.Events())

	case sshx.SessionEndedMsg:
		if err := m.st.RecordSession(msg.Key, time.Now()); err != nil {
			m.setErr(err)
		} else {
			m.setStatus(sessionSummary(msg))
		}
		m.reload()

	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch m.mode {
	case modeHelp:
		m.mode = modeBrowse
		return m, nil
	case modeConfirm:
		return m.handleConfirmKey(msg)
	case modeForm:
		return m.handleFormKey(msg)
	case modeFilter:
		return m.handleFilterKey(msg)
	case modeIdentities:
		return m.handleIdentitiesKey(msg)
	case modeSFTP:
		return m.handleSFTPKey(msg)
	case modeSnippets:
		return m.handleSnippetsKey(msg)
	case modeResults:
		return m.handleResultsKey(msg)
	}
	return m.handleBrowseKey(msg)
}

func (m Model) handleBrowseKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "?":
		m.mode = modeHelp
	case "tab":
		m.focus = (m.focus + 1) % numPanels
	case "shift+tab":
		m.focus = (m.focus + numPanels - 1) % numPanels
	case "1":
		m.focus = panelGroups
	case "2":
		m.focus = panelHosts
	case "3":
		m.focus = panelForwards
	case "j", "down":
		m.move(1)
	case "k", "up":
		m.move(-1)

	case "/":
		m.mode = modeFilter
		m.focus = panelHosts
		return m, m.filter.Focus()

	case "esc":
		if m.filtering() {
			m.clearFilter()
		}

	case "enter":
		if m.focus == panelForwards {
			return m.toggleForward()
		}
		return m.connect()

	case "n":
		if m.focus == panelForwards {
			return m.openNewForwardForm()
		}
		return m.openNewForm()
	case "e":
		if m.focus == panelForwards {
			return m.openEditForwardForm()
		}
		return m.openEditForm()
	case "d":
		if m.focus == panelForwards {
			return m.askDeleteForward()
		}
		return m.askDelete()
	case "i":
		return m.importSelected()
	case "K":
		return m.openIdentities()
	case "s":
		return m.openSFTP()
	case "S":
		return m.openSnippets()
	case "r":
		m.reload()
		m.setStatus(fmt.Sprintf("reloaded — %d host%s", len(m.d.hosts), plural(len(m.d.hosts))))
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
		wasRun := m.confirmRun
		m.confirm, m.confirmRun, m.mode = nil, false, back
		if wasRun && err == nil {
			return m.startRun()
		}
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
		m.confirm, m.confirmRun, m.mode = nil, false, m.returnTo
		m.setStatus("cancelled")
	}
	return m, nil
}

func (m Model) handleFormKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.form, m.mode = nil, m.returnTo
		m.setStatus("cancelled")
		return m, nil
	case "tab", "down":
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
	switch {
	case m.focus == panelForwards:
		m.forwardIdx = clamp(m.forwardIdx+d, 0, len(m.visibleForwards())-1)
	case m.focus == panelGroups && !m.filtering():
		m.groupIdx = clamp(m.groupIdx+d, 0, len(m.d.tree)-1)
		m.hostIdx, m.forwardIdx = 0, 0
	default:
		m.hostIdx = clamp(m.hostIdx+d, 0, len(m.visibleHosts())-1)
		m.forwardIdx = 0
	}
}

func (m *Model) clampSelection() {
	m.groupIdx = clamp(m.groupIdx, 0, len(m.d.tree)-1)
	m.hostIdx = clamp(m.hostIdx, 0, len(m.visibleHosts())-1)
	m.forwardIdx = clamp(m.forwardIdx, 0, len(m.visibleForwards())-1)
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
		m.form = newGroupForm(store.Group{}, "", "")
	} else {
		g, _ := m.currentGroup()
		name := ""
		if g.ID != store.SSHConfigGroupID && g.ID != UngroupedID {
			name = g.Name
		}
		m.form = newHostForm(store.Host{}, name, "")
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
		m.form = newGroupForm(g.Group, m.d.groupName(g.ParentID), m.identityLabel(g.IdentityID, g.Identity))
		m.returnTo, m.mode = backFor(m.mode), modeForm
		return m, m.form.focusCurrent()
	}

	h, ok := m.selectedHost()
	if !ok {
		return m, nil
	}
	if h.Source == store.SourceSSHConfig {
		m.setStatus("ssh_config hosts are read-only — press i to import a copy")
		return m, nil
	}
	m.form = newHostForm(h, m.d.groupName(h.GroupID), m.identityLabel(h.IdentityID, h.Identity))
	m.returnTo, m.mode = backFor(m.mode), modeForm
	return m, m.form.focusCurrent()
}

// importSelected copies a config-sourced host into the store so it can be
// edited, leaving ~/.ssh/config untouched.
func (m Model) importSelected() (tea.Model, tea.Cmd) {
	h, ok := m.selectedHost()
	if !ok {
		return m, nil
	}
	if h.Source != store.SourceSSHConfig {
		m.setStatus("that host is already stored")
		return m, nil
	}
	// A ProxyCommand is what makes such a host reachable at all, and Omassh
	// models jump hosts rather than arbitrary shell. Importing would produce a
	// host that looks fine and cannot connect, so refuse instead.
	if h.Note != "" {
		m.setStatus(h.Name + " depends on a ProxyCommand — connect it by alias instead")
		return m, nil
	}
	copied := h
	copied.ID, copied.Source, copied.GroupID, copied.Note = "", store.SourceLocal, "", ""
	if _, err := m.st.PutHost(copied); err != nil {
		m.setErr(err)
		return m, nil
	}
	m.reload()
	m.setStatus("imported " + h.Name + " — ~/.ssh/config not modified")
	return m, nil
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
	if h.Source == store.SourceSSHConfig {
		m.setStatus("ssh_config hosts are read-only — edit ~/.ssh/config to remove")
		return m, nil
	}
	m.confirm = &confirmation{
		prompt: "Delete host " + h.Name + "?",
		detail: "session history is kept",
		run: func() (string, error) {
			return "deleted " + h.Name, m.st.DeleteHost(h.ID)
		},
	}
	m.returnTo, m.mode = backFor(m.mode), modeConfirm
	return m, nil
}

func (m Model) saveForm() (tea.Model, tea.Cmd) {
	f := m.form
	switch f.kind {
	case formIdentity:
		return m.saveIdentityForm()
	case formGenKey:
		return m.saveGenKeyForm()
	case formForward:
		return m.saveForwardForm()
	case formMkdir, formRename, formChmod:
		return m.saveFileForm()
	case formSnippet:
		return m.saveSnippetForm()
	case formTypedRun:
		return m.saveTypedRunForm()
	}
	if f.kind == formGroup {
		name := f.value("Name")
		if name == "" {
			f.problem = "a group needs a name"
			return m, nil
		}
		credID, keyPath, err := m.resolveIdentityField(f.value("Identity"))
		if err != nil {
			f.problem = err.Error()
			return m, nil
		}
		g := store.Group{
			ID: f.editID, Name: name,
			User: f.value("User"), Identity: keyPath, IdentityID: credID,
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

	credID, keyPath, err := m.resolveIdentityField(f.value("Identity"))
	if err != nil {
		f.problem = err.Error()
		return m, nil
	}
	h := store.Host{
		ID: f.editID, Name: name, Addr: addr, Port: port,
		User: f.value("User"), Identity: keyPath, IdentityID: credID,
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

// identityLabel renders a binding for a form field: the credential name when
// one is bound, otherwise the raw key path.
func (m Model) identityLabel(identityID, keyPath string) string {
	if identityID != "" {
		if idn, ok := m.d.identityByID(identityID); ok {
			return idn.Name
		}
	}
	return keyPath
}

// resolveIdentityField interprets the single Identity field, which accepts
// either a stored credential's name or a raw key path. One field rather than
// two keeps the common case short, and an unknown bare word is far more likely
// to be a typo'd credential name than a key file in the current directory.
func (m Model) resolveIdentityField(val string) (identityID, keyPath string, err error) {
	if val == "" {
		return "", "", nil
	}
	if idn, ok := m.d.identityByName(val); ok {
		return idn.ID, "", nil
	}
	if strings.ContainsAny(val, `/\`) || strings.HasPrefix(val, "~") || strings.HasPrefix(val, ".") {
		return "", val, nil
	}
	return "", "", fmt.Errorf("no credential named %q — give a path for a key file", val)
}

func newHostForm(h store.Host, groupName, identityLabel string) *form {
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
			newField("Identity", "credential name or key path", identityLabel),
			newField("Jump host", "inherited from group", h.ProxyJump),
			newField("Tags", "prod, web", strings.Join(h.Tags, ", ")),
			newField("Group", "unknown names are created", groupName),
		},
	}
}

func newGroupForm(g store.Group, parentName, identityLabel string) *form {
	title := "New group"
	if g.ID != "" {
		title = "Edit " + g.Name
	}
	return &form{
		kind: formGroup, title: title, editID: g.ID,
		fields: []field{
			newField("Name", "Production", g.Name),
			newField("Parent", "none", parentName),
			newField("User", "applies to hosts below", g.User),
			newField("Identity", "credential name or key path", identityLabel),
			newField("Jump host", "applies to hosts below", g.ProxyJump),
		},
	}
}

// backFor records which view a modal was opened from, so esc returns there.
// A modal opened from another modal still returns to the underlying view.
func backFor(current mode) mode {
	switch current {
	case modeIdentities, modeSFTP, modeSnippets:
		return current
	default:
		return modeBrowse
	}
}

func isSynthetic(id string) bool {
	return id == store.SSHConfigGroupID || id == UngroupedID
}

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
	m.forwardIdx = 0
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
