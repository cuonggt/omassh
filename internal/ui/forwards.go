package ui

import (
	"fmt"
	"image/color"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/cuonggt/omassh/internal/forward"
	"github.com/cuonggt/omassh/internal/sshx"
	"github.com/cuonggt/omassh/internal/store"
	"github.com/cuonggt/omassh/internal/ui/theme"
)

// hostIndex lets the forward supervisor resolve a rule's host from a
// background goroutine. The Bubble Tea model is copied on every update, so a
// closure over it would go stale; this is updated in place on each reload.
type hostIndex struct {
	mu    sync.RWMutex
	byKey map[string]store.Host
	res   store.Resolver
}

func newHostIndex() *hostIndex { return &hostIndex{byKey: map[string]store.Host{}} }

func (x *hostIndex) set(hosts []store.Host, res store.Resolver) {
	m := make(map[string]store.Host, len(hosts))
	for _, h := range hosts {
		m[h.StatKey()] = h
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	x.byKey, x.res = m, res
}

func (x *hostIndex) resolve(key string) (store.Host, bool) {
	x.mu.RLock()
	defer x.mu.RUnlock()
	h, ok := x.byKey[key]
	if !ok {
		return store.Host{}, false
	}
	return x.res.Resolve(h).Host, true
}

// forwardBuilder turns a stored rule into the ssh child that carries it.
func forwardBuilder(x *hostIndex) forward.Builder {
	return func(f store.Forward) (*exec.Cmd, error) {
		h, ok := x.resolve(f.HostKey)
		if !ok {
			return nil, fmt.Errorf("the host for this rule no longer exists")
		}
		return exec.Command("ssh", sshx.ForwardArgs(h, f)...), nil
	}
}

type forwardEventMsg forward.Event

// waitForwardEvent blocks in a command until the supervisor reports a change.
func waitForwardEvent(ch <-chan forward.Event) tea.Cmd {
	return func() tea.Msg { return forwardEventMsg(<-ch) }
}

// --- selection ---------------------------------------------------------

// visibleForwards are the rules belonging to the selected host.
func (m Model) visibleForwards() []store.Forward {
	h, ok := m.selectedHost()
	if !ok {
		return nil
	}
	key := h.StatKey()
	var out []store.Forward
	for _, f := range m.d.forwards {
		if f.HostKey == key {
			out = append(out, f)
		}
	}
	return out
}

func (m Model) selectedForward() (store.Forward, bool) {
	fwds := m.visibleForwards()
	if len(fwds) == 0 {
		return store.Forward{}, false
	}
	return fwds[clamp(m.forwardIdx, 0, len(fwds)-1)], true
}

// --- actions -----------------------------------------------------------

// toggleForward starts a stopped rule or stops a live one.
func (m Model) toggleForward() (tea.Model, tea.Cmd) {
	f, ok := m.selectedForward()
	if !ok {
		return m, nil
	}
	if m.sup.Active(f.ID) {
		m.sup.Stop(f.ID)
		m.setStatus("stopping " + f.Label())
		return m, nil
	}
	if err := m.sup.Start(f); err != nil {
		m.setErr(err)
		return m, nil
	}
	m.setStatus("starting " + f.Label())
	return m, nil
}

func (m Model) openNewForwardForm() (tea.Model, tea.Cmd) {
	h, ok := m.selectedHost()
	if !ok {
		m.setStatus("select a host first")
		return m, nil
	}
	m.form = newForwardForm(store.Forward{HostKey: h.StatKey()}, h.Name)
	m.returnTo, m.mode = backFor(m.mode), modeForm
	return m, m.form.focusCurrent()
}

func (m Model) openEditForwardForm() (tea.Model, tea.Cmd) {
	f, ok := m.selectedForward()
	if !ok {
		return m, nil
	}
	if m.sup.Active(f.ID) {
		m.setStatus("stop " + f.Label() + " before editing it")
		return m, nil
	}
	h, _ := m.selectedHost()
	m.form = newForwardForm(f, h.Name)
	m.returnTo, m.mode = backFor(m.mode), modeForm
	return m, m.form.focusCurrent()
}

func (m Model) askDeleteForward() (tea.Model, tea.Cmd) {
	f, ok := m.selectedForward()
	if !ok {
		return m, nil
	}
	sup, id, label := m.sup, f.ID, f.Label()
	detail := "the tunnel is not running"
	if m.sup.Active(id) {
		detail = "the running tunnel is stopped first"
	}
	m.confirm = &confirmation{
		prompt: "Delete forward " + label + "?",
		detail: detail,
		run: func() (string, error) {
			sup.Stop(id)
			return "deleted forward " + label, m.st.DeleteForward(id)
		},
	}
	m.returnTo, m.mode = backFor(m.mode), modeConfirm
	return m, nil
}

func newForwardForm(f store.Forward, hostName string) *form {
	title := "New forward on " + hostName
	if f.ID != "" {
		title = "Edit forward on " + hostName
	}
	port := func(n int) string {
		if n == 0 {
			return ""
		}
		return strconv.Itoa(n)
	}
	return &form{
		kind: formForward, title: title, editID: f.ID,
		hostKey: f.HostKey,
		fields: []field{
			newField("Name", "optional label", f.Name),
			newField("Kind", "local, remote or dynamic", f.Kind.String()),
			newField("Listen port", "5432", port(f.ListenPort)),
			newField("Target host", "localhost (not used by dynamic)", f.TargetHost),
			newField("Target port", "5432 (not used by dynamic)", port(f.TargetPort)),
			newField("Bind address", "localhost", f.BindAddr),
		},
	}
}

func (m Model) saveForwardForm() (tea.Model, tea.Cmd) {
	f := m.form

	kind, err := store.ParseForwardKind(f.value("Kind"))
	if err != nil {
		f.problem = err.Error()
		return m, nil
	}
	listen, err := parsePort(f.value("Listen port"))
	if err != nil {
		f.problem = "listen port: " + err.Error()
		return m, nil
	}
	target := 0
	if v := f.value("Target port"); v != "" {
		if target, err = parsePort(v); err != nil {
			f.problem = "target port: " + err.Error()
			return m, nil
		}
	}

	rule := store.Forward{
		ID: f.editID, HostKey: f.hostKey, Name: f.value("Name"),
		Kind: kind, BindAddr: f.value("Bind address"),
		ListenPort: listen, TargetHost: f.value("Target host"), TargetPort: target,
	}
	saved, err := m.st.PutForward(rule)
	if err != nil {
		f.problem = err.Error()
		return m, nil
	}

	m.form, m.mode = nil, modeBrowse
	m.focus = panelForwards
	m.reload()
	m.selectForward(saved.ID)
	m.setStatus("saved forward " + saved.Label())
	return m, nil
}

// selectForward moves the cursor onto a rule, so that a rule you just created
// is the one the next keystroke acts on.
func (m *Model) selectForward(id string) {
	for i, f := range m.visibleForwards() {
		if f.ID == id {
			m.forwardIdx = i
			return
		}
	}
}

func parsePort(s string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 1 || n > 65535 {
		return 0, fmt.Errorf("must be a number between 1 and 65535")
	}
	return n, nil
}

// --- rendering ---------------------------------------------------------

func (m Model) forwardsBody(w int) string {
	fwds := m.visibleForwards()
	if len(fwds) == 0 {
		if _, ok := m.selectedHost(); !ok {
			return theme.Dim.Render("—")
		}
		return theme.Dim.Render("no forwards — n to add")
	}

	lines := make([]string, 0, len(fwds))
	for i, f := range fwds {
		st := m.sup.Status(f.ID)
		selected := i == m.forwardIdx && m.focus == panelForwards
		text := forwardMarker(st.State) + " " + f.Label()
		if selected {
			lines = append(lines, row(text, true, w))
			continue
		}
		lines = append(lines, theme.Fg(forwardColour(st.State)).Render(forwardMarker(st.State))+
			theme.Normal.Render(" "+f.Label()))
	}
	return strings.Join(lines, "\n")
}

func forwardMarker(s forward.State) string {
	switch s {
	case forward.Running:
		return "▶"
	case forward.Starting, forward.Retrying:
		return "◌"
	case forward.Failed:
		return "✖"
	default:
		return "▪"
	}
}

func forwardColour(s forward.State) color.Color {
	switch s {
	case forward.Running:
		return theme.Green
	case forward.Starting, forward.Retrying:
		return theme.Yellow
	case forward.Failed:
		return theme.Red
	default:
		return theme.TextDim
	}
}

// forwardDetail renders the main pane when the Forwards panel has focus.
func (m Model) forwardDetail() (string, string) {
	f, ok := m.selectedForward()
	if !ok {
		return "Forward", "\n" + theme.Dim.Render("  no forward selected")
	}
	st := m.sup.Status(f.ID)
	h, _ := m.selectedHost()

	lines := []string{
		"",
		detailField("kind", f.Kind.String()+"  "+f.Kind.Flag()+" "+f.Spec(), ""),
		detailField("host", h.Name, ""),
		detailField("state", statusText(st), ""),
	}
	if st.Restarts > 0 {
		lines = append(lines, detailField("retries", strconv.Itoa(st.Restarts), ""))
	}
	if st.Err != "" {
		lines = append(lines, detailField("last", st.Err, ""))
	}

	resolved := m.d.resolver.Resolve(h)
	lines = append(lines,
		"",
		theme.Dim.Render("  command"),
		theme.Fg(theme.Green).Render("    ssh "+strings.Join(sshx.ForwardArgs(resolved.Host, f), " ")),
		"",
		theme.Dim.Render("  tunnels are children of omassh and close when it exits"),
	)
	return f.Label(), strings.Join(lines, "\n")
}

func statusText(st forward.Status) string {
	if st.State == forward.Running && !st.Since.IsZero() {
		return fmt.Sprintf("running for %s", dur(time.Since(st.Since)))
	}
	return st.State.String()
}
