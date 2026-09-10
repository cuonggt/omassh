package ui

import (
	"fmt"
	"image/color"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/cuonggt/omassh/internal/keymap"
	"github.com/cuonggt/omassh/internal/sshx"
	"github.com/cuonggt/omassh/internal/store"
	"github.com/cuonggt/omassh/internal/term"
	"github.com/cuonggt/omassh/internal/ui/theme"
)

// A forward is the one thing in Omassh with nothing to look at: it connects,
// binds a port, and then succeeds by doing nothing visible. So the interface
// has to say what the tunnel would otherwise say for itself — whether it is
// up, and what it was doing when it stopped.

// forwardRefresh is how often the open list re-asks tmux what is still up. A
// tunnel can drop while you are looking at it, and a screen that only told the
// truth at the moment it opened would be worse than no screen.
const forwardRefresh = 2 * time.Second

type forwardTickMsg struct{}

func forwardTick() tea.Cmd {
	return tea.Tick(forwardRefresh, func(time.Time) tea.Msg { return forwardTickMsg{} })
}

// forwardDoneMsg reports what became of a start or a stop, which for a start
// means waiting to see whether ssh stayed up.
type forwardDoneMsg struct {
	f       store.Forward
	stopped bool
	err     error
}

// --- opening -----------------------------------------------------------

func (m Model) openForwards() (tea.Model, tea.Cmd) {
	h, ok := m.selectedHost()
	if !ok {
		return m, nil
	}
	m.forwardHost = h
	m.forwardIdx = 0
	m.mode = modeForwards
	m.refreshForwards()

	n := len(m.d.forwardsFor(h.ID))
	if n == 0 {
		m.setStatus("no forwards on " + h.Name + " yet — n to add one")
	} else {
		m.setStatus(fmt.Sprintf("%d forward%s on %s", n, plural(n), h.Name))
	}
	return m, forwardTick()
}

func (m Model) closeForwards() (tea.Model, tea.Cmd) {
	m.mode = modeBrowse
	m.setStatus("")
	return m, nil
}

// refreshForwards re-asks tmux which tunnels are up, without re-reading the
// store. Nothing about the rules has changed; only what has become of them.
func (m *Model) refreshForwards() {
	if len(m.d.forwards) == 0 {
		return
	}
	if states, err := term.ForwardStates(); err == nil {
		m.d.fwd = states
	}
}

func (m Model) handleForwardTick() (tea.Model, tea.Cmd) {
	if m.mode != modeForwards {
		return m, nil
	}
	m.refreshForwards()
	return m, forwardTick()
}

// --- keys --------------------------------------------------------------

func (m Model) handleForwardsKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	fs := m.d.forwardsFor(m.forwardHost.ID)

	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc", "q":
		return m.closeForwards()
	case "j", "down":
		m.forwardIdx = clamp(m.forwardIdx+1, 0, len(fs)-1)
	case "k", "up":
		m.forwardIdx = clamp(m.forwardIdx-1, 0, len(fs)-1)
	case "g", "r":
		// The store as well as the tunnels. Everywhere else in the interface
		// r means "read the disk again", and that is the whole of how two
		// windows keep up with each other — but here it asked only tmux, so a
		// rule added in the other window stayed invisible until this view was
		// closed and opened again.
		m.reload()
		if _, ok := m.d.hostByID(m.forwardHost.ID); !ok {
			// Another window deleted it. Everything on this screen is about
			// that host, including the offer to add a rule to it — and a rule
			// saved against a host that has gone belongs to nothing.
			m.mode = modeBrowse
			m.setErr(fmt.Errorf("%s is gone — another window deleted it", m.forwardHost.Name))
			return m, nil
		}
		m.forwardIdx = clamp(m.forwardIdx, 0, len(m.d.forwardsFor(m.forwardHost.ID))-1)
		m.setStatus("refreshed")
	case "enter", " ", "space":
		return m.toggleForward()
	case "n":
		m.form = newForwardForm(store.Forward{}, m.forwardHost.Name)
		m.returnTo, m.mode = backFor(m.mode), modeForm
		return m, m.form.focusCurrent()
	case "e":
		f, ok := m.selectedForward()
		if !ok {
			return m, nil
		}
		m.form = newForwardForm(f, m.forwardHost.Name)
		m.returnTo, m.mode = backFor(m.mode), modeForm
		return m, m.form.focusCurrent()
	case "d":
		return m.askDeleteForward()
	}
	return m, nil
}

func (m Model) selectedForward() (store.Forward, bool) {
	fs := m.d.forwardsFor(m.forwardHost.ID)
	if len(fs) == 0 {
		return store.Forward{}, false
	}
	return fs[clamp(m.forwardIdx, 0, len(fs)-1)], true
}

// --- starting and stopping ---------------------------------------------

func (m Model) toggleForward() (tea.Model, tea.Cmd) {
	f, ok := m.selectedForward()
	if !ok {
		return m, nil
	}
	host := m.forwardTarget()
	st := m.d.forwardState(f)

	switch {
	case st.Running && forwardStale(host, f, st):
		// Stopping would answer the wrong question. What is running is not
		// this rule, and starting is what makes the two agree.
		m.setStatus("restarting " + f.Label() + " on the route it now says…")
		return m, startForward(host, f)
	case st.Running:
		if err := term.StopForward(f); err != nil {
			m.setErr(err)
			return m, nil
		}
		m.refreshForwards()
		m.setStatus("stopped " + f.Label())
		return m, nil
	}

	m.setStatus("starting " + f.Label() + "…")
	return m, startForward(host, f)
}

// forwardStale reports whether a running tunnel is carrying something other
// than what its rule now says.
//
// Editing a rule — or the host it belongs to — reaches nothing already
// running. The tunnel is named by the rule's id, so it goes on being found and
// reported as up, against a route it is not carrying: repointing a tunnel at
// staging and watching the interface agree, while every connection through it
// still lands on production.
//
// A tunnel started before this was recorded has no fingerprint, and must not
// be accused on the strength of that.
func forwardStale(target store.Host, f store.Forward, st term.ForwardState) bool {
	if !st.Running || st.Args == "" {
		return false
	}
	return st.Args != term.ForwardFingerprint(sshx.ForwardArgs(target, f))
}

// holderOf finds a tunnel of ours already bound to the port a rule wants.
//
// A remote rule binds its port on the host, so one of those is never what is
// holding a port here however alike the numbers look.
func (m Model) holderOf(f store.Forward) (store.Forward, string, bool) {
	for _, x := range m.d.forwards {
		if x.ID == f.ID || x.Kind == store.ForwardRemote {
			continue
		}
		if x.ListenPort == f.ListenPort && m.d.forwardState(x).Running {
			return x, m.d.hostName(x.HostID), true
		}
	}
	return store.Forward{}, "", false
}

// forwardTarget is the host a tunnel connects to: the selected host with its
// group inheritance applied, exactly as an interactive session would reach it.
//
// Resolving is not a formality. Unresolved, a host that takes its user or its
// key from a group would connect as neither, and one behind a bastion would be
// dialled directly — reaching whatever answers at that address from here, if
// anything does.
func (m Model) forwardTarget() store.Host {
	return m.d.resolver.Resolve(m.forwardHost).Host
}

// startForward hands the rule to ssh and waits to see whether it stayed up,
// which takes long enough to be worth doing off the interface's own goroutine.
func startForward(h store.Host, f store.Forward) tea.Cmd {
	return func() tea.Msg {
		return forwardDoneMsg{f: f, err: term.StartForward(f, sshx.ForwardArgs(h, f))}
	}
}

func (m Model) handleForwardDone(msg forwardDoneMsg) (tea.Model, tea.Cmd) {
	// The rule can go while its tunnel is coming up. Starting takes a second
	// or so, and a delete inside that window stopped a session that did not
	// exist yet — so what the start then created belonged to nothing: a tunnel
	// holding a port, with nothing in the interface able to see or stop it.
	if msg.err == nil && !msg.stopped {
		if _, ok := m.d.forwardByID(msg.f.ID); !ok {
			if err := term.StopForward(msg.f); err != nil {
				m.setErr(err)
				return m, nil
			}
			m.refreshForwards()
			m.setStatus("stopped " + msg.f.Label() + " — its rule went while it was starting")
			return m, nil
		}
	}

	m.refreshForwards()
	switch {
	case msg.err != nil:
		m.setErr(fmt.Errorf("%s: %s", msg.f.Label(), m.forwardAdvice(msg.f, msg.err.Error())))
	case msg.stopped:
		m.setStatus("stopped " + msg.f.Label())
	default:
		m.setStatus(msg.f.Label() + " is up")
	}
	return m, nil
}

func (m Model) askDeleteForward() (tea.Model, tea.Cmd) {
	f, ok := m.selectedForward()
	if !ok {
		return m, nil
	}
	detail := "nothing is running for it"
	if m.d.forwardState(f).Running {
		// Otherwise the tunnel keeps its port with nothing left in the
		// interface that can reach it.
		detail = "its tunnel is stopped first"
	}
	m.confirm = &confirmation{
		prompt: "Delete the forward " + f.Label() + "?",
		detail: detail,
		run: func() (string, error) {
			if err := term.StopForward(f); err != nil {
				return "", err
			}
			return "deleted the forward " + f.Label(), m.st.DeleteForward(f.ID)
		},
	}
	m.returnTo, m.mode = backFor(m.mode), modeConfirm
	return m, nil
}

// stopForwardsFor ends every tunnel belonging to a host, for when the host
// itself is going away. The store takes the rules with the host; the tunnels
// are ours to stop, since the store knows nothing about tmux.
func (m Model) stopForwardsFor(hostID string) error {
	for _, f := range m.d.forwardsFor(hostID) {
		if err := term.StopForward(f); err != nil {
			return err
		}
	}
	return nil
}

// --- the form ----------------------------------------------------------

func newForwardForm(f store.Forward, hostName string) *form {
	title := "New forward on " + hostName
	if f.ID != "" {
		title = "Edit forward on " + hostName
	}

	kind := string(f.Kind)
	if kind == "" {
		kind = string(store.ForwardLocal)
	}
	kinds := make([]string, 0, len(store.ForwardKinds))
	for _, k := range store.ForwardKinds {
		kinds = append(kinds, string(k))
	}

	listen, dest := "", ""
	if f.ListenPort != 0 {
		listen = f.ListenText()
	}
	if f.DestPort != 0 {
		dest = f.DestText()
	}

	return &form{
		kind: formForward, title: title, editID: f.ID,
		fields: []field{
			asSuggestion(withChoices(newField("Kind", "local, remote or dynamic — ↓ to pick", kind), kinds)),
			newField("Listen", "5432, or 127.0.0.1:5432", listen),
			newField("Destination", "db.internal:5432 — dynamic needs none", dest),
		},
	}
}

func (m Model) saveForwardForm() (tea.Model, tea.Cmd) {
	f := m.form

	fwd := store.Forward{
		ID:     f.editID,
		HostID: m.forwardHost.ID,
		Kind:   store.ForwardKind(strings.ToLower(f.value("Kind"))),
	}

	var err error
	if fwd.Listen, fwd.ListenPort, err = store.ParseListen(f.value("Listen")); err != nil {
		f.problem = err.Error()
		return m, nil
	}
	if fwd.Kind != store.ForwardDynamic {
		if fwd.Dest, fwd.DestPort, err = store.ParseDest(f.value("Destination")); err != nil {
			f.problem = err.Error()
			return m, nil
		}
	}
	if _, err := m.st.PutForward(fwd); err != nil {
		f.problem = err.Error()
		return m, nil
	}

	m.form, m.mode = nil, modeForwards
	m.reload()
	m.selectForward(fwd)
	m.setStatus("saved " + fwd.Label() + " — ↵ starts it")
	return m, nil
}

// selectForward puts the cursor on a rule, so one just saved is the one the
// next key acts on rather than whichever happens to sort first.
func (m *Model) selectForward(f store.Forward) {
	for i, x := range m.d.forwardsFor(m.forwardHost.ID) {
		if x.Spec() == f.Spec() && x.Kind == f.Kind {
			m.forwardIdx = i
			return
		}
	}
}

// --- rendering ---------------------------------------------------------

// forwardMarker is the mark beside a rule: up, up but carrying something other
// than what the rule says, stopped deliberately, and stopped because it
// failed.
func forwardMarker(st term.ForwardState, known, stale bool) (string, color.Color) {
	switch {
	case st.Running && stale:
		// Hollow, the way ○ marks a host nothing is known about: something is
		// running, but not what this line describes.
		return "▷", theme.Yellow
	case st.Running:
		return "▶", theme.Green
	case known && st.Failed():
		return "✖", theme.Red
	default:
		return "■", theme.TextDim
	}
}

// tmuxAvailable is what the forwards screens describe the machine as. The
// option carries it so a test can be a machine without tmux without editing
// the environment the whole run shares; nil is the real answer.
func (m Model) tmuxAvailable() bool {
	if m.opts.TmuxAvailable != nil {
		return m.opts.TmuxAvailable()
	}
	return term.TmuxAvailable()
}

// forwardListRows is how many rules the dialog can draw, given the frame.
//
// Everything else the box holds is fixed: its two borders, a blank line above
// the list, and beneath it a blank, whatever the selected rule has to say, a
// blank and the key hints. What is left is the list.
func (m Model) forwardListRows(content int) int {
	const fixed = 2 + 1 + 1 + 1 + 1 // borders, blank above, blank, blank, hints
	return max(content-fixed-len(m.forwardDetail()), 1)
}

func (m Model) forwardsBody(w, content int) string {
	fs := m.d.forwardsFor(m.forwardHost.ID)
	if len(fs) == 0 {
		// What is said here has to be true of this machine. Describing the
		// tmux server a tunnel runs on, where there is no tmux to run one,
		// promises the whole point of the feature to someone who will only
		// find out otherwise after writing a rule and pressing start.
		why := []string{
			"a tunnel runs on omassh's tmux server, so it",
			"outlives the window that started it",
		}
		if !m.tmuxAvailable() {
			why = []string{
				"port forwarding needs tmux, which is not installed —",
				"without it a tunnel would die with omassh and still",
				"look like one that is up",
			}
		}
		out := "\n  " + theme.Dim.Render("no forwards on "+m.forwardHost.Name+" yet") + "\n"
		for _, l := range why {
			out += "\n  " + theme.Dim.Render(l)
		}
		return out + "\n\n  " + hint("n", "new") + sep() + hint("esc", "close")
	}

	// Windowed, the way every other list here is. Drawing all of them let the
	// box grow past the frame, which cut the rules off the bottom along with
	// the line saying what the selected one is doing — and the selection could
	// be among what was cut, so ↵ acted on a rule nothing on screen showed.
	start, end := listWindow(m.forwardIdx, len(fs), m.forwardListRows(content))

	lines := []string{""}
	for i := start; i < end; i++ {
		f := fs[i]
		st, known := m.d.forwardStatus(f)
		mark, col := forwardMarker(st, known, forwardStale(m.forwardTarget(), f, st))
		text := fmt.Sprintf("%s %s %s", mark, pad(string(f.Kind), 7), f.Route())

		if i == m.forwardIdx {
			lines = append(lines, "  "+row(text, true, w-2))
			continue
		}
		lines = append(lines, "  "+theme.Fg(col).Render(mark)+
			theme.Dim.Render(" "+pad(string(f.Kind), 7))+theme.Normal.Render(" "+f.Route()))
	}

	// What the selected rule is doing, on lines of their own: a reason is often
	// a whole sentence from ssh, and putting it beside the rule would push the
	// rule itself off the edge.
	lines = append(lines, "")
	for _, l := range m.forwardDetail() {
		lines = append(lines, "  "+l)
	}

	lines = append(lines, "", "  "+hint("↵", "start/stop")+sep()+
		hint("n/e/d", "new/edit/delete")+sep()+hint("esc", "close"))
	return strings.Join(lines, "\n")
}

// forwardDetail says what has become of the selected rule, in words. It is a
// slice because a failure often needs a second line: what ssh said, and then
// what to do about it.
func (m Model) forwardDetail() []string {
	f, ok := m.selectedForward()
	if !ok {
		return nil
	}
	st, known := m.d.forwardStatus(f)
	switch {
	case st.Running && forwardStale(m.forwardTarget(), f, st):
		// Nothing here names one thing as the cause. The fingerprint covers the
		// whole invocation, so a tunnel goes out of date when the rule, the
		// host, or a group the host inherits from changes — and naming any one
		// of those sends someone to look at what they did not touch. Short
		// enough, too: an earlier wording ran past the dialog and lost the half
		// that says what to do.
		return []string{
			theme.Fg(theme.Yellow).Render("running what it was started with"),
			theme.Dim.Render("↳ the rule or how it connects has changed — ↵ restarts it"),
		}
	case st.Running:
		return []string{theme.Fg(theme.Green).Render("running") + theme.Dim.Render("  ↵ stops it")}
	case !known && !m.tmuxAvailable():
		// ↵ cannot start it, so it must not be offered as though it could.
		return []string{
			theme.Fg(theme.Yellow).Render("port forwarding needs tmux"),
			theme.Dim.Render("↳ without it a dead tunnel would look like a live one"),
		}
	case !known:
		// Nothing is running: either it never was, or stopping it cleared the
		// session away. "Not started" was a small lie the moment after you
		// pressed stop, and this is true of both.
		return []string{theme.Dim.Render("not running  ·  ↵ starts it")}
	case !st.Failed():
		return []string{theme.Dim.Render("stopped  ·  ↵ starts it again")}
	}

	reason := term.FailureReason(term.ForwardSessionName(f), st)
	out := []string{theme.Fg(theme.Red).Render("stopped: ") + theme.Normal.Render(reason)}
	// On its own line rather than after the reason: the box truncates, and a
	// hint cut off at the edge is exactly the part worth reading.
	if advice := m.forwardAdvice(f, reason); advice != reason {
		out = append(out, theme.Dim.Render("↳ "+strings.TrimPrefix(advice, reason+" — ")))
	}
	return out
}

// forwardAdvice adds what to do about the two failures BatchMode produces.
//
// A tunnel runs where nobody can answer a prompt, so ssh is told not to ask —
// and the two questions it would have asked come back as statements instead.
// Both are accurate and both are dead ends: neither is fixed on this screen,
// and neither says so.
func (m Model) forwardAdvice(f store.Forward, reason string) string {
	switch {
	case strings.Contains(reason, "already in use"):
		// Far more often than not it is one of these rules, sitting on the
		// same screen with a ▶ against it. Saying which turns a sentence that
		// sends someone to lsof into one that points a line up.
		if other, host, ok := m.holderOf(f); ok {
			return reason + " — by " + host + "'s " + other.Route()
		}
	case strings.Contains(reason, "Host key verification failed"):
		return reason + " — " + m.keys.Key(keymap.Connect) + " on the host once to accept its key"
	case strings.Contains(reason, "Permission denied"), strings.Contains(reason, "publickey"):
		return reason + " — add the key to your ssh-agent first (ssh-add)"
	}
	return reason
}
