package keymap

import (
	"strings"
	"testing"
)

func TestDefaults(t *testing.T) {
	m := Default()

	if got := m.Lookup("enter"); got != Connect {
		t.Errorf("enter = %q, want connect", got)
	}
	if got := m.Lookup("j"); got != Down {
		t.Errorf("j = %q, want down", got)
	}
	if got := m.Lookup("nothing-bound"); got != None {
		t.Errorf("unbound key = %q, want None", got)
	}
	if got := m.Key(Connect); got != "enter" {
		t.Errorf("Key(connect) = %q", got)
	}
}

func TestOverrideRebinds(t *testing.T) {
	m, err := New(map[string]string{"connect": "c", "search": "z"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := m.Lookup("c"); got != Connect {
		t.Errorf("c = %q, want connect", got)
	}
	if got := m.Lookup("z"); got != Search {
		t.Errorf("z = %q, want search", got)
	}
	// The displaced default must stop working, or two keys would silently do
	// the same thing and the help would be wrong.
	if got := m.Lookup("enter"); got != None {
		t.Errorf("enter still bound to %q after rebinding connect", got)
	}
	if got := m.Key(Connect); got != "c" {
		t.Errorf("Key(connect) = %q, want c", got)
	}
}

func TestArrowsAndCtrlCAlwaysWork(t *testing.T) {
	// "m" is free; "n" is already new, and rebinding onto it would rightly
	// be refused as a conflict.
	m, err := New(map[string]string{"down": "m"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := m.Lookup("down"); got != Down {
		t.Errorf("down arrow = %q, want down (arrows survive a rebind)", got)
	}
	if got := m.Lookup("m"); got != Down {
		t.Errorf("m = %q, want down", got)
	}
	if got := m.Lookup("ctrl+c"); got != Quit {
		t.Errorf("ctrl+c = %q, want quit", got)
	}
}

func TestReservedKeysAreRefused(t *testing.T) {
	for _, k := range []string{"ctrl+c", "up", "down"} {
		if _, err := New(map[string]string{"connect": k}); err == nil {
			t.Errorf("New accepted a binding to the reserved key %q", k)
		}
	}
}

func TestConflictsAreReported(t *testing.T) {
	// "e" is already edit.
	_, err := New(map[string]string{"connect": "e"})
	if err == nil {
		t.Fatal("New accepted two actions on one key")
	}
	t.Logf("reported: %v", err)
}

func TestUnknownActionIsReported(t *testing.T) {
	_, err := New(map[string]string{"teleport": "t"})
	if err == nil {
		t.Fatal("New accepted an unknown action")
	}
}

func TestEmptyKeyIsReported(t *testing.T) {
	if _, err := New(map[string]string{"connect": "  "}); err == nil {
		t.Error("New accepted an empty key")
	}
}

func TestNamesCoversEveryDefault(t *testing.T) {
	if len(Names()) != len(defaults) {
		t.Errorf("Names() has %d entries, defaults has %d", len(Names()), len(defaults))
	}
}

// A new built-in binding lands on some key, and a file that had already
// claimed that key for something else stops the program at startup — rightly,
// since dropping one of the two would leave a key doing something the file
// does not say. What turns that into a line to change is knowing which of the
// two came from Omassh rather than from the file.
func TestAClashNamesTheDefault(t *testing.T) {
	_, err := New(map[string]string{"search": "q"}) // q is quit's default
	if err == nil {
		t.Fatal("a key claimed twice was accepted")
	}
	msg := err.Error()
	for _, want := range []string{`"q"`, "quit", "search", "built-in default"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the message does not mention %s: %s", want, msg)
		}
	}
	// And the one to move is the default, not the one the file asked for.
	if strings.Contains(msg, `"search" is a built-in default`) {
		t.Errorf("the message blames the file's own choice: %s", msg)
	}

	// Two keys the file itself claimed twice is an ordinary mistake, with no
	// default to point at.
	_, err = New(map[string]string{"search": "m", "sftp": "m"})
	if err == nil {
		t.Fatal("a file claiming one key twice was accepted")
	}
	if strings.Contains(err.Error(), "built-in default") {
		t.Errorf("a clash between two overrides blamed a default: %v", err)
	}
}
