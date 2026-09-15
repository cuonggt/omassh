package store

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestPutSnippetMintsAnIdAndKeepsIt(t *testing.T) {
	s := openTest(t)

	sn, err := s.PutSnippet(Snippet{Name: "disk free", Script: "df -h /"})
	if err != nil {
		t.Fatal(err)
	}
	if sn.ID == "" {
		t.Fatal("no id was minted")
	}

	sn.Name = "free space"
	again, err := s.PutSnippet(sn)
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != sn.ID {
		t.Errorf("the id changed on edit: %q then %q", sn.ID, again.ID)
	}
	got, _ := s.Snippets()
	if len(got) != 1 {
		t.Fatalf("%d snippets, want the one edited in place", len(got))
	}
}

// Names are how records are matched across machines, so two cannot share one.
func TestASecondSnippetByTheSameNameIsRefused(t *testing.T) {
	s := openTest(t)
	if _, err := s.PutSnippet(Snippet{Name: "Disk free", Script: "df -h /"}); err != nil {
		t.Fatal(err)
	}
	_, err := s.PutSnippet(Snippet{Name: "disk free", Script: "df -h"})
	if err == nil {
		t.Fatal("a second snippet called disk free was accepted")
	}
	if !strings.Contains(err.Error(), "already called") {
		t.Errorf("err = %v, want it to say the name is taken", err)
	}
}

// A snippet is a script. One without is a name that does nothing, and the
// complaint is what the form shows.
func TestASnippetWithNothingToRunIsRefused(t *testing.T) {
	s := openTest(t)
	for _, script := range []string{"", "   ", "\n\n", "\t\n "} {
		if _, err := s.PutSnippet(Snippet{Name: "empty", Script: script}); err == nil {
			t.Errorf("a snippet whose script was %q was accepted", script)
		}
	}
}

// Deleting one is the whole of it: nothing points at a snippet, so nothing is
// left pointing at a gap. A credential needs a transaction that clears every
// host and group naming it; this needs none of that, and the test is here to
// say that is by design rather than by omission.
func TestDeletingASnippetTouchesNothingElse(t *testing.T) {
	s := openTest(t)
	keep, err := s.PutSnippet(Snippet{Name: "keep", Script: "uptime"})
	if err != nil {
		t.Fatal(err)
	}
	drop, err := s.PutSnippet(Snippet{Name: "drop", Script: "w"})
	if err != nil {
		t.Fatal(err)
	}
	h, err := s.PutHost(Host{Name: "web", Addr: "10.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteSnippet(drop.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Snippets()
	if len(got) != 1 || got[0].ID != keep.ID {
		t.Fatalf("snippets after the delete = %v, want only %q", got, keep.Name)
	}
	hosts, _ := s.Hosts()
	if len(hosts) != 1 || hosts[0].ID != h.ID {
		t.Errorf("deleting a snippet disturbed the hosts: %v", hosts)
	}
}

// Open creates whatever bucket is missing, which is how a database written
// before snippets existed gains them without a version check anywhere.
func TestADatabaseWithoutSnippetsGainsThem(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")

	first, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.PutHost(Host{Name: "web", Addr: "10.0.0.1"}); err != nil {
		t.Fatal(err)
	}
	first.Close()

	again, err := Open(path)
	if err != nil {
		t.Fatalf("reopening a database without the bucket: %v", err)
	}
	defer again.Close()
	snippets, err := again.Snippets()
	if err != nil {
		t.Fatalf("Snippets on an upgraded database: %v", err)
	}
	if len(snippets) != 0 {
		t.Errorf("%d snippets in a database that never had any", len(snippets))
	}
	if _, err := again.PutSnippet(Snippet{Name: "new", Script: "uptime"}); err != nil {
		t.Errorf("writing into the new bucket: %v", err)
	}
}

// Nothing here quotes, trims or rewrites any part of a script: what comes back
// is what went in, tabs, blank lines, quotes and all. A shell script is
// whitespace-sensitive in places — a heredoc body most of all — so a store
// that tidied one would change what it does.
func TestASnippetsScriptComesBackExactly(t *testing.T) {
	s := openTest(t)
	script := "set -e\n\tcat <<'EOF' > /tmp/x\n  keep   these   spaces\n\nEOF\necho \"done\"\n"

	sn, err := s.PutSnippet(Snippet{Name: "heredoc", Script: script})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Snippets()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("%d snippets, want 1", len(got))
	}
	if got[0].Script != script {
		t.Errorf("the script came back changed:\n got %q\nwant %q", got[0].Script, script)
	}
	if got[0].ID != sn.ID {
		t.Errorf("id = %q, want %q", got[0].ID, sn.ID)
	}
}

// The row beside the name shows the command where there is one and the size
// where there is more, so a long script does not need the whole list to widen.
func TestASnippetDescribesItselfByItsCommandOrItsSize(t *testing.T) {
	for _, tc := range []struct {
		script string
		lines  int
		want   string
	}{
		{"", 0, ""},
		{"df -h /", 1, "df -h /"},
		{"  df -h /  ", 1, "df -h /"},
		// A trailing newline is what an editor leaves behind. A snippet that
		// became "2 lines" by being saved would be describing the editor.
		{"df -h /\n", 1, "df -h /"},
		{"set -e\nsystemctl restart nginx", 2, "2 lines"},
		{"set -e\nsystemctl restart nginx\n", 2, "2 lines"},
		{"a\n\nb\n", 3, "3 lines"},
	} {
		sn := Snippet{Name: "x", Script: tc.script}
		if got := sn.Lines(); got != tc.lines {
			t.Errorf("Lines(%q) = %d, want %d", tc.script, got, tc.lines)
		}
		if got := sn.Describe(); got != tc.want {
			t.Errorf("Describe(%q) = %q, want %q", tc.script, got, tc.want)
		}
	}
}
