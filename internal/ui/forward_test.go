package ui

import (
	"strings"
	"testing"

	"github.com/cuonggt/omassh/internal/sshx"
	"github.com/cuonggt/omassh/internal/store"
	"github.com/cuonggt/omassh/internal/term"
)

// running pretends tmux has a tunnel up for a rule, so the interface can be
// asked what it draws without starting one.
func (h *harness) running(f store.Forward) {
	h.t.Helper()
	if h.m.d.fwd == nil {
		h.m.d.fwd = map[string]term.ForwardState{}
	}
	h.m.d.fwd[term.ForwardSessionName(f)] = term.ForwardState{Running: true}
}

func (h *harness) stopped(f store.Forward, exit int) {
	h.t.Helper()
	if h.m.d.fwd == nil {
		h.m.d.fwd = map[string]term.ForwardState{}
	}
	h.m.d.fwd[term.ForwardSessionName(f)] = term.ForwardState{Exit: exit}
}

// addForward writes a rule directly, for tests that need one without driving
// the form.
func (h *harness) addForward(hostID string, port int, dest string, destPort int) store.Forward {
	h.t.Helper()
	f, err := h.store.PutForward(store.Forward{
		HostID: hostID, Kind: store.ForwardLocal,
		ListenPort: port, Dest: dest, DestPort: destPort,
	})
	if err != nil {
		h.t.Fatalf("put forward: %v", err)
	}
	h.reload()
	return f
}

func TestForwardsOpenOnTheSelectedHost(t *testing.T) {
	h := newHarness(t)
	host := h.addHost("db-01", "10.0.0.1")
	h.addForward(host.ID, 5432, "localhost", 5432)
	h.selectHost("db-01")

	h.press("f")
	if h.m.mode != modeForwards {
		t.Fatalf("f did not open the forwards view, got mode %v", h.m.mode)
	}
	h.mustContain("Forwards on db-01")
	h.mustContain("5432 → localhost:5432")

	h.press("esc")
	if h.m.mode != modeBrowse {
		t.Errorf("esc did not close the forwards view, got mode %v", h.m.mode)
	}
}

// A rule made in the interface is a rule in the store, spelled the way ssh
// spells it.
func TestNewForwardThroughTheForm(t *testing.T) {
	h := newHarness(t)
	host := h.addHost("db-01", "10.0.0.1")
	h.selectHost("db-01")

	h.press("f", "n")
	if h.m.mode != modeForm {
		t.Fatalf("n did not open a form, got mode %v", h.m.mode)
	}
	h.press("tab")
	h.type_("5432")
	h.press("tab")
	h.type_("db.internal:5432")
	h.press("enter")

	if h.m.mode != modeForwards {
		t.Fatalf("saving did not return to the forwards view, got mode %v", h.m.mode)
	}
	fs, err := h.store.Forwards()
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) != 1 {
		t.Fatalf("got %d rules, want 1", len(fs))
	}
	got := fs[0]
	if got.HostID != host.ID {
		t.Errorf("the rule belongs to %q, want the selected host %q", got.HostID, host.ID)
	}
	if got.Kind != store.ForwardLocal {
		t.Errorf("kind = %q, want the default", got.Kind)
	}
	if got.Spec() != "5432:db.internal:5432" {
		t.Errorf("spec = %q", got.Spec())
	}
}

// A destination is required, and saying so in the form beats handing ssh a
// spec it will reject inside a session nobody is watching.
func TestForwardFormRefusesAnIncompleteRule(t *testing.T) {
	h := newHarness(t)
	h.addHost("db-01", "10.0.0.1")
	h.selectHost("db-01")

	h.press("f", "n", "tab")
	h.type_("5432")
	h.press("enter")

	if h.m.mode != modeForm {
		t.Fatalf("an incomplete rule was accepted, mode is %v", h.m.mode)
	}
	if h.m.form.problem == "" {
		t.Error("nothing was said about what is missing")
	}
	if fs, _ := h.store.Forwards(); len(fs) != 0 {
		t.Errorf("an incomplete rule was written: %+v", fs)
	}
}

// A dynamic forward decides its destination per connection, so the form must
// not demand one.
func TestDynamicForwardNeedsNoDestination(t *testing.T) {
	h := newHarness(t)
	h.addHost("db-01", "10.0.0.1")
	h.selectHost("db-01")

	h.press("f", "n", "down") // the kind picker
	h.pickUntil("dynamic")
	h.press("enter") // take the choice
	h.press("tab")
	h.type_("1080")
	h.press("enter")

	fs, _ := h.store.Forwards()
	if len(fs) != 1 {
		t.Fatalf("got %d rules, want 1", len(fs))
	}
	if fs[0].Kind != store.ForwardDynamic || fs[0].Spec() != "1080" {
		t.Errorf("got %+v", fs[0])
	}
}

// The rules belong to the host, and the pane describing a host is where they
// have to appear: a tunnel runs whether or not anyone is connected, so there
// is otherwise nothing to see.
func TestTheDetailPaneListsForwards(t *testing.T) {
	h := newHarness(t)
	host := h.addHost("db-01", "10.0.0.1")
	h.addForward(host.ID, 5432, "localhost", 5432)
	h.selectHost("db-01")

	h.mustContain("forwards")
	h.mustContain("5432 → localhost:5432")
}

// A running tunnel is marked in the list, which is the only place it is
// visible without going looking for it.
func TestARunningTunnelIsMarkedInTheList(t *testing.T) {
	h := newHarness(t)
	host := h.addHost("db-01", "10.0.0.1")
	f := h.addForward(host.ID, 5432, "localhost", 5432)
	h.selectHost("db-01")

	if strings.Contains(h.screen(), "db-01 ▶") {
		t.Error("a host with nothing running is already marked")
	}
	h.running(f)
	h.mustContain("db-01 ▶")
}

// What ssh said is the useful part of a tunnel that stopped; the interface
// keeps the stopped session precisely so it can be asked.
func TestAStoppedTunnelIsDistinguishedFromOneNeverStarted(t *testing.T) {
	h := newHarness(t)
	host := h.addHost("db-01", "10.0.0.1")
	f := h.addForward(host.ID, 5432, "localhost", 5432)
	h.selectHost("db-01")

	h.press("f")
	h.mustContain("not running")

	h.stopped(f, 255)
	if !strings.Contains(h.screen(), "stopped") {
		t.Errorf("a tunnel that failed reads as never started:\n%s", h.screen())
	}
}

// A rule names a host and means nothing without it, so deleting the host takes
// the rules with it rather than leaving tunnels nothing can reach.
func TestDeletingAHostSaysItsForwardsGoToo(t *testing.T) {
	h := newHarness(t)
	host := h.addHost("db-01", "10.0.0.1")
	h.addForward(host.ID, 5432, "localhost", 5432)
	h.selectHost("db-01")

	h.press("d")
	if h.m.mode != modeConfirm {
		t.Fatalf("d did not ask, mode is %v", h.m.mode)
	}
	// One rule reads as one rule. Counting into a plural phrase produced
	// "1 forward and any tunnel of theirs", which is the commonest way for a
	// sentence assembled from parts to come out wrong.
	h.mustContain("its forward goes too")
	h.mustNotContain("theirs")

	h.press("y")
	if fs, _ := h.store.Forwards(); len(fs) != 0 {
		t.Errorf("deleting the host left its rules: %+v", fs)
	}
}

// And the plural says so as a plural.
func TestDeletingAHostWithSeveralForwards(t *testing.T) {
	h := newHarness(t)
	host := h.addHost("db-01", "10.0.0.1")
	h.addForward(host.ID, 5432, "localhost", 5432)
	h.addForward(host.ID, 8080, "localhost", 80)
	h.selectHost("db-01")

	h.press("d")
	h.mustContain("its 2 forwards go too")
}

// Deleting a rule asks first, the way deleting anything else does.
func TestDeletingAForward(t *testing.T) {
	h := newHarness(t)
	host := h.addHost("db-01", "10.0.0.1")
	h.addForward(host.ID, 5432, "localhost", 5432)
	h.selectHost("db-01")

	h.press("f", "d")
	if h.m.mode != modeConfirm {
		t.Fatalf("d did not ask, mode is %v", h.m.mode)
	}
	h.press("y")
	if fs, _ := h.store.Forwards(); len(fs) != 0 {
		t.Errorf("the rule survived: %+v", fs)
	}
	// And it lands back on the list it was deleted from, not the host list.
	if h.m.mode != modeForwards {
		t.Errorf("confirming returned to mode %v, want the forwards view", h.m.mode)
	}
}

// Editing keeps the id, because that is what the running tunnel is named
// after: a new id would leave the old tunnel holding its port unreachably.
func TestEditingAForwardKeepsItsID(t *testing.T) {
	h := newHarness(t)
	host := h.addHost("db-01", "10.0.0.1")
	f := h.addForward(host.ID, 5432, "localhost", 5432)
	h.selectHost("db-01")

	h.press("f", "e", "tab")
	for range len("5432") {
		h.press("backspace")
	}
	h.type_("15432")
	h.press("enter")

	fs, _ := h.store.Forwards()
	if len(fs) != 1 {
		t.Fatalf("got %d rules, want 1", len(fs))
	}
	if fs[0].ID != f.ID {
		t.Errorf("the id changed: %q, was %q", fs[0].ID, f.ID)
	}
	if fs[0].ListenPort != 15432 {
		t.Errorf("listen port = %d, want 15432", fs[0].ListenPort)
	}
}

// A tunnel reaches its host the way a session does, group inheritance and all.
// Started from the unresolved host it would connect as the wrong user, with
// the wrong key, and — worse — dial a bastioned address directly rather than
// through the bastion.
func TestATunnelInheritsFromItsGroups(t *testing.T) {
	h := newHarness(t)
	bastion := h.addHost("bastion", "edge.example.com")
	g, err := h.store.PutGroup(store.Group{Name: "Production", User: "admin",
		Identity: "~/.ssh/prod", ProxyJump: bastion.Name})
	if err != nil {
		t.Fatal(err)
	}
	h.reload()
	host := h.addGroupedHost("db-01", g.ID)
	h.addForward(host.ID, 5432, "localhost", 5432)
	h.selectHost("db-01")
	h.press("f")

	target := h.m.forwardTarget()
	if target.User != "admin" {
		t.Errorf("user = %q, want the group's", target.User)
	}
	if target.Identity != "~/.ssh/prod" {
		t.Errorf("identity = %q, want the group's", target.Identity)
	}
	if target.Jump == nil {
		t.Fatal("the tunnel would be dialled directly, bypassing the bastion")
	}
	if target.Jump.Addr != "edge.example.com" {
		t.Errorf("jump host = %q", target.Jump.Addr)
	}
}

// A tunnel is told not to prompt, so the two questions ssh would have asked
// come back as statements. Both are accurate and both are dead ends: what to
// do about them is on this screen or nowhere.
func TestAFailedTunnelSaysWhatToDo(t *testing.T) {
	h := newHarness(t)
	for _, c := range []struct{ reason, want string }{
		{"Host key verification failed.", "accept its key"},
		{"tester@10.0.0.1: Permission denied (publickey).", "ssh-add"},
	} {
		got := h.m.forwardAdvice(c.reason)
		if !strings.Contains(got, c.want) {
			t.Errorf("advice for %q = %q, want it to mention %q", c.reason, got, c.want)
		}
		if !strings.Contains(got, c.reason) {
			t.Errorf("advice for %q dropped what ssh said: %q", c.reason, got)
		}
	}
	// Anything else is left exactly as ssh wrote it, rather than guessed at.
	plain := "ssh: connect to host 10.0.0.1 port 22: Connection refused"
	if got := h.m.forwardAdvice(plain); got != plain {
		t.Errorf("a reason with no known remedy was embellished: %q", got)
	}
}

// Space is the other way to start a tunnel, and a key press for it stringifies
// to "space" rather than to a literal space — so matching only the literal
// left the key doing nothing at all.
func TestSpaceStartsATunnelAsEnterDoes(t *testing.T) {
	for _, key := range []string{"enter", "space"} {
		h := newHarness(t)
		host := h.addHost("db-01", "10.0.0.1")
		h.addForward(host.ID, 5432, "localhost", 5432)
		h.selectHost("db-01")
		h.press("f", key)

		if !strings.Contains(h.m.status, "starting") {
			t.Errorf("%q did not start the tunnel; status is %q", key, h.m.status)
		}
	}
}

// Local and remote bind the same port number on opposite machines, so two
// rules differing only in kind are two different tunnels. Named without it
// they drew as one line twice — which is what a host with both actually
// showed.
func TestTwoRulesDifferingOnlyInDirectionAreToldApart(t *testing.T) {
	h := newHarness(t)
	host := h.addHost("db-01", "10.0.0.1")
	for _, kind := range []store.ForwardKind{store.ForwardLocal, store.ForwardRemote} {
		if _, err := h.store.PutForward(store.Forward{
			HostID: host.ID, Kind: kind,
			ListenPort: 5432, Dest: "db.internal", DestPort: 5432,
		}); err != nil {
			t.Fatal(err)
		}
	}
	h.reload()
	h.selectHost("db-01")

	// The detail pane, where both are listed side by side.
	for _, want := range []string{"local", "remote"} {
		if !strings.Contains(h.screen(), want) {
			t.Errorf("the detail pane does not say %q:\n%s", want, h.screen())
		}
	}
	if n := strings.Count(h.screen(), "5432 → db.internal:5432"); n != 2 {
		t.Fatalf("expected both rules listed, found %d", n)
	}

	// And naming one on its own — a status message, a confirmation — carries
	// the kind, or it could mean either rule.
	fs, _ := h.store.Forwards()
	for _, f := range fs {
		if !strings.Contains(f.Label(), string(f.Kind)) {
			t.Errorf("Label() = %q, which does not say which direction it is", f.Label())
		}
	}
}

// A tunnel carries what it was started with. Editing the rule under it left
// the interface reporting the new route as running while every connection went
// to the old one — the shape of mistake where repointing at staging keeps
// reaching production, and the screen agrees with you.
func TestATunnelRunningAnOlderRuleIsNotClaimedAsCurrent(t *testing.T) {
	h := newHarness(t)
	host := h.addHost("db-01", "10.0.0.1")
	f := h.addForward(host.ID, 5432, "old.internal", 5432)
	h.selectHost("db-01")
	target := h.m.d.resolver.Resolve(host).Host

	// Opening the view re-asks tmux, so the state under test goes in after.
	h.press("f")
	h.m.d.fwd = map[string]term.ForwardState{
		term.ForwardSessionName(f): {
			Running: true,
			Args:    term.ForwardFingerprint(sshx.ForwardArgs(target, f)),
		},
	}
	if forwardStale(target, f, h.m.d.forwardState(f)) {
		t.Fatal("a tunnel carrying its own rule was called stale")
	}
	h.mustContain("↵ stops it")
	h.mustNotContain("what it was started with")

	// Now the rule points somewhere else. The tunnel has not moved, and the
	// screen must not say it has.
	moved := f
	moved.Dest = "new.internal"
	if _, err := h.store.PutForward(moved); err != nil {
		t.Fatal(err)
	}
	was := h.m.d.fwd
	h.reload()
	h.m.d.fwd = was // the tunnel is still the one that was started

	if !forwardStale(target, moved, h.m.d.forwardState(f)) {
		t.Fatal("a tunnel carrying the old route was reported as carrying the new one")
	}
	h.mustContain("what it was started with")
	h.mustNotContain("↵ stops it")

	// A tunnel from before this was recorded says nothing about itself, and
	// must not be accused on the strength of that.
	if forwardStale(target, moved, term.ForwardState{Running: true}) {
		t.Error("a tunnel with no fingerprint was called stale")
	}
}

// And ↵ on such a tunnel restarts it rather than merely stopping it: what is
// running is not this rule, so starting is what makes the two agree.
func TestRestartingATunnelRunningAnOlderRule(t *testing.T) {
	h := newHarness(t)
	host := h.addHost("db-01", "10.0.0.1")
	f := h.addForward(host.ID, 5432, "old.internal", 5432)
	h.selectHost("db-01")

	h.press("f")
	h.m.d.fwd = map[string]term.ForwardState{
		term.ForwardSessionName(f): {Running: true, Args: "not-what-this-rule-says"},
	}
	h.press("enter")

	if !strings.Contains(h.m.status, "restarting") {
		t.Errorf("status is %q, want it to say the tunnel is being restarted", h.m.status)
	}
}

// The dialog truncates, so what a stale tunnel says has to fit inside it. The
// first wording ran past the edge and lost the half naming the remedy.
func TestTheStaleNoticeFitsTheDialog(t *testing.T) {
	h := newHarness(t)
	host := h.addHost("db-01", "10.0.0.1")
	f := h.addForward(host.ID, 5432, "old.internal", 5432)
	h.selectHost("db-01")
	h.press("f")
	h.m.d.fwd = map[string]term.ForwardState{
		term.ForwardSessionName(f): {Running: true, Args: "not-what-this-rule-says"},
	}

	// The dialog is 64 wide with a two-cell border and a two-space indent.
	const usable = 64 - 4 - 2
	for _, line := range h.m.forwardDetail() {
		if w := ansiWidth(line); w > usable {
			t.Errorf("a line %d cells wide will be cut short at %d: %q", w, usable, line)
		}
	}
	// And the remedy actually reaches the screen.
	h.mustContain("↵ restarts it")
}

// On a machine with no tmux there is no tunnel to be had, and the screen has
// to say so where it can be read — not after a rule has been written and
// started. It used to describe the tmux server a tunnel runs on, to someone
// who had no tmux to run one.
func TestWithoutTmuxTheScreenSaysSo(t *testing.T) {
	// LookPath is how availability is decided, so an empty PATH is a machine
	// without it.
	t.Setenv("PATH", t.TempDir())

	h := newHarness(t)
	host := h.addHost("db-01", "10.0.0.1")
	h.selectHost("db-01")

	h.press("f")
	h.mustContain("needs tmux")
	h.mustNotContain("outlives the window")

	// And a rule that exists is not offered as one ↵ could start.
	h.press("esc")
	h.addForward(host.ID, 5432, "localhost", 5432)
	h.selectHost("db-01")
	h.press("f")
	h.mustContain("needs tmux")
	h.mustNotContain("↵ starts it")
}

// With tmux there, none of that appears.
func TestWithTmuxTheOfferStands(t *testing.T) {
	if !term.TmuxAvailable() {
		t.Skip("tmux not installed, so there is no contrast to draw")
	}
	h := newHarness(t)
	host := h.addHost("db-01", "10.0.0.1")
	h.addForward(host.ID, 5432, "localhost", 5432)
	h.selectHost("db-01")

	h.press("f")
	h.mustContain("↵ starts it")
	h.mustNotContain("needs tmux")
}

// Two windows keep up with each other by reading the disk again, and r is how
// that is asked for. In this view it asked only tmux, so a rule added in the
// other window was invisible until the view was closed and reopened.
func TestReloadInTheForwardsViewSeesAnotherWindowsRule(t *testing.T) {
	h := newHarness(t)
	host := h.addHost("db-01", "10.0.0.1")
	h.addForward(host.ID, 5432, "localhost", 5432)
	h.selectHost("db-01")
	h.press("f")
	h.mustNotContain("8080 → localhost:80")

	// Written straight to the store, the way another window would.
	if _, err := h.store.PutForward(store.Forward{
		HostID: host.ID, Kind: store.ForwardLocal,
		ListenPort: 8080, Dest: "localhost", DestPort: 80,
	}); err != nil {
		t.Fatal(err)
	}

	h.press("r")
	h.mustContain("8080 → localhost:80")
	h.mustContain("5432 → localhost:5432")
}
