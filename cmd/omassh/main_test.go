package main

import (
	"flag"
	"io"
	"os"
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

// -n is how you look before the one command that writes to ~/.ssh/config
// does, so it has to leave the file exactly as it was.
func TestExportSSHConfigDryRunWritesNothing(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config")
	const original = "# my own\nHost mine\n    HostName 1.2.3.4\n"
	if err := os.WriteFile(cfg, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	resetFlags()
	if err := run([]string{"export-ssh-config", "-db", filepath.Join(dir, "x.db"), "-o", cfg, "-n"}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != original {
		t.Errorf("a dry run changed the file:\n%s", got)
	}
}

// And the file it writes is one ssh will agree to read: a config group or
// world can reach is refused by ssh outright.
func TestExportSSHConfigWritesAPrivateFile(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "deeper", "config")

	resetFlags()
	if err := run([]string{"export-ssh-config", "-db", filepath.Join(dir, "x.db"), "-o", cfg}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode is %o, want 600", perm)
	}
}

// A dotfiles setup commonly symlinks ~/.ssh/config into a repository.
// Renaming over the link replaced it with an ordinary file, detaching the
// config from the repository meant to be tracking it — and the tracked copy
// never saw a word of what had been written.
func TestExportSSHConfigFollowsASymlinkedConfig(t *testing.T) {
	dir := t.TempDir()
	repo := filepath.Join(dir, "dotfiles", "ssh_config")
	link := filepath.Join(dir, "home", "config")
	for _, d := range []string{filepath.Dir(repo), filepath.Dir(link)} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	const tracked = "# tracked in my dotfiles\nHost mine\n    HostName 1.2.3.4\n"
	if err := os.WriteFile(repo, []byte(tracked), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(repo, link); err != nil {
		t.Fatal(err)
	}

	resetFlags()
	if err := run([]string{"export-ssh-config", "-db", filepath.Join(dir, "x.db"), "-o", link}); err != nil {
		t.Fatal(err)
	}

	fi, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Error("the symlink was replaced by an ordinary file")
	}
	body, err := os.ReadFile(repo)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), ">>> omassh") {
		t.Error("the tracked file never saw what was written")
	}
	if !strings.Contains(string(body), "tracked in my dotfiles") {
		t.Error("the tracked file lost what was already in it")
	}
}

// -n promises to write nothing, and that has to include scratch files.
// Scanning the config for the aliases it declares once wrote a copy of it
// beside the original — in ~/.ssh, and during a dry run.
func TestExportSSHConfigDryRunLeavesNoScratchFiles(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config")
	if err := os.WriteFile(cfg, []byte("Host mine\n    HostName 1.2.3.4\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	before, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	resetFlags()
	if err := run([]string{"export-ssh-config", "-db", filepath.Join(dir, "x.db"), "-o", cfg, "-n"}); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	// The database is made by opening the store; nothing else may appear.
	names := map[string]bool{}
	for _, e := range before {
		names[e.Name()] = true
	}
	for _, e := range after {
		if !names[e.Name()] && e.Name() != "x.db" {
			t.Errorf("a dry run left %q beside the config", e.Name())
		}
	}
}
