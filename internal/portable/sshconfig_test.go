package portable

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cuonggt/omassh/internal/store"
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
	if _, err := Merge(d, nil, nil, nil, nil, nil); err != nil {
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

// A host the file names is reached by that name, so what is read for each name
// has to be where ssh goes with it: the HostName with %h expanded, the name
// itself where there is none, and the port. omassh's own block is left out,
// since everything in it came from omassh, and so is any name ssh would not
// take as a destination, since it is no way to reach anything.
func TestAConfigSaysWhereEachOfItsNamesGoes(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "conf.d/orb", "Host orb\n  HostName 198.19.249.2\n")
	path := write(t, dir, "config", `# >>> omassh >>>
Host from-omassh
    HostName 10.9.9.9
# <<< omassh <<<

Include conf.d/*

Host prod-web
    HostName 10.0.0.5
    ForwardAgent yes

Host db1 db2
    HostName %h.internal
    Port 2222

Host Plain

Host web-*
    User deploy

Host semi;colon
    HostName 10.0.0.6
`)
	c, err := ReadSSHConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	got := c.Aliases()
	byName := map[string]store.ConfigAlias{}
	for _, a := range got {
		byName[a.Name] = a
	}
	want := map[string]store.ConfigAlias{
		"orb":      {Name: "orb", Addr: "198.19.249.2", Port: 22},
		"prod-web": {Name: "prod-web", Addr: "10.0.0.5", Port: 22},
		"db1":      {Name: "db1", Addr: "db1.internal", Port: 2222},
		"db2":      {Name: "db2", Addr: "db2.internal", Port: 2222},
		// Spelled as written: ssh matches a Host line exactly, so the name
		// handed to it has to be this one and not omassh's spelling of it.
		"Plain": {Name: "Plain", Addr: "Plain", Port: 22},
	}
	if len(byName) != len(want) {
		t.Errorf("Aliases = %+v, want exactly %v", got, want)
	}
	for name, w := range want {
		if byName[name] != w {
			t.Errorf("%s = %+v, want %+v", name, byName[name], w)
		}
	}

	none, err := ReadSSHConfig(filepath.Join(dir, "not-there"))
	if err != nil || len(none.Aliases()) != 0 {
		t.Errorf("a missing file gave %v, %v; it names nothing", none.Aliases(), err)
	}
}

// An address behind a jump host means something only from the far side of it,
// so what the file sends each name through has to be what ssh would: the first
// ProxyJump or ProxyCommand of the blocks that apply to the name, with none
// counting as the first — which is how a bastion is let out of the catch-all
// that sends everything else through it. ssh -G gives the same answer for
// every name here but the one in omassh's own block, which ssh reads and this
// deliberately does not.
func TestAConfigSaysWhatItSendsEachNameThrough(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "conf.d/ssm", "Host i-*\n    ProxyCommand aws ssm start-session --target %h\n")
	path := write(t, dir, "config", `Include conf.d/*

# >>> omassh >>>
Host from-omassh
    ProxyJump somewhere-stale
# <<< omassh <<<

Host bastion
    HostName 203.0.113.1
    ProxyJump none

Host direct
    HostName 10.0.2.2
    ProxyCommand none

Host prod-web
    HostName 10.0.0.5
    ProxyJump bastion # the far side

Host 10.0.1.*
    ProxyCommand ssh -W %h:%p bastion

Match host on-call exec "test -f /nonexistent"
    ProxyJump pager

Host *
    ProxyJump bastion
`)
	c, err := ReadSSHConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	for dest, want := range map[string]string{
		"prod-web": "bastion",
		// By the address a host is handed to ssh as, when nothing names it.
		"10.0.1.7": "ssh -W %h:%p bastion",
		"10.9.9.9": "bastion",
		// From an Include, and ahead of the catch-all's ProxyJump, which a
		// ProxyCommand met first keeps from taking effect.
		"i-0123456789abcdef0": "aws ssm start-session --target %h",
		// none is a route too, and the first one met.
		"bastion": "",
		"direct":  "",
		// A Match that holds only some of the time is read as not holding.
		"on-call": "bastion",
		// What omassh wrote is not read back as the file's own.
		"from-omassh": "bastion",
	} {
		if got := c.Proxy(dest); got != want {
			t.Errorf("Proxy(%q) = %q, want %q", dest, got, want)
		}
	}

	none, err := ReadSSHConfig(filepath.Join(dir, "not-there"))
	if got := none.Proxy("prod-web"); err != nil || got != "" {
		t.Errorf("a missing file sent prod-web through %q, %v; it sends nothing anywhere", got, err)
	}
}

// A Match on anything but the host is ordinary OpenSSH — `Match exec` is how a
// gpg agent learns its terminal — and the parser refused the whole file at one,
// so a config ssh reads every day could not be imported at all. Such a block is
// read as one whose condition is false: nothing in it is a machine, and none
// of its settings are taken, since they hold only for some connections.
func TestAMatchOnAnythingButTheHostDoesNotStopAConfigBeingRead(t *testing.T) {
	dir := t.TempDir()
	path := write(t, dir, "config", `Match exec "gpg-connect-agent updatestartuptty /bye"
    User from-exec

Match user bob
    User from-user

Match final
    Port 2200

Match canonical all
    User from-canonical

Match localnetwork 10.0.0.0/8
    HostName 10.7.7.7

Match tagged work
    User from-tagged

Match host web user bob
    User from-host-and-user

Host web
    HostName 10.0.0.5
    User deploy
`)
	d, err := FromSSHConfig(path)
	if err != nil {
		t.Fatalf("a config ssh reads was refused: %v", err)
	}
	if len(d.Hosts) != 1 || d.Hosts[0].Name != "web" {
		t.Fatalf("Hosts = %+v, want only web", d.Hosts)
	}
	web := d.Hosts[0]
	if web.User != "deploy" || web.Addr != "10.0.0.5" || web.Port != 0 {
		t.Errorf("web = %+v, want deploy@10.0.0.5 and nothing from a block that holds only some of the time", web)
	}

	// The same file is one export can write beside and a connection can read.
	if _, err := DeclaredAliases(path); err != nil {
		t.Errorf("DeclaredAliases: %v", err)
	}
	c, err := ReadSSHConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if as := c.Aliases(); len(as) != 1 || as[0] != (store.ConfigAlias{Name: "web", Addr: "10.0.0.5", Port: 22}) {
		t.Errorf("Aliases = %+v, want web at 10.0.0.5:22", as)
	}
}

// originalhost is the name as typed, which for an alias is the alias, so it can
// be answered here — and a block under it that sets a HostName ahead of the
// alias's own decides where ssh goes. A Match on the host alone is still read
// too, comment and all.
func TestAMatchThatCanBeAnsweredIsStillRead(t *testing.T) {
	dir := t.TempDir()
	d, err := FromSSHConfig(write(t, dir, "config", `Match originalhost web
    HostName 10.6.6.6

Match host web # the far side
    User deploy

Host web
    HostName 10.0.0.5
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Hosts) != 1 {
		t.Fatalf("Hosts = %+v, want only web", d.Hosts)
	}
	// ssh -G web says 10.6.6.6 for this file: the first HostName wins.
	if got := d.Hosts[0].Addr; got != "10.6.6.6" {
		t.Errorf("addr = %q, want 10.6.6.6, where ssh goes", got)
	}
	if got := d.Hosts[0].User; got != "deploy" {
		t.Errorf("user = %q, want deploy from the Match on the host", got)
	}
}
