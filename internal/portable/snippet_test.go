package portable

import (
	"strings"
	"testing"

	"github.com/cuonggt/omassh/internal/store"
)

// A script crosses verbatim or it is not the same script. YAML has more than
// one way to write a multi-line string and they disagree about the trailing
// newline, so this goes the whole way round — store to document to text and
// back — rather than trusting the encoder to have chosen well.
func TestAScriptSurvivesTheRoundTrip(t *testing.T) {
	for _, script := range []string{
		"df -h /",
		"df -h /\n",
		"set -e\nsystemctl restart nginx\n",
		"cat <<'EOF' > /tmp/x\n  keep   these   spaces\n\nEOF\n",
		"echo \"quoted\" && echo 'single' # trailing comment",
		"grep -r $'\\t' .",
		"printf '%s\\n' one two\n\n\n",
	} {
		in := []store.Snippet{{ID: "s1", Name: "sample", Script: script}}
		raw, err := Export(nil, nil, nil, nil, in).YAML()
		if err != nil {
			t.Fatalf("%q: %v", script, err)
		}
		d, err := Parse(raw)
		if err != nil {
			t.Fatalf("%q: parsing what we wrote: %v\n%s", script, err, raw)
		}
		p, err := Merge(d, nil, nil, nil, nil, nil)
		if err != nil {
			t.Fatalf("%q: %v", script, err)
		}
		if len(p.Snippets) != 1 {
			t.Fatalf("%q: %d snippets came back, want 1", script, len(p.Snippets))
		}
		if got := p.Snippets[0].Script; got != script {
			t.Errorf("the script changed in the file:\n got %q\nwant %q\n\n%s", got, script, raw)
		}
	}
}

// Importing the same file twice changes nothing the second time, which is what
// makes an export something you can keep re-applying.
func TestImportingTheSameSnippetTwiceChangesNothing(t *testing.T) {
	have := []store.Snippet{{ID: "s1", Name: "disk free", Script: "df -h /\n"}}
	raw, err := Export(nil, nil, nil, nil, have).YAML()
	if err != nil {
		t.Fatal(err)
	}
	d, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}

	p, err := Merge(d, nil, nil, nil, nil, have)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Empty() {
		t.Errorf("re-importing an unchanged snippet planned %v", p.Changes)
	}
	if p.Unchanged != 1 {
		t.Errorf("Unchanged = %d, want 1", p.Unchanged)
	}

	// And the record it matched keeps its id rather than being made again.
	d.Snippets[0].Script = "df -h"
	p, err = Merge(d, nil, nil, nil, nil, have)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Snippets) != 1 || p.Snippets[0].ID != "s1" {
		t.Fatalf("an edited snippet came back as %v, want the id it had", p.Snippets)
	}
	if p.Changes[0].Action != Update {
		t.Errorf("action = %v, want update", p.Changes[0].Action)
	}
}

// Import fills in and corrects; it does not blank things. A document that
// names a snippet without giving a script leaves the one already here.
func TestASnippetWithNoScriptKeepsTheOneAlreadyHere(t *testing.T) {
	have := []store.Snippet{{ID: "s1", Name: "disk free", Script: "df -h /"}}
	d, err := Parse([]byte("version: 3\nsnippets:\n  - name: disk free\n"))
	if err != nil {
		t.Fatal(err)
	}
	p, err := Merge(d, nil, nil, nil, nil, have)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Empty() {
		t.Errorf("a snippet with nothing new in it planned %v", p.Changes)
	}
}

// The same document with nothing already here has no script to fall back on,
// and a snippet that runs nothing is refused rather than written.
func TestASnippetThatArrivesWithNoScriptAtAllIsRefused(t *testing.T) {
	d, err := Parse([]byte("version: 3\nsnippets:\n  - name: disk free\n"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = Merge(d, nil, nil, nil, nil, nil)
	if err == nil {
		t.Fatal("a snippet with no script anywhere was accepted")
	}
	if !strings.Contains(err.Error(), "disk free") {
		t.Errorf("err = %v, want it to name the snippet", err)
	}
}

func TestASnippetWithNoNameOrATwinIsRefused(t *testing.T) {
	for _, tc := range []struct{ doc, want string }{
		{"version: 3\nsnippets:\n  - script: uptime\n", "no name"},
		{"version: 3\nsnippets:\n  - name: a\n    script: uptime\n  - name: A\n    script: w\n", "twice"},
	} {
		d, err := Parse([]byte(tc.doc))
		if err != nil {
			t.Fatalf("%s: %v", tc.doc, err)
		}
		_, err = Merge(d, nil, nil, nil, nil, nil)
		if err == nil {
			t.Fatalf("%s was accepted", tc.doc)
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("err = %v, want it to say %q", err, tc.want)
		}
	}
}

// A document says what it needs rather than what wrote it, so a plain host
// list goes on being readable by an older omassh. A section that one has no
// field for does not: an unknown key is refused before the version is read,
// so the number has to be raised or the complaint is about YAML instead of
// about the version.
func TestOnlyADocumentThatNeedsItAnnouncesTheNewerVersion(t *testing.T) {
	hosts := []store.Host{{ID: "h1", Name: "web", Addr: "10.0.0.1"}}
	snips := []store.Snippet{{ID: "s1", Name: "uptime", Script: "uptime"}}

	if got := Export(nil, hosts, nil, nil, nil).Version; got != 1 {
		t.Errorf("a plain host list announced version %d, want 1", got)
	}
	if got := Export(nil, hosts, nil, nil, snips).Version; got != 3 {
		t.Errorf("a list with snippets announced version %d, want 3", got)
	}
}

// The keys a complaint offers are read off the struct, so a typo inside a
// snippet is answered with the keys a snippet actually takes.
func TestATypoInsideASnippetNamesTheKeysASnippetTakes(t *testing.T) {
	_, err := Parse([]byte("version: 3\nsnippets:\n  - name: a\n    command: uptime\n"))
	if err == nil {
		t.Fatal("an unknown key inside a snippet was accepted")
	}
	for _, want := range []string{"command", "a snippet", "name", "script"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want it to mention %q", err, want)
		}
	}
}
