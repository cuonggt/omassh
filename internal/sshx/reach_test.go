package sshx

import (
	"slices"
	"testing"
	"time"

	"github.com/cuonggt/omassh/internal/store"
)

// A probe through a jump host asks the hop for a stdio forward and nothing
// else: the destination on the command line is the hop, reached by its own
// port and key, and the host being probed appears only as the address the hop
// is to connect to. Nothing authenticates to the host itself.
func TestProbeThroughAsksTheHopForAForward(t *testing.T) {
	hop := store.Host{Name: "bastion", Addr: "bastion.corp", Port: 2222, User: "admin", Identity: "/keys/id"}
	h := store.Host{Addr: "10.0.1.14", User: "root", ProxyJump: "bastion", Jump: &hop}

	args, got, ok := ProbeThrough(h, 2*time.Second)
	if !ok {
		t.Fatal("a host whose jump host is one of your own cannot be asked about")
	}
	want := []string{
		"-o", "BatchMode=yes", "-o", "ConnectTimeout=2",
		"-p", "2222", "-i", "/keys/id",
		"-W", "[10.0.1.14]:22", "admin@bastion.corp",
	}
	if !slices.Equal(args, want) {
		t.Errorf("ProbeThrough() = %q, want %q", args, want)
	}
	if got.Addr != hop.Addr {
		t.Errorf("hop = %q, want %q — the askpass helper is set up for whoever is connected to", got.Addr, hop.Addr)
	}
}

// ssh takes whole seconds, and a zero there means its own default — which is
// the system's, and can run to minutes. A probe given less than a second
// still bounds the dial.
func TestProbeThroughNeverLeavesTheDialUnbounded(t *testing.T) {
	hop := store.Host{Addr: "bastion.corp"}
	h := store.Host{Addr: "10.0.1.14", ProxyJump: "bastion", Jump: &hop}

	args, _, ok := ProbeThrough(h, 200*time.Millisecond)
	if !ok {
		t.Fatal("not probed")
	}
	if !slices.Contains(args, "ConnectTimeout=1") {
		t.Errorf("args = %q, want a ConnectTimeout of at least a second", args)
	}
}

// Asking a hop means connecting to it, which takes its address, its port, its
// key and its user. A jump host that is one of your own hosts is where those
// are written down; a hop named as free text is a destination for ssh to
// resolve, and a route in ssh's own config is one only ssh can follow. Both
// stay unprobed rather than guessed at.
func TestProbeThroughDeclinesAHopItCannotSpell(t *testing.T) {
	for what, h := range map[string]store.Host{
		"no route at all":             {Addr: "10.0.1.14"},
		"a hop named as free text":    {Addr: "10.0.1.14", ProxyJump: "bastion.corp:2222"},
		"a route in ssh's own config": {Addr: "10.0.1.14", ConfigProxy: "aws ssm start-session --target %h"},
	} {
		if _, _, ok := ProbeThrough(h, time.Second); ok {
			t.Errorf("%s: probed through a hop omassh cannot spell", what)
		}
	}
}
