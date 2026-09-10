package portable

import (
	"strings"
	"testing"

	"github.com/cuonggt/omassh/internal/store"
)

// A rule says how you work with a host — "the database is on 5432 through
// there" — which is as true on a laptop as on a desktop, so it travels. It
// used to be dropped in silence: the export named the host and not its
// tunnels, and the import reported the same tidy "1 added" either way.
func TestForwardsRoundTrip(t *testing.T) {
	hosts := []store.Host{{ID: "h1", Name: "db-01", Addr: "10.0.0.1"}}
	rules := []store.Forward{
		{ID: "f1", HostID: "h1", Kind: store.ForwardLocal, ListenPort: 5432, Dest: "db.internal", DestPort: 5432},
		{ID: "f2", HostID: "h1", Kind: store.ForwardRemote, ListenPort: 8080, Dest: "localhost", DestPort: 3000},
		{ID: "f3", HostID: "h1", Kind: store.ForwardDynamic, Listen: "127.0.0.1", ListenPort: 1080},
	}

	raw, err := Export(nil, hosts, rules).YAML()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "forwards:") {
		t.Fatalf("the document has no forwards:\n%s", raw)
	}

	d, err := Parse(raw)
	if err != nil {
		t.Fatalf("parsing what we wrote: %v", err)
	}
	// Into a store that has the host but none of its rules.
	p, err := Merge(d, nil, hosts, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Forwards) != 3 {
		t.Fatalf("planned %d rules, want 3: %+v", len(p.Forwards), p.Forwards)
	}
	want := map[string]bool{}
	for _, f := range rules {
		want[ruleKey(f)] = true
	}
	for _, f := range p.Forwards {
		if !want[ruleKey(f)] {
			t.Errorf("a rule came back different: %s", ruleKey(f))
		}
		if f.HostID != "h1" {
			t.Errorf("rule %s attached to %q, want the host it belongs to", f.Label(), f.HostID)
		}
		if f.ID == "" {
			t.Errorf("rule %s has no id", f.Label())
		}
	}
}

// Importing the same document twice changes nothing the second time, as it
// does for hosts and groups.
func TestImportingForwardsTwiceIsIdempotent(t *testing.T) {
	hosts := []store.Host{{ID: "h1", Name: "db-01", Addr: "10.0.0.1"}}
	rules := []store.Forward{
		{ID: "f1", HostID: "h1", Kind: store.ForwardLocal, ListenPort: 5432, Dest: "db", DestPort: 5432},
	}
	d := Export(nil, hosts, rules)

	first, err := Merge(d, nil, hosts, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Forwards) != 1 {
		t.Fatalf("first import planned %d rules, want 1", len(first.Forwards))
	}

	second, err := Merge(d, nil, hosts, first.Forwards)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Forwards) != 0 {
		t.Errorf("the second import would write %+v", second.Forwards)
	}
	if second.Unchanged == 0 {
		t.Error("the second import reported nothing as already matched")
	}
}

// Two rules may legitimately bind the same port for different destinations —
// only one can run at a time, but both are real. Matching on the binding
// alone would fold them into one on the way across.
func TestTwoRulesOnOnePortBothTravel(t *testing.T) {
	hosts := []store.Host{{ID: "h1", Name: "db-01", Addr: "10.0.0.1"}}
	rules := []store.Forward{
		{ID: "f1", HostID: "h1", Kind: store.ForwardLocal, ListenPort: 5432, Dest: "primary", DestPort: 5432},
		{ID: "f2", HostID: "h1", Kind: store.ForwardLocal, ListenPort: 5432, Dest: "replica", DestPort: 5432},
	}
	d := Export(nil, hosts, rules)

	p, err := Merge(d, nil, hosts, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Forwards) != 2 {
		t.Fatalf("planned %d rules, want both: %+v", len(p.Forwards), p.Forwards)
	}
}

// A rule travels with a host the same document brings, attaching to the id
// minted for it a moment earlier.
func TestAForwardAttachesToAHostTheDocumentBrings(t *testing.T) {
	d := Document{Version: 1, Hosts: []Host{{
		Name: "new-box", Addr: "10.0.0.9",
		Forwards: []Forward{{Kind: "local", Listen: "5432", Dest: "db:5432"}},
	}}}

	p, err := Merge(d, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Hosts) != 1 || len(p.Forwards) != 1 {
		t.Fatalf("planned %d hosts and %d rules, want one of each", len(p.Hosts), len(p.Forwards))
	}
	if p.Forwards[0].HostID != p.Hosts[0].ID {
		t.Errorf("the rule points at %q, but the host it came with is %q",
			p.Forwards[0].HostID, p.Hosts[0].ID)
	}
}

// A document with a rule it cannot honour is refused whole, before anything
// is written, and the complaint names the host it is on.
func TestABadForwardIsRefusedNamingTheHost(t *testing.T) {
	for _, c := range []struct {
		name string
		f    Forward
	}{
		{"no kind", Forward{Listen: "5432", Dest: "db:5432"}},
		{"unknown kind", Forward{Kind: "sideways", Listen: "5432", Dest: "db:5432"}},
		{"listen is not a port", Forward{Kind: "local", Listen: "http", Dest: "db:5432"}},
		{"no destination", Forward{Kind: "local", Listen: "5432"}},
		{"destination has no port", Forward{Kind: "local", Listen: "5432", Dest: "db"}},
		{"port out of range", Forward{Kind: "local", Listen: "70000", Dest: "db:5432"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			d := Document{Version: 1, Hosts: []Host{{Name: "db-01", Addr: "10.0.0.1", Forwards: []Forward{c.f}}}}
			p, err := Merge(d, nil, nil, nil)
			if err == nil {
				t.Fatalf("accepted, planning %+v", p.Forwards)
			}
			if !strings.Contains(err.Error(), "db-01") {
				t.Errorf("the complaint does not name the host: %v", err)
			}
			if len(p.Hosts) != 0 {
				t.Errorf("the host was planned anyway: %+v", p.Hosts)
			}
		})
	}
}

// A dynamic rule chooses its destination per connection, so the document must
// not demand one — and must not write an empty one either.
func TestADynamicForwardTravelsWithoutADestination(t *testing.T) {
	hosts := []store.Host{{ID: "h1", Name: "db-01", Addr: "10.0.0.1"}}
	rules := []store.Forward{{ID: "f1", HostID: "h1", Kind: store.ForwardDynamic, ListenPort: 1080}}

	raw, err := Export(nil, hosts, rules).YAML()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "dest:") {
		t.Errorf("a dynamic rule was written with a destination:\n%s", raw)
	}
	d, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	p, err := Merge(d, nil, hosts, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Forwards) != 1 || p.Forwards[0].Kind != store.ForwardDynamic || p.Forwards[0].Spec() != "1080" {
		t.Fatalf("got %+v", p.Forwards)
	}
}

// The report says what it is writing, so a forward is not folded into the
// host's line.
func TestAForwardIsReportedAsItsOwnChange(t *testing.T) {
	d := Document{Version: 1, Hosts: []Host{{
		Name: "db-01", Addr: "10.0.0.1",
		Forwards: []Forward{{Kind: "local", Listen: "5432", Dest: "db.internal:5432"}},
	}}}
	p, err := Merge(d, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var line string
	for _, c := range p.Changes {
		if c.Kind == "forward" {
			line = c.String()
		}
	}
	if line == "" {
		t.Fatalf("no forward in the report: %+v", p.Changes)
	}
	for _, want := range []string{"add", "forward", "db-01", "local", "5432"} {
		if !strings.Contains(line, want) {
			t.Errorf("the line %q does not mention %q", line, want)
		}
	}
}

// A document says what it needs to be read, not what wrote it. An omassh that
// does not know forwards rejects the unknown field with the name of a Go type
// and no hint that upgrading is the answer — the version is what turns that
// into a sentence, and only a document that would genuinely be misread should
// pay for it.
func TestADocumentDeclaresTheVersionItNeeds(t *testing.T) {
	hosts := []store.Host{{ID: "h1", Name: "db-01", Addr: "10.0.0.1"}}

	if got := Export(nil, hosts, nil).Version; got != 1 {
		t.Errorf("a list with no forwards is version %d — an older omassh could have read it", got)
	}

	withRules := []store.Forward{
		{ID: "f1", HostID: "h1", Kind: store.ForwardLocal, ListenPort: 5432, Dest: "db", DestPort: 5432},
	}
	if got := Export(nil, hosts, withRules).Version; got != 2 {
		t.Errorf("a list carrying forwards is version %d, want 2", got)
	}

	// And this omassh still reads what the old one wrote.
	if _, err := Parse([]byte("version: 1\nhosts:\n  - name: web\n    addr: 10.0.0.1\n")); err != nil {
		t.Errorf("a version 1 document was refused: %v", err)
	}
	// A document from a later omassh is refused in words.
	_, err := Parse([]byte("version: 99\nhosts: []\n"))
	if err == nil || !strings.Contains(err.Error(), "upgrade omassh") {
		t.Errorf("a newer document gave %v, want it to say to upgrade", err)
	}
}

// The version has to be read before the fields are, or it can never do its
// job: a document from a later omassh carries fields this one has not heard
// of, and a strict decode rejects one of those first — naming a Go type where
// the version would have said to upgrade.
func TestAVersionFromTheFutureIsReadBeforeItsFields(t *testing.T) {
	raw := []byte("version: 99\nhosts:\n  - name: web\n    addr: 10.0.0.1\n    somethingNew: yes\n")

	_, err := Parse(raw)
	if err == nil {
		t.Fatal("a document from the future was accepted")
	}
	if !strings.Contains(err.Error(), "upgrade omassh") {
		t.Errorf("said %q, want it to say to upgrade", err)
	}
	if strings.Contains(err.Error(), "not found in type") {
		t.Errorf("the field error won the race: %q", err)
	}
}
