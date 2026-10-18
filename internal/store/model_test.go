package store

import (
	"fmt"
	"testing"
)

func names(ns []GroupNode) []string {
	out := make([]string, len(ns))
	for i, n := range ns {
		out[i] = n.Name
	}
	return out
}

func TestFlattenGroupsNestsDepthFirst(t *testing.T) {
	got := FlattenGroups([]Group{
		{ID: "2", Name: "Staging"},
		{ID: "1", Name: "Production"},
		{ID: "1a", Name: "EU", ParentID: "1"},
		{ID: "1b", Name: "US", ParentID: "1"},
		{ID: "1a1", Name: "Frankfurt", ParentID: "1a"},
	})

	want := []string{"Production", "EU", "Frankfurt", "US", "Staging"}
	for i, n := range names(got) {
		if n != want[i] {
			t.Fatalf("order = %v, want %v", names(got), want)
		}
	}
	if got[2].Depth != 2 {
		t.Errorf("Frankfurt depth = %d, want 2", got[2].Depth)
	}
}

// A dangling ParentID must not make a group (and its hosts) invisible.
func TestFlattenGroupsSurfacesOrphans(t *testing.T) {
	got := FlattenGroups([]Group{{ID: "x", Name: "Orphan", ParentID: "missing"}})

	if len(got) != 1 || got[0].Depth != 0 {
		t.Fatalf("got %v, want Orphan at depth 0", got)
	}
}

// It terminates on a cycle, and lists what is in one rather than dropping it.
// Emitting nothing is what this used to do, and it was the wrong half of the
// promise the function makes: a broken ParentID must not hide hosts, and a
// parent that comes back round is as broken as one that is not there.
func TestFlattenGroupsTerminatesOnCycle(t *testing.T) {
	got := FlattenGroups([]Group{
		{ID: "a", Name: "A", ParentID: "b"},
		{ID: "b", Name: "B", ParentID: "a"},
	})
	if len(got) != 2 {
		t.Fatalf("got %v, want both groups listed", names(got))
	}
	for _, n := range got {
		if n.Depth != 0 {
			t.Errorf("%s is at depth %d; neither of a pair pointing at each other is under the other", n.Name, n.Depth)
		}
	}
}

func TestStatKey(t *testing.T) {
	local := Host{ID: "abc", Name: "web"}
	if got := local.StatKey(); got != "abc" {
		t.Errorf("local StatKey = %q, want abc", got)
	}
}

// A depth limit alongside the once-only guard dropped anything nested past it,
// silently: the groups vanished from the list, and their hosts with them —
// reachable only by searching.
func TestFlattenGroupsKeepsDeepNesting(t *testing.T) {
	var gs []Group
	for i := range 25 {
		g := Group{ID: fmt.Sprintf("g%02d", i), Name: fmt.Sprintf("L%02d", i)}
		if i > 0 {
			g.ParentID = fmt.Sprintf("g%02d", i-1)
		}
		gs = append(gs, g)
	}

	got := FlattenGroups(gs)
	if len(got) != len(gs) {
		t.Fatalf("flattened %d of %d groups", len(got), len(gs))
	}
	last := got[len(got)-1]
	if last.Name != "L24" || last.Depth != 24 {
		t.Errorf("deepest is %s at depth %d, want L24 at depth 24", last.Name, last.Depth)
	}
}

// A group whose ancestry never reaches a root is surfaced at the root, the
// same as one whose parent is missing.
//
// A loop is not something this store will create, but a database can hold one
// — written by an older build, by another writer, or by hand — and the walk
// starts from roots, so no group in a loop was ever reached. Every group in it
// vanished from the list and took its hosts with it: the records were all
// still there, and the only thing that could be done about them was to not see
// them. Nothing on screen to select is also nothing to mend, so the loop could
// not even be undone from the interface that was hiding it.
func TestAGroupInALoopIsStillListed(t *testing.T) {
	gs := []Group{
		{ID: "a", Name: "company", ParentID: "b"}, // company → eu → company
		{ID: "b", Name: "eu", ParentID: "a"},
		{ID: "c", Name: "eu-prod", ParentID: "b"}, // below the loop
		{ID: "d", Name: "elsewhere"},              // a genuine root
	}

	nodes := FlattenGroups(gs)

	listed := map[string]int{}
	for _, n := range nodes {
		if _, twice := listed[n.Name]; twice {
			t.Errorf("%q is listed more than once", n.Name)
		}
		listed[n.Name] = n.Depth
	}
	for _, g := range gs {
		if _, ok := listed[g.Name]; !ok {
			t.Errorf("%q is not in the list at all, and its hosts went with it", g.Name)
		}
	}
	if len(nodes) != len(gs) {
		t.Fatalf("%d groups came back as %d rows", len(gs), len(nodes))
	}
	// And what is below the loop stays below it, rather than being flattened
	// alongside: eu-prod's parent is still in the list.
	if listed["eu-prod"] == 0 {
		t.Errorf("eu-prod was raised to the root; it has a parent that is listed")
	}
}

// A chain that breaks further up is not a loop, and the group at the bottom of
// it keeps its parent. Only the group whose own parent is missing is surfaced,
// which is what the orphan rule has always done — walking up and giving up at
// the first thing that is not there would have raised the whole line to the
// root and flattened a tree that is perfectly readable.
func TestAGroupWhoseGrandparentIsMissingKeepsItsParent(t *testing.T) {
	nodes := FlattenGroups([]Group{
		{ID: "p", Name: "parent", ParentID: "gone"},
		{ID: "c", Name: "child", ParentID: "p"},
	})

	depth := map[string]int{}
	for _, n := range nodes {
		depth[n.Name] = n.Depth
	}
	if len(nodes) != 2 {
		t.Fatalf("got %v, want both groups", names(nodes))
	}
	if depth["parent"] != 0 {
		t.Errorf("parent is at depth %d, want the root", depth["parent"])
	}
	if depth["child"] != 1 {
		t.Errorf("child is at depth %d, want it still under parent", depth["child"])
	}
}

// A jump host written as a destination is read back the way ssh reads it, so
// that the connection to it can be spelled rather than handed over whole: ssh
// takes `[user@]host[:port]` for -J and nothing like it for a destination,
// where a colon is part of a hostname that resolves to nothing.
func TestParseJumpTarget(t *testing.T) {
	// A Host carries a slice and so cannot be compared; these three fields
	// are the whole of what a destination says.
	type dest struct {
		user, addr string
		port       int
	}
	spelled := func(h Host) dest { return dest{h.User, h.Addr, h.Port} }

	for written, want := range map[string]dest{
		"bastion":              {addr: "bastion"},
		"ops@edge.example.com": {user: "ops", addr: "edge.example.com"},
		"bastion:2222":         {addr: "bastion", port: 2222},
		"ops@bastion:2222":     {user: "ops", addr: "bastion", port: 2222},
		"ops@bastion:22":       {user: "ops", addr: "bastion", port: 22},
		"  bastion  ":          {addr: "bastion"},
		// ssh splits the user at the last @, so this logs in as a@b.
		"a@b@bastion": {user: "a@b", addr: "bastion"},
		// An IPv6 literal: bracketed to carry a port, and bare without one,
		// where the colons are the address and no port at all.
		"[::1]:2222":       {addr: "::1", port: 2222},
		"[::1]":            {addr: "::1"},
		"::1":              {addr: "::1"},
		"2001:db8::1":      {addr: "2001:db8::1"},
		"ops@[fe80::1]:22": {user: "ops", addr: "fe80::1", port: 22},
	} {
		got, ok := ParseJumpTarget(written)
		if !ok {
			t.Errorf("ParseJumpTarget(%q) gave up", written)
			continue
		}
		if spelled(got) != want {
			t.Errorf("ParseJumpTarget(%q) = %+v, want %+v", written, spelled(got), want)
		}
	}
}

// What it will not take apart it says so about, rather than handing back a
// host that reaches somewhere else. A chain is the caller's to split, since
// every hop in one is spelled on its own.
func TestParseJumpTargetRefusesWhatItCannotRead(t *testing.T) {
	for _, written := range []string{
		"", "   ", "ops@",
		"bastion:", "bastion:nope", "bastion:0", "bastion:70000", "bastion:-1",
		"[::1", "[::1]junk", "[::1]:nope",
		"a,b", "bastion,", "ops@a:22,ops@b:22",
	} {
		if got, ok := ParseJumpTarget(written); ok {
			t.Errorf("ParseJumpTarget(%q) = %+v, want to have given up", written, got)
		}
	}
}

// The two directions agree: a host rendered for -J reads back as itself. The
// default port is left implicit by JumpTarget, which is ssh's own doing — a
// host on 22 and a host on no port are the same host.
func TestParseJumpTargetUndoesJumpTarget(t *testing.T) {
	for _, h := range []Host{
		{Addr: "bastion"},
		{Addr: "bastion", Port: 22},
		{User: "ops", Addr: "bastion", Port: 2222},
		{User: "ops", Addr: "edge.example.com"},
	} {
		got, ok := ParseJumpTarget(h.JumpTarget())
		if !ok {
			t.Fatalf("%q, rendered from %+v, did not read back", h.JumpTarget(), h)
		}
		if got.User != h.User || got.Addr != h.Addr || portOr22(got.Port) != portOr22(h.Port) {
			t.Errorf("%+v rendered as %q and read back as %+v", h, h.JumpTarget(), got)
		}
	}
}
