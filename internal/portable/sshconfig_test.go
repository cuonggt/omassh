package portable

import (
	"os"
	"path/filepath"
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
	if _, err := Merge(d, nil, nil); err != nil {
		t.Fatalf("the document it produced does not merge: %v", err)
	}
}
