package ui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/cuonggt/omassh/internal/sftpx"
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
		got := h.m.forwardAdvice(store.Forward{}, c.reason)
		if !strings.Contains(got, c.want) {
			t.Errorf("advice for %q = %q, want it to mention %q", c.reason, got, c.want)
		}
		if !strings.Contains(got, c.reason) {
			t.Errorf("advice for %q dropped what ssh said: %q", c.reason, got)
		}
	}
	// Anything else is left exactly as ssh wrote it, rather than guessed at.
	plain := "ssh: connect to host 10.0.0.1 port 22: Connection refused"
	if got := h.m.forwardAdvice(store.Forward{}, plain); got != plain {
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

// withTmux says whether the interface should believe tmux is installed.
func withTmux(ok bool) func(*Options) {
	return func(o *Options) { o.TmuxAvailable = func() bool { return ok } }
}

// On a machine with no tmux there is no tunnel to be had, and the screen has
// to say so where it can be read — not after a rule has been written and
// started. It used to describe the tmux server a tunnel runs on, to someone
// who had no tmux to run one.
func TestWithoutTmuxTheScreenSaysSo(t *testing.T) {
	h := newHarness(t, withTmux(false))
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

// With tmux there, none of that appears. Both halves of the contrast are
// drawn from what the model was told rather than from what the machine has, so
// the pair says the same thing on a build agent without tmux as on a laptop
// with it — and neither half decides it by reaching for the process PATH.
func TestWithTmuxTheOfferStands(t *testing.T) {
	h := newHarness(t, withTmux(true))
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

// Every other list here is windowed to the frame. Drawing all the rules let
// the box grow past it, and box() cut the overflow off — taking with it the
// line saying what the selected rule was doing, the keys, and often the
// selected rule itself, which ↵ then acted on unseen.
func TestTheForwardsListWindowsToTheFrame(t *testing.T) {
	h := newHarness(t)
	host := h.addHost("db-01", "10.0.0.1")
	const n = 40
	for i := range n {
		if _, err := h.store.PutForward(store.Forward{
			HostID: host.ID, Kind: store.ForwardLocal,
			ListenPort: 6000 + i, Dest: "localhost", DestPort: 80,
		}); err != nil {
			t.Fatal(err)
		}
	}
	h.reload()
	h.selectHost("db-01")
	h.press("f")

	// The first rule, what it is doing, and the keys are all on screen.
	h.mustContain("6000 → localhost:80")
	h.mustContain("↵ starts it")
	h.mustContain("esc")

	// Walk to the last one: it has to come into view, along with everything
	// the box says beneath it.
	for range n - 1 {
		h.press("j")
	}
	last := fmt.Sprintf("%d → localhost:80", 6000+n-1)
	h.mustContain(last)
	h.mustContain("↵ starts it")
	h.mustContain("esc")
	// And the first is now out of view, or nothing scrolled at all.
	h.mustNotContain("6000 → localhost:80")

	// The count says there is more than fits, since the border no longer can.
	h.mustContain(fmt.Sprintf("%d/%d", n, n))
}

// The view is about one host, and holds on to it. When another window deletes
// that host, everything on the screen is about something that is not there —
// including the offer to add a rule to it, which would belong to nothing.
func TestTheForwardsViewNoticesItsHostIsGone(t *testing.T) {
	h := newHarness(t)
	host := h.addHost("db-01", "10.0.0.1")
	h.addForward(host.ID, 5432, "localhost", 5432)
	h.selectHost("db-01")
	h.press("f")
	h.mustContain("Forwards on db-01")

	// Deleted the way another window would: straight in the store.
	if err := h.store.DeleteHost(host.ID); err != nil {
		t.Fatal(err)
	}

	h.press("r")
	if h.m.mode == modeForwards {
		t.Error("the view stayed open on a host that is gone")
	}
	if !strings.Contains(h.m.status, "gone") {
		t.Errorf("status is %q, want it to say the host is gone", h.m.status)
	}
	h.mustNotContain("no forwards on db-01 yet")
}

// And a rule saved against a host that has gone is refused where it is
// written, since that is the only place the two cannot come apart.
func TestSavingAForwardForADeletedHostIsRefused(t *testing.T) {
	h := newHarness(t)
	host := h.addHost("db-01", "10.0.0.1")
	h.selectHost("db-01")
	h.press("f", "n")

	if err := h.store.DeleteHost(host.ID); err != nil {
		t.Fatal(err)
	}
	h.press("tab")
	h.type_("5432")
	h.press("tab")
	h.type_("db.internal:5432")
	h.press("enter")

	if h.m.mode != modeForm {
		t.Fatalf("the rule was accepted; mode is %v", h.m.mode)
	}
	if h.m.form.problem == "" {
		t.Error("nothing was said about why it was refused")
	}
	if fs, _ := h.store.Forwards(); len(fs) != 0 {
		t.Errorf("an orphan was written: %+v", fs)
	}
}

// The fingerprint is of the whole invocation, so a tunnel goes out of date
// when a group the host inherits from changes, not only the rule or the host.
// A jump host supplied by a group is the case that matters: the tunnel goes on
// travelling through a bastion the group no longer names.
func TestAGroupChangeMakesATunnelStale(t *testing.T) {
	h := newHarness(t)
	bastion := h.addHost("bastion", "edge.example.com")
	g, err := h.store.PutGroup(store.Group{Name: "Prod", ProxyJump: bastion.Name})
	if err != nil {
		t.Fatal(err)
	}
	h.reload()
	host := h.addGroupedHost("db-01", g.ID)
	f := h.addForward(host.ID, 5432, "localhost", 5432)
	h.selectHost("db-01")

	// Running exactly what the rule resolves to today.
	started := term.ForwardFingerprint(sshx.ForwardArgs(h.m.d.resolver.Resolve(host).Host, f))
	h.press("f")
	h.m.d.fwd = map[string]term.ForwardState{
		term.ForwardSessionName(f): {Running: true, Args: started},
	}
	h.mustContain("↵ stops it")

	// The group drops the jump host. Nothing about the rule or the host
	// changed, but what the tunnel would be started with did.
	g.ProxyJump = ""
	if _, err := h.store.PutGroup(g); err != nil {
		t.Fatal(err)
	}
	was := h.m.d.fwd
	h.reload()
	h.m.d.fwd = was

	if !forwardStale(h.m.forwardTarget(), f, h.m.d.forwardState(f)) {
		t.Fatal("a tunnel still going through the group's old bastion was reported as current")
	}
	h.mustContain("what it was started with")
}

// The marks are the only thing a host row says that is not in the name: a
// session waiting, a tunnel up. Truncating the composed line dropped them off
// the end, so a long name hid the very thing they were there to show — the
// tunnel went on running with nothing on screen saying so.
func TestALongNameDoesNotEatTheMarks(t *testing.T) {
	h := newHarness(t)
	host := h.addHost("prod-be-delivery-console-eu-west-1", "10.0.0.1")
	f := h.addForward(host.ID, 5432, "localhost", 5432)
	h.selectHost("prod-be-delivery-console-eu-west-1")
	h.m.d.fwd = map[string]term.ForwardState{
		term.ForwardSessionName(f): {Running: true},
	}

	// Selected and unselected rows are composed separately, so both are asked.
	h.mustContain("▶")
	h.m.focus = panelGroups
	h.mustContain("▶")

	// And it is the name that gave way: the host row carries an elision and
	// still ends with the mark.
	var row string
	for _, line := range strings.Split(h.screen(), "\n") {
		// The mark identifies the host row: the detail pane's title carries the
		// name too, on a line that also holds the sidebar's border.
		if strings.Contains(line, "○ prod-be-delivery") {
			row = line
			break
		}
	}
	if row == "" {
		t.Fatalf("no host row found:\n%s", h.screen())
	}
	if !strings.Contains(row, "…") {
		t.Errorf("the name was not elided though the marks needed the room: %q", row)
	}
	if !strings.Contains(row, "▶") {
		t.Errorf("the mark did not survive on the host row: %q", row)
	}
}

// A host can have both at once: a session waiting and a tunnel up. They are
// separate tmux sessions under separate names, and the row has to show both —
// neither is derivable from the other, and each is the only sign of its own.
func TestASessionAndATunnelBothMarkTheRow(t *testing.T) {
	h := newHarness(t)
	host := h.addHost("db-01", "10.0.0.1")
	f := h.addForward(host.ID, 5432, "localhost", 5432)
	h.selectHost("db-01")

	h.mustNotContain("db-01 ●")
	h.mustNotContain("db-01 ▶")

	h.m.d.live = map[string]bool{sessionNameFor(host): true}
	h.m.d.fwd = map[string]term.ForwardState{
		term.ForwardSessionName(f): {Running: true},
	}
	h.mustContain("db-01 ● ▶")

	// And the two names cannot be the same string, or stopping one would end
	// the other.
	if sessionNameFor(host) == term.ForwardSessionName(f) {
		t.Error("a host's session and its tunnel share a name")
	}
}

// A tunnel something killed is a tunnel that failed, and must not draw as one
// that was stopped on purpose: tmux reports a signal death with an empty exit
// status, which scored as a clean zero.
func TestAKilledTunnelDrawsAsAFailure(t *testing.T) {
	h := newHarness(t)
	host := h.addHost("db-01", "10.0.0.1")
	f := h.addForward(host.ID, 5432, "localhost", 5432)
	h.selectHost("db-01")
	h.press("f")

	// Stopped on purpose: a clean exit, no signal.
	h.m.d.fwd = map[string]term.ForwardState{
		term.ForwardSessionName(f): {Exit: 0},
	}
	h.mustContain("↵ starts it again")
	h.mustNotContain("stopped:")

	// Killed: the same empty exit status, with a signal beside it.
	h.m.d.fwd = map[string]term.ForwardState{
		term.ForwardSessionName(f): {Exit: 0, Signal: "kill"},
	}
	h.mustContain("stopped:")
	h.mustContain("kill")
}

// Starting takes a second or so, and the rule can be deleted inside that
// window — the delete stops a session that does not exist yet, and the start
// then creates one belonging to nothing: a tunnel holding a port with nothing
// in the interface able to see or stop it.
func TestATunnelWhoseRuleWentWhileStartingIsStopped(t *testing.T) {
	h := newHarness(t)
	host := h.addHost("db-01", "10.0.0.1")
	f := h.addForward(host.ID, 5432, "localhost", 5432)
	h.selectHost("db-01")
	h.press("f")

	// The rule goes while the start is in flight.
	if err := h.store.DeleteForward(f.ID); err != nil {
		t.Fatal(err)
	}
	h.reload()

	// The start now reports success, for a rule that is no longer there.
	h.send(forwardDoneMsg{f: f})

	if !strings.Contains(h.m.status, "went while it was starting") {
		t.Errorf("status is %q, want it to say the tunnel was stopped", h.m.status)
	}
	if strings.Contains(h.m.status, "is up") {
		t.Errorf("a tunnel for a deleted rule was reported as up: %q", h.m.status)
	}
}

// A start that lands while its rule is still there is reported as usual.
func TestATunnelThatOutlivesItsStartIsReportedUp(t *testing.T) {
	h := newHarness(t)
	host := h.addHost("db-01", "10.0.0.1")
	f := h.addForward(host.ID, 5432, "localhost", 5432)
	h.selectHost("db-01")
	h.press("f")

	h.send(forwardDoneMsg{f: f})
	if !strings.Contains(h.m.status, "is up") {
		t.Errorf("status is %q, want it to report the tunnel up", h.m.status)
	}
}

// "port 5432 is already in use" is accurate and useless: far more often than
// not it is one of these rules, sitting on the same screen with a ▶ against
// it. Saying which turns a sentence that sends someone to lsof into one that
// points a line up.
func TestAPortConflictNamesTheRuleHoldingIt(t *testing.T) {
	h := newHarness(t)
	host := h.addHost("db-01", "10.0.0.1")
	running := h.addForward(host.ID, 5432, "primary.internal", 5432)
	wants := h.addForward(host.ID, 5432, "replica.internal", 5432)
	h.selectHost("db-01")
	h.press("f")
	h.m.d.fwd = map[string]term.ForwardState{
		term.ForwardSessionName(running): {Running: true},
	}

	got := h.m.forwardAdvice(wants, "port 5432 is already in use")
	for _, want := range []string{"db-01", "primary.internal"} {
		if !strings.Contains(got, want) {
			t.Errorf("advice %q does not name %q", got, want)
		}
	}

	// With nothing of ours on that port, the reason is left as it came.
	h.m.d.fwd = nil
	if plain := h.m.forwardAdvice(wants, "port 5432 is already in use"); plain != "port 5432 is already in use" {
		t.Errorf("a port held by something else was blamed on us: %q", plain)
	}
}

// A remote rule binds its port on the host, so one of those is never what is
// holding a port here however alike the numbers look.
func TestARemoteRuleIsNotBlamedForALocalPort(t *testing.T) {
	h := newHarness(t)
	host := h.addHost("db-01", "10.0.0.1")
	remote, err := h.store.PutForward(store.Forward{
		HostID: host.ID, Kind: store.ForwardRemote,
		ListenPort: 5432, Dest: "localhost", DestPort: 5432,
	})
	if err != nil {
		t.Fatal(err)
	}
	local := h.addForward(host.ID, 5432, "replica.internal", 5432)
	h.selectHost("db-01")
	h.press("f")
	h.m.d.fwd = map[string]term.ForwardState{
		term.ForwardSessionName(remote): {Running: true},
	}

	if got := h.m.forwardAdvice(local, "port 5432 is already in use"); got != "port 5432 is already in use" {
		t.Errorf("a remote rule was blamed for a local port: %q", got)
	}
}

// confirmLines is the dialog's body as it will be drawn, escapes stripped.
// The rendered screen is boxes side by side with this one overlaid in the
// middle, so reading it back off the screen finds the pane behind it; the
// body is what the box is actually given.
func confirmLines(h *harness) []string {
	const width = 60 // what dialog() passes: the box's content width
	var out []string
	for _, l := range strings.Split(ansiRE.ReplaceAllString(h.m.confirmBody(width), ""), "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

func confirmSays(h *harness) string {
	return strings.Join(strings.Fields(strings.Join(confirmLines(h), " ")), " ")
}

// Every line has to fit the box, because box() cuts what does not — which is
// how the end of the sentence went missing in the first place.
func confirmFits(t *testing.T, h *harness) {
	t.Helper()
	for _, l := range confirmLines(h) {
		if w := ansiWidth(l); w > 60 {
			t.Errorf("a line %d cells wide will be cut at 60: %q", w, l)
		}
	}
}

// The confirmation is the sentence that says what a destructive action will
// do, and what it says last is the part that reassures — "nothing is
// deleted", "session history is kept". Cutting the line at the border took
// exactly that off, and left the group things were moving to half spelled.
func TestALongConfirmationWrapsRatherThanTruncating(t *testing.T) {
	h := newHarness(t)
	parent := h.addGroup("capichi-production-singapore", "")
	child := h.addGroup("capichi-production-singapore-legacy", parent.ID)
	for _, n := range []string{"web-01", "web-02", "web-03"} {
		h.addGroupedHost(n, child.ID)
	}
	h.m.focus = panelGroups
	for i, g := range h.m.d.tree {
		if g.ID == child.ID {
			h.m.groupIdx = i
		}
	}

	h.press("d")
	if h.m.mode != modeConfirm {
		t.Fatalf("d did not ask, mode is %v", h.m.mode)
	}
	got := confirmSays(h)
	// Where things go, and that nothing is lost: the two questions it exists
	// to answer, both of which the truncation took.
	for _, want := range []string{
		"Delete group capichi-production-singapore-legacy?",
		"3 hosts move to",
		"nothing is deleted",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the dialog does not say %q; it says: %s", want, got)
		}
	}
	confirmFits(t, h)

	// And through the real render, so the width the dialog hands it is pinned
	// too: get that wrong and box truncates the line exactly as before.
	h.mustContain("nothing is deleted")
}

// And a name with no break opportunities at all still cannot overflow: it is
// hard-broken, because a line wider than the box is truncated there, which is
// the thing being fixed.
func TestAnUnbreakableNameIsBrokenRatherThanOverflowing(t *testing.T) {
	h := newHarness(t)
	parent := h.addGroup(strings.Repeat("z", 70), "")
	child := h.addGroup("child", parent.ID)
	h.addGroupedHost("web-01", child.ID)
	h.m.focus = panelGroups
	for i, g := range h.m.d.tree {
		if g.ID == child.ID {
			h.m.groupIdx = i
		}
	}

	h.press("d")
	got := confirmSays(h)
	if !strings.Contains(got, "nothing is deleted") {
		t.Errorf("the end of the sentence was lost: %s", got)
	}
	if n := strings.Count(got, "z"); n != 70 {
		t.Errorf("the name appears as %d characters, want all 70: %s", n, got)
	}
	confirmFits(t, h)
}

// The host confirmation carries the same risk, and got longer when forwards
// were added to what it has to say.
func TestTheHostConfirmationWrapsToo(t *testing.T) {
	h := newHarness(t)
	host := h.addHost("db-01", "10.0.0.1")
	h.addForward(host.ID, 5432, "localhost", 5432)
	h.selectHost("db-01")

	h.press("d")
	got := confirmSays(h)
	for _, want := range []string{"its forward goes too", "session history is kept"} {
		if !strings.Contains(got, want) {
			t.Errorf("the dialog does not say %q; it says: %s", want, got)
		}
	}
	confirmFits(t, h)
}

// formLines is the form's body as the box will be given it, escapes stripped.
func formLines(h *harness) []string {
	const width = 60 // what dialog() passes
	var out []string
	for _, l := range strings.Split(ansiRE.ReplaceAllString(h.m.form.render(width), ""), "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

// A complaint says last what it is about, so cutting the line at the border
// takes the complaint and leaves the name: "would be its own ancestor" became
// "would be it…", and the store's busy message leads with the database path,
// so cutting that leaves a path and nothing else.
func TestALongFormProblemWrapsRatherThanTruncating(t *testing.T) {
	h := newHarness(t)
	parent := h.addGroup("capichi-production-singapore", "")
	child := h.addGroup("capichi-production-singapore-legacy", parent.ID)
	h.m.focus = panelGroups
	for i, g := range h.m.d.tree {
		if g.ID == parent.ID {
			h.m.groupIdx = i
		}
	}

	// Make the parent a child of its own child: the store refuses it.
	h.press("e", "tab")
	h.type_(child.Name)
	h.press("enter")

	if h.m.mode != modeForm {
		t.Fatalf("the cycle was accepted; mode is %v", h.m.mode)
	}
	said := strings.Join(strings.Fields(strings.Join(formLines(h), " ")), " ")
	if !strings.Contains(said, "would be its own ancestor") {
		t.Errorf("the complaint was cut short; the form says: %s", said)
	}
	for _, l := range formLines(h) {
		if w := ansiWidth(l); w > 60 {
			t.Errorf("a line %d cells wide will be cut at 60: %q", w, l)
		}
	}
}

// The worst of them: a message that leads with a path, so truncation leaves
// the path and none of the reason.
func TestAProblemLeadingWithAPathStillSaysWhy(t *testing.T) {
	h := newHarness(t)
	h.addHost("db-01", "10.0.0.1")
	h.selectHost("db-01")
	h.press("e")
	h.m.form.problem = "/Users/someone/Library/Application Support/omassh/omassh.db is busy — another omassh has been writing to it for over 5s"

	said := strings.Join(strings.Fields(strings.Join(formLines(h), " ")), " ")
	if !strings.Contains(said, "another omassh has been writing to it") {
		t.Errorf("the reason was cut; the form says: %s", said)
	}
	for _, l := range formLines(h) {
		if w := ansiWidth(l); w > 60 {
			t.Errorf("a line %d cells wide will be cut at 60: %q", w, l)
		}
	}
}

// A name with nothing to break at is the case Wordwrap cannot handle: it
// leaves the line over the width, and the box cuts it there — which is the
// thing being fixed. And the continuation lines sit under the text, not
// against the border.
func TestAnUnbreakableProblemIsBrokenAndIndented(t *testing.T) {
	h := newHarness(t)
	h.addHost("db-01", "10.0.0.1")
	h.selectHost("db-01")
	h.press("e")
	h.m.form.problem = `group "` + strings.Repeat("z", 70) + `" would be its own ancestor`

	// Just the complaint: the fields above it are their own business, and the
	// hints below it are not part of it.
	var problem []string
	for _, l := range formLines(h) {
		if strings.Contains(l, "✖") {
			problem = append(problem, l)
			continue
		}
		if len(problem) > 0 {
			if strings.Contains(l, "tab next field") {
				break
			}
			problem = append(problem, l)
		}
	}
	if len(problem) < 2 {
		t.Fatalf("the complaint did not wrap at all: %q", problem)
	}

	said := strings.Join(strings.Fields(strings.Join(problem, " ")), " ")
	if !strings.Contains(said, "would be its own ancestor") {
		t.Errorf("the complaint was cut short: %s", said)
	}
	for _, l := range problem {
		if w := ansiWidth(l); w > 60 {
			t.Errorf("a line %d cells wide will be cut at 60: %q", w, l)
		}
	}
	// The lines after the first line up under the text, past the ✖.
	for _, l := range problem[1:] {
		if !strings.HasPrefix(l, "    ") {
			t.Errorf("a continuation line is not indented under the text: %q", l)
		}
	}
}

// The bar is one row and cannot wrap, so something has to give when a message
// is longer than the terminal. What gives is the subject: which rule it was is
// on screen already, while why it failed is written nowhere else — cut the
// other way round, "port 443 is not yours to bind — a…" told you nothing you
// could act on.
func TestANarrowStatusKeepsTheReasonAndDropsTheSubject(t *testing.T) {
	h := newHarness(t)
	host := h.addHost("db-01", "10.0.0.1")
	f := h.addForward(host.ID, 443, "db.internal", 5432)
	h.selectHost("db-01")

	const reason = "port 443 is not yours to bind — a port below 1024 needs root"
	h.send(forwardDoneMsg{f: f, err: errors.New(reason)})

	// Wide enough for both: the subject is there.
	h.send(tea.WindowSizeMsg{Width: 120, Height: testH})
	wide := h.screen()
	if !strings.Contains(wide, f.Label()) {
		t.Errorf("at 120 columns the subject was dropped anyway:\n%s", lastLine(wide))
	}
	if !strings.Contains(wide, "needs root") {
		t.Errorf("at 120 columns the reason was cut:\n%s", lastLine(wide))
	}

	// Too narrow for both: the reason survives whole, the subject goes.
	h.send(tea.WindowSizeMsg{Width: 64, Height: testH})
	narrow := lastLine(h.screen())
	if !strings.Contains(narrow, "needs root") {
		t.Errorf("the reason was cut at 64 columns: %q", narrow)
	}
	if strings.Contains(narrow, "db.internal:5432") {
		t.Errorf("the subject was kept at the reason's expense: %q", narrow)
	}
	if w := ansiWidth(narrow); w > 64 {
		t.Errorf("the bar is %d cells wide, want at most 64: %q", w, narrow)
	}
}

// lastLine is the status bar: the final row of the frame.
func lastLine(screen string) string {
	lines := strings.Split(strings.TrimRight(screen, "\n"), "\n")
	return lines[len(lines)-1]
}

// An sftp failure is a sentence like any other, and its remedy is at the end.
// ssh's own half of it is long, so the omassh half has to be short, and the
// host has to be droppable — it is named on screen either way.
func TestAnSftpFailureKeepsItsRemedy(t *testing.T) {
	h := newHarness(t)
	h.addHost("filebox", "10.0.0.1")
	h.selectHost("filebox")

	h.send(sftpConnectedMsg{
		host: "filebox",
		err:  errors.New("cuonggt@10.0.0.1: Permission denied (publickey,keyboard-interactive). — ssh-add the key first"),
	})

	// Wide: the host is named alongside the reason.
	h.send(tea.WindowSizeMsg{Width: 120, Height: testH})
	if wide := lastLine(h.screen()); !strings.Contains(wide, "filebox:") || !strings.Contains(wide, "ssh-add the key first") {
		t.Errorf("at 120 columns: %q", wide)
	}

	// Narrow: the host goes, the remedy stays.
	h.send(tea.WindowSizeMsg{Width: 100, Height: testH})
	narrow := lastLine(h.screen())
	if !strings.Contains(narrow, "ssh-add the key first") {
		t.Errorf("the remedy was cut at 100 columns: %q", narrow)
	}
	if strings.Contains(narrow, "filebox:") {
		t.Errorf("the host was kept at the remedy's expense: %q", narrow)
	}
}

// The bar is set to "copying …" when a transfer starts, and nothing replaced
// it — so after a refusal the strip said the transfer had failed while the row
// beneath it went on saying it was in progress.
func TestAFinishedTransferStopsTheBarSayingCopying(t *testing.T) {
	h := newHarness(t)
	h.addHost("filebox", "10.0.0.1")
	h.selectHost("filebox")
	h.m.mode = modeSFTP
	h.m.panes = [2]filePane{{fs: fakeFS{}}, {fs: fakeFS{}}}

	h.m.setStatus("copying report.csv → filebox")
	h.send(transferMsg{name: "report.csv", err: errors.New("permission denied"), finished: true, dst: 1})

	if strings.Contains(h.m.status, "copying") {
		t.Errorf("the bar still says the transfer is running: %q", h.m.status)
	}
	if !strings.Contains(h.m.status, "permission denied") {
		t.Errorf("the bar does not say what went wrong: %q", h.m.status)
	}

	// And a transfer that worked leaves the outcome to the strip rather than
	// standing there claiming to still be copying.
	h.m.setStatus("copying report.csv → filebox")
	h.send(transferMsg{name: "report.csv", finished: true, total: 10, done: 10, dst: 1})
	if strings.Contains(h.m.status, "copying") {
		t.Errorf("after a successful transfer the bar still says: %q", h.m.status)
	}
}

// The strip is one row too, and the file it names is in the listing directly
// above — so when there is no room for both, the reason is what stays.
func TestANarrowTransferStripKeepsTheReason(t *testing.T) {
	h := newHarness(t)
	h.m.mode = modeSFTP
	h.m.panes = [2]filePane{{fs: fakeFS{}}, {fs: fakeFS{}}}
	h.m.transfer = transferMsg{
		name:     "quarterly-revenue-reconciliation-2026-Q3.csv",
		err:      errors.New("permission denied"),
		finished: true,
	}

	h.m.w = 110
	if wide := h.m.transferStrip(); !strings.Contains(wide, "quarterly-revenue") || !strings.Contains(wide, "permission denied") {
		t.Errorf("at 110 columns: %q", wide)
	}

	h.m.w = 50
	narrow := ansiRE.ReplaceAllString(h.m.transferStrip(), "")
	if !strings.Contains(narrow, "permission denied") {
		t.Errorf("the reason was cut at 50 columns: %q", narrow)
	}
	if strings.Contains(narrow, "quarterly-revenue") {
		t.Errorf("the name was kept at the reason's expense: %q", narrow)
	}
}

// A session owns every keystroke, so the one thing the screen must keep is the
// way back out. Both places that carry it — the pane's title and the bar —
// used to lose it to a long host name, leaving nothing on screen to try.
func TestANarrowSessionKeepsTheWayOut(t *testing.T) {
	h := newHarness(t)
	const long = "prod-be-delivery-console-eu-west-1"
	h.addHost(long, "10.0.0.1")
	h.selectHost(long)

	// The bar, as attaching sets it.
	h.m.setStatusOf(long, attachedMessage(false))
	h.send(tea.WindowSizeMsg{Width: 120, Height: testH})
	if wide := lastLine(h.screen()); !strings.Contains(wide, long) || !strings.Contains(wide, "for the host list") {
		t.Errorf("at 120 columns: %q", wide)
	}
	h.send(tea.WindowSizeMsg{Width: 64, Height: testH})
	narrow := lastLine(h.screen())
	if !strings.Contains(narrow, prefixKey+" w for the host list") {
		t.Errorf("the way out was cut at 64 columns: %q", narrow)
	}
	if strings.Contains(narrow, long) {
		t.Errorf("the name was kept at its expense: %q", narrow)
	}
}

// And the title itself: the name gives way, not the instruction.
func TestASessionTitleElidesTheNameNotTheWayOut(t *testing.T) {
	const long = "prod-be-delivery-console-eu-west-1"
	const plain = "ctrl+\\ w for the host list"

	// A title narrower than name and instruction together, composed by the
	// function the pane itself uses.
	got := ansiRE.ReplaceAllString(sessionTitleText(long, plain, plain, 60), "")
	if !strings.Contains(got, plain) {
		t.Errorf("the instruction was cut: %q", got)
	}
	if !strings.Contains(got, "…") {
		t.Errorf("the name was not elided though there was no room: %q", got)
	}
	if w := ansiWidth(got); w > 60-5 {
		t.Errorf("the title is %d cells, which box would cut at %d: %q", w, 60-5, got)
	}
}

// Narrower still, and the name goes altogether. The list beside the pane marks
// which host is connected; nothing else on screen says how to get out of it.
func TestAVeryNarrowSessionTitleKeepsOnlyTheWayOut(t *testing.T) {
	const plain = "ctrl+\\ w for the host list"
	got := sessionTitleText("prod-be-delivery-console-eu-west-1", plain, plain, 34)

	if !strings.Contains(got, plain) {
		t.Errorf("the way out was cut: %q", got)
	}
	// Exactly the instruction: not a stub of the name and an ellipsis, which
	// costs two cells and says nothing.
	if got != plain {
		t.Errorf("the title is %q, want only the way out", got)
	}
}

// Detaching leaves a session running, and that is the part worth keeping when
// the bar is short of room — the host it was is in the list with a mark
// against it, while "still running, t to reattach" is what says the work is
// not lost.
func TestDetachingSaysTheSessionSurvives(t *testing.T) {
	ctx, msg := detachText("prod-be-delivery-console-eu-west-1", true, "t")
	if ctx != "prod-be-delivery-console-eu-west-1" {
		t.Errorf("the host is not the subject: %q", ctx)
	}
	if strings.Contains(msg, "prod-be") {
		t.Errorf("the message names the host, which the subject already does: %q", msg)
	}
	for _, want := range []string{"still running", "t to reattach"} {
		if !strings.Contains(msg, want) {
			t.Errorf("%q does not say %q", msg, want)
		}
	}

	// Without tmux a session really does end, and must not claim otherwise.
	_, ephemeral := detachText("box", false, "t")
	if strings.Contains(ephemeral, "still running") {
		t.Errorf("an ephemeral session was reported as surviving: %q", ephemeral)
	}
}

// A filename comes from the far side, and a name is not a message. Drawn as it
// came, one holding an escape sequence coloured the interface from the file
// rather than from the theme, and one holding a cursor move or an erase had
// the terminal act on it in the middle of a redraw — for nothing more than
// opening a directory and looking at it.
func TestANameFromTheFarSideIsDrawnAsTextNotAsInstructions(t *testing.T) {
	dir := t.TempDir()
	names := []string{
		"aaa\x1b[31mRED\x1b[0m.txt",    // takes over the row's colour
		"bbb\x1b[2K\x1b[1;1Hmoved.txt", // erases the line and homes the cursor
		"ccc\x1b]0;retitled\x07.txt",   // renames the terminal window
		"ddd\nsplit.txt",               // two rows out of one
		"eee\ttabbed.txt",              // shifts the size column
		"fff\rover.txt",                // draws the end over the start
		"ggg\x07bell.txt",              // rings
		"ordinary.txt",
	}
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o600); err != nil {
			t.Skipf("this filesystem will not hold %q: %v", n, err)
		}
	}

	h := newHarness(t)
	h.m.mode = modeSFTP
	h.m.panes = [2]filePane{{fs: sftpx.Local{}, path: dir}, {fs: fakeFS{}}}
	h.m.panes[0].reload()
	if len(h.m.panes[0].entries) != len(names) {
		t.Fatalf("listed %d of %d names", len(h.m.panes[0].entries), len(names))
	}

	const w = 60
	body := h.m.paneBody(0, w, len(names)+2)

	// One row per file: a newline in a name split one row into two, and the
	// second had no border on its end.
	rows := strings.Split(body, "\n")
	if len(rows) != len(names) {
		t.Fatalf("%d names drew %d rows", len(names), len(rows))
	}
	// Nothing the file said reaches the terminal, and every row is the same
	// width as the rest, so the size column stays in its place. The first is
	// the one under the cursor, which is drawn to the full width on purpose.
	want := ansi.StringWidth(rows[1])
	for _, r := range rows[1:] {
		for _, seq := range []string{"\x1b[31m", "\x1b[2K", "\x1b[1;1H", "\x1b]0;", "\t", "\r", "\x07"} {
			if strings.Contains(r, seq) {
				t.Errorf("a drawn row still carries %q from a filename: %q", seq, r)
			}
		}
		if got := ansi.StringWidth(r); got != want {
			t.Errorf("a row is %d cells wide where the rest are %d: %q", got, want, r)
		}
	}
	// And the names are still readable, rather than blanked out.
	for _, want := range []string{"aaaRED.txt", "ddd?split.txt", "eee?tabbed.txt", "ordinary.txt"} {
		if !strings.Contains(body, want) {
			t.Errorf("the listing does not show %q", want)
		}
	}
}

// A listing reports what lstat says, so a symlink is never a directory in it —
// and /tmp, /etc, /var and /home are all symlinks on macOS. Pressing ↵ on one
// did nothing whatsoever: no descent, no message, nothing to say it was not
// simply broken, and no way from / to anywhere they lead.
func TestASymlinkToADirectoryOpensLikeOne(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "real"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "real", "inside.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "afile.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, l := range []struct{ target, name string }{
		{"real", "to-dir"}, {"afile.txt", "to-file"}, {"nowhere", "to-nothing"},
	} {
		if err := os.Symlink(l.target, filepath.Join(dir, l.name)); err != nil {
			t.Skipf("no symlinks here: %v", err)
		}
	}

	at := func(name string) *filePane {
		p := &filePane{fs: sftpx.Local{}, path: dir}
		p.reload()
		for i, e := range p.entries {
			if e.Name == name {
				p.idx = i
				return p
			}
		}
		t.Fatalf("%q is not in the listing", name)
		return nil
	}

	t.Run("to a directory", func(t *testing.T) {
		p := at("to-dir")
		ok, why := p.enterSelected()
		if !ok {
			t.Fatalf("↵ did not open it: %q", why)
		}
		if p.path != filepath.Join(dir, "to-dir") {
			t.Errorf("path = %q", p.path)
		}
		if len(p.entries) != 1 || p.entries[0].Name != "inside.txt" {
			t.Errorf("entries = %+v, want what the link points at", p.entries)
		}
	})

	t.Run("to a file", func(t *testing.T) {
		p := at("to-file")
		// A link to a file is a file: ↵ is not what opens one, and there is
		// nothing to say about it.
		if ok, why := p.enterSelected(); ok || why != "" {
			t.Errorf("enterSelected() = %v, %q", ok, why)
		}
		if p.path != dir {
			t.Errorf("it moved to %q", p.path)
		}
	})

	t.Run("to nothing", func(t *testing.T) {
		p := at("to-nothing")
		ok, why := p.enterSelected()
		if ok {
			t.Fatal("it descended into a broken link")
		}
		// Silence is what this looked like before, and silence is what made
		// the whole thing read as broken.
		if !strings.Contains(why, "to-nothing") {
			t.Errorf("why = %q, want it to name the link", why)
		}
		if strings.Contains(why, "no such file") {
			t.Errorf("why = %q, said about a name that is plainly on the screen", why)
		}
	})
}
