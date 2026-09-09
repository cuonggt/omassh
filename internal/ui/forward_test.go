package ui

import (
	"strings"
	"testing"

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
	h.mustContain("forward")

	h.press("y")
	if fs, _ := h.store.Forwards(); len(fs) != 0 {
		t.Errorf("deleting the host left its rules: %+v", fs)
	}
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
