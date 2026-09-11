package portable

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cuonggt/omassh/internal/store"
)

func hostsFixture() []store.Host {
	return []store.Host{
		{Name: "bastion", Addr: "bastion.example.com", Port: 2222, User: "ops", Identity: "~/.ssh/id_bastion"},
		{Name: "prod-web", Addr: "10.0.1.14", User: "deploy", ProxyJump: "bastion"},
		{Name: "plain", Addr: "10.9.9.9"},
	}
}

// The block goes first because ssh keeps the first value it finds for each
// setting. Below a Host * the exported hosts would take that block's user and
// port instead of their own, and nothing on screen would say so.
func TestTheBlockIsWrittenAboveTheRestOfTheFile(t *testing.T) {
	existing := "Host *\n    User someone-else\n"
	p, err := ExportSSHConfig([]byte(existing), nil, hostsFixture())
	if err != nil {
		t.Fatal(err)
	}
	got := string(p.Content)

	if !strings.HasPrefix(got, blockStart) {
		t.Fatalf("the block is not first:\n%s", got)
	}
	// Looked for after the end marker: the block's own header explains the
	// rule by quoting "Host *", which is not the user's block.
	_, tail, ok := strings.Cut(got, blockEnd)
	if !ok {
		t.Fatal("no end marker in the output")
	}
	if !strings.Contains(tail, "Host *") {
		t.Error("the user's own blocks came before omassh's, so they would outrank it")
	}
}

// Everything outside the markers has to come back exactly: a config file is
// one of the few things on a machine worse to reformat than to leave alone.
func TestWhatTheUserWroteSurvivesByteForByte(t *testing.T) {
	existing := "# my own notes\n\nHost *\n\tServerAliveInterval 60   # trailing comment\n\n\n# end\n"
	p, err := ExportSSHConfig([]byte(existing), nil, hostsFixture())
	if err != nil {
		t.Fatal(err)
	}

	_, tail, ok := strings.Cut(string(p.Content), blockEnd+"\n")
	if !ok {
		t.Fatal("no end marker in the output")
	}
	if got := strings.TrimPrefix(tail, "\n"); got != existing {
		t.Errorf("the file was reformatted:\n--- want ---\n%q\n--- got ---\n%q", existing, got)
	}
}

// Running it twice must change nothing, or every export shows up as a diff in
// whatever keeps the file.
func TestExportingTwiceChangesNothing(t *testing.T) {
	first, err := ExportSSHConfig([]byte("Host *\n    User me\n"), nil, hostsFixture())
	if err != nil {
		t.Fatal(err)
	}
	second, err := ExportSSHConfig(first.Content, nil, hostsFixture())
	if err != nil {
		t.Fatal(err)
	}
	if string(first.Content) != string(second.Content) {
		t.Errorf("a second export differs:\n--- first ---\n%s\n--- second ---\n%s", first.Content, second.Content)
	}
	if strings.Count(string(second.Content), blockStart) != 1 {
		t.Error("the block was written twice rather than replaced")
	}
}

// A host the file already declares is left to it. The block goes on top, so
// writing one would silently take over an entry ssh has been using.
func TestAnAliasTheFileAlreadyDeclaresIsLeftAlone(t *testing.T) {
	declared := map[string]bool{"bastion": true}
	p, err := ExportSSHConfig(nil, declared, hostsFixture())
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(string(p.Content), "bastion.example.com") {
		t.Error("omassh wrote over an alias the file declares itself")
	}
	if len(p.Left) != 1 || p.Left[0].Name != "bastion" {
		t.Fatalf("Left = %+v, want bastion", p.Left)
	}
	if !strings.Contains(p.Left[0].Why, "already defines it") {
		t.Errorf("the reason given is %q", p.Left[0].Why)
	}
	if len(p.Written) != 2 {
		t.Errorf("wrote %v, want the other two", p.Written)
	}
}

// ssh refuses a destination with whitespace in it however the config quotes
// it, and a name holding a pattern character would govern hosts omassh knows
// nothing about. Neither is written, and both say why.
func TestANameThatCannotBeAnSSHAliasIsNotWritten(t *testing.T) {
	// Every one of these is refused by ssh as a destination — checked against
	// ssh itself, which answers "hostname contains invalid characters".
	hosts := []store.Host{
		{Name: "my server", Addr: "10.0.0.1"},
		{Name: "prod-*", Addr: "10.0.0.2"},
		{Name: "who?", Addr: "10.0.0.3"},
		{Name: `we"b`, Addr: "10.0.0.5"},
		{Name: "we'b", Addr: "10.0.0.6"},
		{Name: `we\b`, Addr: "10.0.0.7"},
		{Name: "fine", Addr: "10.0.0.4"},
	}
	p, err := ExportSSHConfig(nil, nil, hosts)
	if err != nil {
		t.Fatal(err)
	}

	if len(p.Written) != 1 || p.Written[0] != "fine" {
		t.Errorf("Written = %v, want only the usable one", p.Written)
	}
	for _, bad := range []string{"my server", "prod-*", "who?", `we"b`, "we'b", `we\\b`} {
		if strings.Contains(string(p.Content), bad) {
			t.Errorf("%q reached the config", bad)
		}
	}
	if len(p.Left) != 6 {
		t.Fatalf("Left = %+v, want six", p.Left)
	}
	for _, l := range p.Left {
		if !strings.Contains(l.Why, "ssh alias") {
			t.Errorf("%s was left out for %q", l.Name, l.Why)
		}
	}
}

// The stanza carries what ssh needs and nothing it would ignore. Port 22 is
// what ssh does anyway, so saying it is noise.
func TestTheStanzaSaysOnlyWhatSshNeeds(t *testing.T) {
	got := sshEntry(store.Host{Name: "web", Addr: "10.0.0.1", Port: 22, User: "", Identity: "", ProxyJump: ""})
	want := "Host web\n    HostName 10.0.0.1\n"
	if got != want {
		t.Errorf("sshEntry = %q, want %q", got, want)
	}

	full := sshEntry(store.Host{Name: "web", Addr: "10.0.0.1", Port: 2222, User: "deploy",
		Identity: "/tmp/a key/id", ProxyJump: "bastion"})
	for _, want := range []string{
		"Port 2222", "User deploy", "ProxyJump bastion",
		// Quoted, or ssh reads a path with a space in it as two arguments.
		`IdentityFile "/tmp/a key/id"`,
	} {
		if !strings.Contains(full, want) {
			t.Errorf("stanza is missing %q:\n%s", want, full)
		}
	}
}

// A file with no block of ours is left exactly as it is by stripBlock, and an
// export into nothing produces just the block.
func TestStripBlockLeavesAStrangerAlone(t *testing.T) {
	s := "Host a\n    HostName 1.2.3.4\n"
	got, err := stripBlock(s)
	if err != nil {
		t.Fatal(err)
	}
	if got != s {
		t.Errorf("stripBlock changed a file with no block: %q", got)
	}
}

// DeclaredAliases has to ignore omassh's own block, or the second export
// would find every host already declared and write none of them.
func TestDeclaredAliasesIgnoresOmasshsOwnBlock(t *testing.T) {
	dir := t.TempDir()
	p, err := ExportSSHConfig([]byte("Host mine\n    HostName 10.0.0.1\n"), nil, hostsFixture())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config")
	if err := os.WriteFile(path, p.Content, 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := DeclaredAliases(path)
	if err != nil {
		t.Fatal(err)
	}
	if !got["mine"] {
		t.Error("the user's own alias was not seen")
	}
	for _, a := range []string{"bastion", "prod-web", "plain"} {
		if got[a] {
			t.Errorf("%q was counted as the file's own, though omassh wrote it", a)
		}
	}
}

// Include is followed, so an alias hidden in another file is not shadowed by a
// block written above it.
func TestDeclaredAliasesFollowsInclude(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "extra.conf", "Host hidden\n    HostName 10.0.0.9\n")
	path := write(t, dir, "config", "Include extra.conf\n\nHost visible\n    HostName 10.0.0.8\n")

	got, err := DeclaredAliases(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range []string{"hidden", "visible"} {
		if !got[a] {
			t.Errorf("%q was not seen", a)
		}
	}
}

// A file that is not there yet declares nothing, which is what a first export
// meets.
func TestDeclaredAliasesOfAMissingFile(t *testing.T) {
	got, err := DeclaredAliases(filepath.Join(t.TempDir(), "absent"))
	if err != nil || len(got) != 0 {
		t.Errorf("DeclaredAliases = %v, %v", got, err)
	}
}

// A start marker whose end had been deleted — by hand, by a truncation, by a
// merge going wrong — meant everything after it read as omassh's, so the rest
// of the file was dropped and the export reported a clean write. There is no
// getting a ~/.ssh/config back from that.
func TestAnUnclosedBlockIsRefusedRatherThanSwallowingTheFile(t *testing.T) {
	cases := map[string]string{
		"never closed":      blockStart + "\nHost stale\n\nHost precious\n    HostName 10.0.0.1\n",
		"end with no start": "Host precious\n    HostName 10.0.0.1\n" + blockEnd + "\n",
		"opened twice":      blockStart + "\nHost a\n" + blockStart + "\nHost b\n" + blockEnd + "\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := stripBlock(body); err == nil {
				t.Fatal("accepted a file whose markers do not pair up")
			}
			_, err := ExportSSHConfig([]byte(body), nil, hostsFixture())
			if err == nil {
				t.Fatal("the export went ahead anyway")
			}
			// The line number is the whole of what makes it fixable.
			if !strings.Contains(err.Error(), "line ") {
				t.Errorf("the error does not say where: %v", err)
			}
		})
	}
}

// A ProxyJump naming one of your hosts is only worth writing if that host is
// written too. Left out for a name ssh cannot use, it took its dependants into
// a config referring to an alias nothing declared — and dropping the ProxyJump
// instead would be worse, dialling a machine meant to sit behind a bastion.
func TestAHostWhoseJumpHostIsNotInTheFileIsLeftOut(t *testing.T) {
	hosts := []store.Host{
		{Name: "my bastion", Addr: "bastion.example.com"}, // unusable as an alias
		{Name: "behind-it", Addr: "10.0.0.5", ProxyJump: "my bastion"},
		{Name: "further", Addr: "10.0.0.6", ProxyJump: "behind-it"}, // stranded in turn
		{Name: "yours", Addr: "10.0.0.7", ProxyJump: "declared-by-you"},
		{Name: "outside", Addr: "10.0.0.8", ProxyJump: "ops@edge.example.com"},
	}
	p, err := ExportSSHConfig(nil, map[string]bool{"declared-by-you": true}, hosts)
	if err != nil {
		t.Fatal(err)
	}

	// A jump host your own config declares, and one that is no host of yours
	// at all, are both ssh's to resolve and stay.
	want := map[string]bool{"yours": true, "outside": true}
	if len(p.Written) != len(want) {
		t.Fatalf("Written = %v, want %v", p.Written, want)
	}
	for _, n := range p.Written {
		if !want[n] {
			t.Errorf("%q was written though its jump host is not in the file", n)
		}
	}
	// And the chain behind the unusable name goes with it, one hop at a time.
	reasons := map[string]string{}
	for _, l := range p.Left {
		reasons[l.Name] = l.Why
	}
	for _, n := range []string{"behind-it", "further"} {
		if !strings.Contains(reasons[n], "jump host") {
			t.Errorf("%s was left out for %q", n, reasons[n])
		}
	}
	if !strings.Contains(string(p.Content), "ProxyJump ops@edge.example.com") {
		t.Error("an ssh destination as a jump host was not written")
	}
}
