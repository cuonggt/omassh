package portable

import (
	"strings"
	"testing"

	"github.com/cuonggt/omassh/internal/store"
)

func TestAStoreSurvivesTheRoundTrip(t *testing.T) {
	groups := []store.Group{
		{ID: "g1", Name: "Production", User: "admin"},
		{ID: "g2", Name: "EU", ParentID: "g1", Identity: "~/.ssh/eu"},
	}
	hosts := []store.Host{
		{ID: "h1", Name: "web", Addr: "10.0.0.1", Port: 2222, GroupID: "g2", Tags: []string{"prod", "web"}},
		{ID: "h2", Name: "bastion", Addr: "edge.example.com", User: "ops"},
	}

	raw, err := Export(groups, hosts).YAML()
	if err != nil {
		t.Fatal(err)
	}
	d, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	p, err := Merge(d, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	if len(p.Hosts) != 2 || len(p.Groups) != 2 {
		t.Fatalf("got %d groups and %d hosts, want 2 and 2", len(p.Groups), len(p.Hosts))
	}
	web := findHost(t, p, "web")
	if web.Addr != "10.0.0.1" || web.Port != 2222 {
		t.Errorf("address did not survive: %+v", web)
	}
	if strings.Join(web.Tags, ",") != "prod,web" {
		t.Errorf("tags did not survive: %v", web.Tags)
	}
	// The ids are new, but the tree has to be the same shape: web is in EU,
	// and EU is under Production.
	eu := findGroup(t, p, "EU")
	prod := findGroup(t, p, "Production")
	if web.GroupID != eu.ID {
		t.Errorf("web is not in EU")
	}
	if eu.ParentID != prod.ID {
		t.Errorf("EU is not under Production")
	}
}

func TestExportLeavesOutSessionHistory(t *testing.T) {
	raw, err := Export(nil, []store.Host{{ID: "h1", Name: "web", Addr: "10.0.0.1"}}).YAML()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"h1", "last_seen", "stats", "count"} {
		if strings.Contains(string(raw), s) {
			t.Errorf("export leaked %q:\n%s", s, raw)
		}
	}
}

func TestImportMatchesByNameAndKeepsTheID(t *testing.T) {
	// The id is what session history hangs off, so re-importing a host must
	// not mint a new one and orphan it.
	existing := []store.Host{{ID: "keep-me", Name: "web", Addr: "10.0.0.1"}}
	d := Document{Version: Version, Hosts: []Host{{Name: "WEB", Addr: "10.0.0.9"}}}

	p, err := Merge(d, nil, existing)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Hosts) != 1 {
		t.Fatalf("got %d hosts, want 1 — a name that differs only in case is the same host", len(p.Hosts))
	}
	if p.Hosts[0].ID != "keep-me" {
		t.Errorf("id = %q, want keep-me", p.Hosts[0].ID)
	}
	if p.Hosts[0].Addr != "10.0.0.9" {
		t.Errorf("addr = %q, want the document's value", p.Hosts[0].Addr)
	}
	if p.Changes[0].Action != Update {
		t.Errorf("action = %q, want update", p.Changes[0].Action)
	}
}

func TestImportFillsInWithoutBlanking(t *testing.T) {
	existing := []store.Host{{ID: "h1", Name: "web", Addr: "10.0.0.1", User: "admin", Tags: []string{"prod"}}}
	// What ssh_config knows nothing about must survive a second import.
	d := Document{Version: Version, Hosts: []Host{{Name: "web", Addr: "10.0.0.1"}}}

	p, err := Merge(d, nil, existing)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Empty() {
		t.Fatalf("a document that adds nothing should change nothing, got %v", p.Changes)
	}
	if p.Unchanged != 1 {
		t.Errorf("unchanged = %d, want 1", p.Unchanged)
	}
}

func TestImportCreatesAGroupAHostNames(t *testing.T) {
	d := Document{Version: Version, Hosts: []Host{{Name: "web", Addr: "10.0.0.1", Group: "Homelab"}}}

	p, err := Merge(d, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	g := findGroup(t, p, "Homelab")
	if findHost(t, p, "web").GroupID != g.ID {
		t.Errorf("the host is not in the group that was created for it")
	}
}

func TestImportRejectsWhatItCannotApply(t *testing.T) {
	cases := map[string]Document{
		"a name twice": {Hosts: []Host{
			{Name: "web", Addr: "a"}, {Name: "WEB", Addr: "b"},
		}},
		"no address": {Hosts: []Host{{Name: "web"}}},
		"no name":    {Hosts: []Host{{Addr: "10.0.0.1"}}},
		"a group cycle": {Groups: []Group{
			{Name: "a", Parent: "b"}, {Name: "b", Parent: "a"},
		}},
		"an unknown parent": {Groups: []Group{{Name: "a", Parent: "nowhere"}}},
		"a later version":   {Version: Version + 1},
	}
	for name, d := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Merge(d, nil, nil); err == nil {
				t.Fatalf("merged a document with %s", name)
			}
		})
	}
}

func TestAParentListedAfterItsChildStillResolves(t *testing.T) {
	d := Document{Version: Version, Groups: []Group{
		{Name: "EU", Parent: "Production"},
		{Name: "Production"},
	}}
	p, err := Merge(d, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if findGroup(t, p, "EU").ParentID != findGroup(t, p, "Production").ID {
		t.Error("EU did not attach to a parent declared below it")
	}
}

func findHost(t *testing.T, p Plan, name string) store.Host {
	t.Helper()
	for _, h := range p.Hosts {
		if strings.EqualFold(h.Name, name) {
			return h
		}
	}
	t.Fatalf("no host %q in the plan", name)
	return store.Host{}
}

func findGroup(t *testing.T, p Plan, name string) store.Group {
	t.Helper()
	for _, g := range p.Groups {
		if strings.EqualFold(g.Name, name) {
			return g
		}
	}
	t.Fatalf("no group %q in the plan", name)
	return store.Group{}
}

func TestEveryRecordIsAccountedForExactlyOnce(t *testing.T) {
	// One group named by two hosts is one group, however many hosts point at
	// it — the report says what the import did, so it has to add up.
	d := Document{Version: Version, Hosts: []Host{
		{Name: "a", Addr: "10.0.0.1", Group: "Work"},
		{Name: "b", Addr: "10.0.0.2", Group: "Work"},
	}}

	first, err := Merge(d, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if added, updated := first.Counts(); added != 3 || updated != 0 {
		t.Fatalf("first import: %d added, %d updated, want 3 and 0 (two hosts and the group)", added, updated)
	}

	// Applied, the same document is a no-op — and still speaks for all three.
	second, err := Merge(d, first.Groups, first.Hosts)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Empty() {
		t.Errorf("re-importing changed something: %v", second.Changes)
	}
	if second.Unchanged != 3 {
		t.Errorf("unchanged = %d, want 3 — the group is a record too", second.Unchanged)
	}
}
