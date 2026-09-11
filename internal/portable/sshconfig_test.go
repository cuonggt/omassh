package portable

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// write puts a config file in a temp dir and returns its path.
func write(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestReadingAnSSHConfig(t *testing.T) {
	dir := t.TempDir()
	path := write(t, dir, "config", `
Host *
  User admin
  ServerAliveInterval 60

Host web1 web2
  HostName 10.0.0.1
  Port 2222
  IdentityFile ~/.ssh/id_ed25519

Host db-*
  HostName db.example.com

Match host bastion
  User root

Host plain

Host behind
  HostName 10.0.0.9
  ProxyJump bastion.example.com

Host explicit-22
  HostName 10.0.0.5
  Port 22
`)
	d, err := FromSSHConfig(path)
	if err != nil {
		t.Fatal(err)
	}

	byName := map[string]Host{}
	for _, h := range d.Hosts {
		byName[h.Name] = h
	}

	// A wildcard block is settings for other hosts, not a host.
	if _, ok := byName["db-*"]; ok {
		t.Error("imported a wildcard pattern as a host")
	}
	// Match names a condition, not a machine. bastion here is a rule about a
	// host, and importing it would invent one that was never declared.
	if _, ok := byName["bastion"]; ok {
		t.Error("imported a Match block as a host")
	}
	if len(d.Hosts) != 5 {
		t.Fatalf("got %d hosts (%v), want web1 web2 plain behind explicit-22", len(d.Hosts), byName)
	}

	// Settings from `Host *` reach every alias, which is the reason for
	// resolving values through the parser rather than reading blocks.
	if got := byName["web1"].User; got != "admin" {
		t.Errorf("web1 user = %q, want admin inherited from Host *", got)
	}
	if got := byName["web1"].Port; got != 2222 {
		t.Errorf("web1 port = %d, want 2222", got)
	}
	if got := byName["web1"].Identity; got != "~/.ssh/id_ed25519" {
		t.Errorf("web1 identity = %q", got)
	}
	// Both aliases on one Host line are machines.
	if byName["web2"].Addr != "10.0.0.1" {
		t.Errorf("web2 did not get the address from a shared Host line")
	}
	// No HostName means the alias is the address.
	if got := byName["plain"].Addr; got != "plain" {
		t.Errorf("plain addr = %q, want the alias itself", got)
	}
	if got := byName["behind"].Jump; got != "bastion.example.com" {
		t.Errorf("behind jump = %q", got)
	}
	// 22 is what ssh does anyway; storing it would only add noise to exports.
	if got := byName["explicit-22"].Port; got != 0 {
		t.Errorf("explicit-22 port = %d, want 0", got)
	}
}

func TestIncludedFilesAreRead(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "conf.d/10-work.conf", "Host work\n  HostName 10.1.1.1\n")
	write(t, dir, "conf.d/20-home.conf", "Host home\n  HostName 10.2.2.2\n")
	write(t, dir, "orbstack", "Host orb\n  HostName 198.19.249.2\n")
	// A config that is nothing but includes is the case that matters: it is
	// what the machine this was written on actually has.
	path := write(t, dir, "config", "Include orbstack\nInclude conf.d/*.conf\nInclude missing-entirely\n")

	d, err := FromSSHConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, h := range d.Hosts {
		names[h.Name] = true
	}
	for _, want := range []string{"work", "home", "orb"} {
		if !names[want] {
			t.Errorf("%q was not imported from an Include; got %v", want, names)
		}
	}
}

func TestAnAliasDeclaredTwiceIsOneHost(t *testing.T) {
	dir := t.TempDir()
	path := write(t, dir, "config", "Host web\n  HostName 10.0.0.1\n\nHost web\n  User admin\n")

	d, err := FromSSHConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Hosts) != 1 {
		t.Fatalf("got %d hosts, want 1", len(d.Hosts))
	}
	// Merge would reject a document naming the same host twice, so this has
	// to be settled here.
	if _, err := Merge(d, nil, nil, nil); err != nil {
		t.Fatalf("the document it produced does not merge: %v", err)
	}
}

// `HostName %h.internal` is how an estate of machines gets one block instead
// of thirty. ssh expands the token in the setting and never in the destination
// it is finally handed, so imported literally those hosts carried an address
// with a %h in it — reported as a clean import, and unable to resolve
// anything. The expansions are ssh's own, checked against ssh -G.
func TestHostNameTokensAreExpandedTheWaySshExpandsThem(t *testing.T) {
	cases := []struct{ raw, alias, want string }{
		{"%h.internal", "web1", "web1.internal"},
		{"%h.internal", "web2", "web2.internal"},
		{"pre-%h", "t", "pre-t"},
		{"%h.%h", "t", "t.t"},
		{"%%literal", "t", "%literal"},
		{"10.0.0.1", "t", "10.0.0.1"},
		{"", "t", ""},
		// ssh accepts no other token here — it refuses the file outright — so
		// nothing is invented for one.
		{"%r.foo", "t", "%r.foo"},
		{"%p", "t", "%p"},
		// A trailing percent is not the start of anything.
		{"host%", "t", "host%"},
	}
	for _, c := range cases {
		if got := hostName(c.raw, c.alias); got != c.want {
			t.Errorf("hostName(%q, %q) = %q, want %q", c.raw, c.alias, got, c.want)
		}
	}
}

// And end to end: one block naming two machines becomes two hosts, each
// carrying its own address rather than the pattern they share.
func TestABlockOfMachinesImportsWithTheirOwnAddresses(t *testing.T) {
	dir := t.TempDir()
	path := write(t, dir, "config", `
Host web1 web2
  HostName %h.internal.example.com
  User deploy
`)
	d, err := FromSSHConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"web1": "web1.internal.example.com", "web2": "web2.internal.example.com"}
	if len(d.Hosts) != len(want) {
		t.Fatalf("imported %d hosts, want %d: %+v", len(d.Hosts), len(want), d.Hosts)
	}
	for _, h := range d.Hosts {
		if h.Addr != want[h.Name] {
			t.Errorf("%s has addr %q, want %q", h.Name, h.Addr, want[h.Name])
		}
		if strings.Contains(h.Addr, "%") {
			t.Errorf("%s kept a token in its address: %q", h.Name, h.Addr)
		}
	}
}

// IdentityFile is the one setting here ssh accumulates rather than decides:
// a host under both `Host *` and its own block gets both keys offered, in file
// order, until one is accepted. Omassh keeps one, and taking the first meant a
// config written the usual way — catch-all at the top — handed every host the
// generic key and lost the one under its own name. Against a server that only
// accepts the specific key, ssh connected and omassh could not.
func TestAKeyWrittenForAHostBeatsTheCatchAllOne(t *testing.T) {
	dir := t.TempDir()
	for _, order := range []struct{ name, body string }{{
		"catch-all first", `
Host *
  IdentityFile ~/.ssh/id_generic

Host bastion
  HostName edge.example.com
  IdentityFile ~/.ssh/id_bastion
`}, {
		"catch-all last", `
Host bastion
  HostName edge.example.com
  IdentityFile ~/.ssh/id_bastion

Host *
  IdentityFile ~/.ssh/id_generic
`}} {
		t.Run(order.name, func(t *testing.T) {
			doc, err := FromSSHConfig(write(t, dir, "config", order.body))
			if err != nil {
				t.Fatal(err)
			}
			if len(doc.Hosts) != 1 {
				t.Fatalf("Hosts = %+v, want just the one named outright", doc.Hosts)
			}
			if got := doc.Hosts[0].Identity; got != "~/.ssh/id_bastion" {
				t.Errorf("identity = %q, want the key written under the host's own name", got)
			}
		})
	}
}

// A host with no key of its own still takes the one the pattern covering it
// gives, because that is the key ssh would use for it.
func TestAHostWithNoKeyOfItsOwnTakesThePatternsKey(t *testing.T) {
	dir := t.TempDir()
	doc, err := FromSSHConfig(write(t, dir, "config", `
Host *
  IdentityFile ~/.ssh/id_generic

Host plain
  HostName plain.example.com
`))
	if err != nil {
		t.Fatal(err)
	}
	if got := doc.Hosts[0].Identity; got != "~/.ssh/id_generic" {
		t.Errorf("identity = %q, want the one the pattern gives it", got)
	}
}

// And a key from a Match block is left where it is. Match is evaluated at
// connection time against things a host list cannot know — the local user, the
// network, the output of a command — so a key behind one is not this host's
// key, it is this host's key under conditions nobody here can check.
func TestAKeyBehindAMatchBlockIsNotTakenAsTheHostsOwn(t *testing.T) {
	dir := t.TempDir()
	doc, err := FromSSHConfig(write(t, dir, "config", `
Host *
  IdentityFile ~/.ssh/id_generic

Host prod-db
  HostName db.example.com

Match host prod-db exec "test -f /tmp/on-call"
  IdentityFile ~/.ssh/id_oncall
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Hosts) != 1 || doc.Hosts[0].Name != "prod-db" {
		t.Fatalf("Hosts = %+v, want only prod-db", doc.Hosts)
	}
	if got := doc.Hosts[0].Identity; got != "~/.ssh/id_generic" {
		t.Errorf("identity = %q, want the unconditional one", got)
	}
}
