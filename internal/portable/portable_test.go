package portable

import (
	"reflect"
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

	raw, err := Export(groups, hosts, nil).YAML()
	if err != nil {
		t.Fatal(err)
	}
	d, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	p, err := Merge(d, nil, nil, nil)
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
	raw, err := Export(nil, []store.Host{{ID: "h1", Name: "web", Addr: "10.0.0.1"}}, nil).YAML()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"h1", "last_seen", "stats", "count"} {
		if strings.Contains(string(raw), s) {
			t.Errorf("export leaked %q:\n%s", s, raw)
		}
	}
}

// A key the format does not have must be named, not skipped past. The report
// reads the same either way — "2 added, 0 updated" — so jump_host where the
// field is jump would otherwise leave the host showing via — with nothing on
// screen to say a line was ignored.
func TestParseNamesAKeyTheFormatDoesNotHave(t *testing.T) {
	raw := []byte("version: 1\nhosts:\n  - name: web\n    addr: 10.0.0.1\n    jump_host: bastion\n")

	if _, err := Parse(raw); err == nil {
		t.Fatal("parsed a document with a key the format does not have")
	} else if !strings.Contains(err.Error(), "jump_host") {
		t.Errorf("error does not name the key: %v", err)
	} else if !strings.Contains(err.Error(), "line 5") {
		// The line is what makes the message usable in a document holding a
		// hundred hosts.
		t.Errorf("error does not name the line: %v", err)
	}
}

// The other half of that bargain: every key the format does have must still
// be accepted, since a yaml tag that drifted from its field would now turn a
// working document into an error rather than a silently dropped line.
func TestParseAcceptsEveryFieldTheFormatHas(t *testing.T) {
	d, err := Parse([]byte(`version: 1
groups:
  - name: Production
    user: admin
  - name: EU
    parent: Production
    identity: ~/.ssh/eu
    jump: bastion
hosts:
  - name: web
    addr: 10.0.0.1
    port: 2222
    user: deploy
    identity: ~/.ssh/web
    jump: bastion
    group: EU
    tags: [prod, web]
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(d.Groups) != 2 || len(d.Hosts) != 1 {
		t.Fatalf("got %d groups and %d hosts, want 2 and 1", len(d.Groups), len(d.Hosts))
	}
	want := Host{
		Name: "web", Addr: "10.0.0.1", Port: 2222, User: "deploy",
		Identity: "~/.ssh/web", Jump: "bastion", Group: "EU",
		Tags: []string{"prod", "web"},
	}
	if !reflect.DeepEqual(d.Hosts[0], want) {
		t.Errorf("host = %+v, want %+v", d.Hosts[0], want)
	}
	if eu := d.Groups[1]; eu != (Group{Name: "EU", Parent: "Production", Identity: "~/.ssh/eu", Jump: "bastion"}) {
		t.Errorf("group = %+v", eu)
	}
}

// Decode reads one document, so the records after a second --- would arrive
// as nothing at all — a host list split in two would import half of itself
// and report that half as the whole.
func TestParseRejectsMoreThanOneDocument(t *testing.T) {
	raw := []byte("version: 1\nhosts:\n  - name: web\n    addr: 10.0.0.1\n---\nhosts:\n  - name: db\n    addr: 10.0.0.2\n")

	if _, err := Parse(raw); err == nil {
		t.Fatal("parsed a stream holding two documents")
	} else if !strings.Contains(err.Error(), "more than one YAML document") {
		t.Errorf("error does not say what is wrong: %v", err)
	}
}

// Opening with the marker is a plain YAML habit and is still one document —
// only a second --- starts a second one.
func TestParseAcceptsALeadingDocumentMarker(t *testing.T) {
	d, err := Parse([]byte("---\nversion: 1\nhosts:\n  - name: web\n    addr: 10.0.0.1\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(d.Hosts) != 1 {
		t.Fatalf("got %d hosts, want 1", len(d.Hosts))
	}
}

// An empty file is empty, not malformed — it decodes to EOF, and the import
// has a better complaint about it than anything about YAML.
func TestParseAcceptsADocumentWithNothingInIt(t *testing.T) {
	d, err := Parse([]byte("# only a comment\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(d.Hosts) != 0 || len(d.Groups) != 0 {
		t.Errorf("got records out of an empty document: %+v", d)
	}
}

func TestImportMatchesByNameAndKeepsTheID(t *testing.T) {
	// The id is what session history hangs off, so re-importing a host must
	// not mint a new one and orphan it.
	existing := []store.Host{{ID: "keep-me", Name: "web", Addr: "10.0.0.1"}}
	d := Document{Version: Version, Hosts: []Host{{Name: "WEB", Addr: "10.0.0.9"}}}

	p, err := Merge(d, nil, existing, nil)
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

	p, err := Merge(d, nil, existing, nil)
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

	p, err := Merge(d, nil, nil, nil)
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
			if _, err := Merge(d, nil, nil, nil); err == nil {
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
	p, err := Merge(d, nil, nil, nil)
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

	first, err := Merge(d, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if added, updated := first.Counts(); added != 3 || updated != 0 {
		t.Fatalf("first import: %d added, %d updated, want 3 and 0 (two hosts and the group)", added, updated)
	}

	// Applied, the same document is a no-op — and still speaks for all three.
	second, err := Merge(d, first.Groups, first.Hosts, nil)
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
