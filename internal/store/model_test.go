package store

import "testing"

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

func TestFlattenGroupsTerminatesOnCycle(t *testing.T) {
	got := FlattenGroups([]Group{
		{ID: "a", Name: "A", ParentID: "b"},
		{ID: "b", Name: "B", ParentID: "a"},
	})
	if len(got) != 0 {
		t.Errorf("got %v, want nothing emitted for a pure cycle", names(got))
	}
}

func TestStatKey(t *testing.T) {
	local := Host{ID: "abc", Name: "web"}
	if got := local.StatKey(); got != "abc" {
		t.Errorf("local StatKey = %q, want abc", got)
	}
	cfg := Host{ID: "", Name: "orb", Source: SourceSSHConfig}
	if got := cfg.StatKey(); got != "cfg:orb" {
		t.Errorf("config StatKey = %q, want cfg:orb", got)
	}
}
