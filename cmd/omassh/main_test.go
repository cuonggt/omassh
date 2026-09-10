package main

import (
	"flag"
	"io"
	"path/filepath"
	"strings"
	"testing"
)

// resetFlags gives each case a flag set of its own. browse defines its flags
// on the global one, which panics the second time round.
func resetFlags() {
	flag.CommandLine = flag.NewFlagSet("omassh", flag.ContinueOnError)
	flag.CommandLine.SetOutput(io.Discard)
}

// flag stops at the first bare word, so a command written after the flags was
// parsed away and the browser opened instead — on the database that had been
// asked for, which is exactly what made it look like it had worked. The import
// silently never happened.
func TestACommandWrittenAfterTheFlagsIsRefused(t *testing.T) {
	db := filepath.Join(t.TempDir(), "x.db")
	for _, args := range [][]string{
		{"-db", db, "import", "hosts.yaml"},
		{"-db", db, "export"},
		{"-o", "ConnectTimeout=5", "import-ssh-config"},
	} {
		resetFlags()
		err := run(args)
		if err == nil {
			t.Errorf("run(%q) opened the browser rather than saying the command was dropped", args)
			continue
		}
		if !strings.Contains(err.Error(), "comes first") {
			t.Errorf("run(%q) says %v, which does not say how to write it", args, err)
		}
	}
}

// A bare word that is not a command is just as dropped, and just as silent.
func TestAnArgumentTheBrowserCannotUseIsRefused(t *testing.T) {
	resetFlags()
	err := run([]string{"-version", "hosts.yaml"})
	if err == nil {
		t.Fatal("a stray argument was accepted")
	}
	if !strings.Contains(err.Error(), "hosts.yaml") {
		t.Errorf("says %v, without naming what it could not use", err)
	}
}

// Nothing on its own still browses; the check must not refuse the ordinary way
// of starting the program.
func TestFlagsAloneStillOpenTheBrowser(t *testing.T) {
	resetFlags()
	// -print-config returns before any terminal is needed, so this reaches the
	// end of browse without opening one.
	if err := run([]string{"-config", filepath.Join(t.TempDir(), "c.yaml"), "-print-config"}); err != nil {
		t.Errorf("plain flags were refused: %v", err)
	}
}

// A command omassh does not have is named as such rather than dispatched.
func TestAnUnknownCommandIsNamed(t *testing.T) {
	resetFlags()
	err := run([]string{"improt", "hosts.yaml"})
	if err == nil {
		t.Fatal("a misspelt command was accepted")
	}
	// Said as a command, not as a stray word the browser could not use: the
	// two reach different messages, and this one is the typo people make.
	if !strings.Contains(err.Error(), `unknown command "improt"`) {
		t.Errorf("run said %v", err)
	}
}

// The usage text and the dispatch table have to agree, or omassh offers a
// command it does not have.
func TestEveryCommandIsInTheUsageText(t *testing.T) {
	for name := range commands {
		if !strings.Contains(usage, "omassh "+name+" ") {
			t.Errorf("%q is dispatched but the usage text does not offer it", name)
		}
	}
}
