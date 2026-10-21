package ui

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// ctrlG is the key that hands the terminal to ssh-keygen.
func ctrlG(h *harness) { h.send(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl}) }

// A key is named after the record it is for and kept beside the others, which
// is how keys are named in practice and what makes one recognisable later.
func TestAKeyIsNamedAfterWhatItIsFor(t *testing.T) {
	dir := t.TempDir()
	h := newHarness(t, func(o *Options) { o.KeyDir = dir })
	h.addHost("prod web 01", "10.0.0.2")

	h.press("2", "e")
	onField(t, h, keyPathField)

	got, err := h.m.keyPathFor(h.m.form)
	if err != nil {
		t.Fatalf("keyPathFor: %v", err)
	}
	if want := filepath.Join(dir, "prod-web-01"); got != want {
		t.Errorf("keyPathFor = %q, want %q", got, want)
	}
}

// A path already in the field wins: someone who typed one meant it. It is
// expanded on the way, because ssh-keygen does not do it for -f the way ssh
// does for -i.
func TestAPathAlreadyTypedIsWhereTheKeyGoes(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory to expand ~ against")
	}
	h := newHarness(t, func(o *Options) { o.KeyDir = t.TempDir() })
	h.addHost("web", "10.0.0.2")

	h.press("2", "e")
	onField(t, h, keyPathField)
	h.type_("~/keys/deploy")

	got, err := h.m.keyPathFor(h.m.form)
	if err != nil {
		t.Fatalf("keyPathFor: %v", err)
	}
	if want := filepath.Join(home, "keys", "deploy"); got != want {
		t.Errorf("keyPathFor = %q, want %q — a ~ ssh-keygen would refuse", got, want)
	}
}

// With no name to use and nowhere to put it, the form says so rather than
// making a key called something nobody asked for.
func TestWithNothingToNameAKeyAfterTheFormAsks(t *testing.T) {
	dir := t.TempDir()
	for what, o := range map[string]func(*Options){
		"no name yet":       func(o *Options) { o.KeyDir = dir },
		"nowhere to put it": func(o *Options) { o.KeyDir = "" },
	} {
		h := newHarness(t, o)
		h.press("2", "n") // a new host, with nothing typed into it
		onField(t, h, keyPathField)
		ctrlG(h)

		if h.m.form == nil {
			t.Fatalf("%s: the form closed", what)
		}
		if h.m.form.problem == "" {
			t.Errorf("%s: nothing was said about why no key was made", what)
		}
		if strings.Contains(h.m.form.problem, "exit status") {
			t.Errorf("%s: the form shows an exit status: %q", what, h.m.form.problem)
		}
	}
}

// Elsewhere the key is not bound and falls through to the field, as ctrl+e
// does on every form but a snippet's.
func TestTheKeyIsNotBoundAwayFromTheKeyPath(t *testing.T) {
	h := newHarness(t, func(o *Options) { o.KeyDir = t.TempDir() })
	h.addHost("web", "10.0.0.2")

	h.press("2", "e")
	onField(t, h, "Name")
	ctrlG(h)

	if h.m.form == nil {
		t.Fatal("the form closed")
	}
	if h.m.form.problem != "" {
		t.Errorf("ctrl+g on the name field said %q", h.m.form.problem)
	}
}

// What comes back fills the field in and is offered from then on, without the
// reload the picker's list would otherwise be waiting for.
func TestAKeyThatWasMadeFillsTheFieldAndIsOffered(t *testing.T) {
	dir := t.TempDir()
	h := newHarness(t, func(o *Options) { o.KeyDir = dir })
	h.addHost("web", "10.0.0.2")

	h.press("2", "e")
	onField(t, h, keyPathField)
	made := writeKey(t, dir, "web")
	h.send(keyMadeMsg{path: made})

	if got := h.m.form.value(keyPathField); got != made {
		t.Errorf("Identity = %q, want the key that was made (%q)", got, made)
	}
	idx := slices.IndexFunc(h.m.form.fields, func(f field) bool { return f.label == keyPathField })
	if got := h.m.form.fields[idx].choices; !slices.Contains(got, made) {
		t.Errorf("offered %q, want the key just made among them", got)
	}
	if !strings.Contains(h.m.status, "made") {
		t.Errorf("status = %q, want it to say a key was made", h.m.status)
	}
}

// And ssh-keygen having exited badly is reported without its exit status: the
// person watching has just been looking at whatever it said.
func TestAKeyThatWasNotMadeSaysSoWithoutAnExitStatus(t *testing.T) {
	h := newHarness(t, func(o *Options) { o.KeyDir = t.TempDir() })
	h.addHost("web", "10.0.0.2")

	h.press("2", "e")
	onField(t, h, keyPathField)
	h.send(keyMadeMsg{err: &exitErr{}})

	if h.m.form == nil {
		t.Fatal("the form closed")
	}
	switch p := h.m.form.problem; {
	case p == "":
		t.Error("nothing was said about the key not being made")
	case strings.Contains(p, "exit status"):
		t.Errorf("the form shows an exit status: %q", p)
	}
}

// exitErr is what exec gives back for a program that exited non-zero.
type exitErr struct{}

func (*exitErr) Error() string { return "exit status 1" }

// A name has to survive being turned into a filename, and one that leaves
// nothing behind is not a name to make a key under.
func TestAKeyFileNameKeepsWhatAPathCanCarry(t *testing.T) {
	for name, want := range map[string]string{
		"prod-web":     "prod-web",
		"prod web 01":  "prod-web-01",
		"Prod_Web.old": "Prod_Web.old",
		"a/b":          "ab",
		"../../escape": "escape",
		".hidden":      "hidden",
		"  spaced  ":   "spaced",
		"///":          "",
		"":             "",
		"你好":           "",
	} {
		if got := keyFileName(name); got != want {
			t.Errorf("keyFileName(%q) = %q, want %q", name, got, want)
		}
	}
}
