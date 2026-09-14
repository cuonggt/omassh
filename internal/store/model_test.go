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
