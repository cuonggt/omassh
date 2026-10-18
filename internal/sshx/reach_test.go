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

// A hop named as free text rather than as one of your own hosts is a
// destination, and asking it means connecting to it — so the destination is
// read back into a host and the connection spelled out, which is what ssh does
// for the inner command of its own -J.
func TestProbeThroughAsksAHopNamedAsFreeText(t *testing.T) {
	h := store.Host{Addr: "10.0.1.14", ProxyJump: "ops@edge.example.com:2222"}

	args, hop, ok := ProbeThrough(h, 2*time.Second)
	if !ok {
		t.Fatal("a hop written as a destination was not asked")
	}
	want := []string{
		"-o", "BatchMode=yes", "-o", "ConnectTimeout=2",
		"-p", "2222", "-W", "[10.0.1.14]:22", "ops@edge.example.com",
	}
	if !slices.Equal(args, want) {
		t.Errorf("ProbeThrough() = %q, want %q", args, want)
	}
	if hop.Addr != "edge.example.com" || hop.Port != 2222 || hop.User != "ops" {
		t.Errorf("hop = %+v, want the destination taken apart", hop)
	}
}

// A ProxyJump in ssh's own config is a destination too, so the probe can ask
// it. The file is still the only thing that knows it was a ProxyJump and not a
// ProxyCommand, which is why the resolver carries that along.
func TestProbeThroughAsksAJumpHostYourSSHConfigNames(t *testing.T) {
	h := store.Host{Addr: "10.0.1.14", ConfigProxy: store.ConfigRoute{Via: "bastion", Jump: true}}

	args, _, ok := ProbeThrough(h, 2*time.Second)
	if !ok {
		t.Fatal("a ProxyJump from ssh's own config was not asked")
	}
	want := []string{
		"-o", "BatchMode=yes", "-o", "ConnectTimeout=2",
		"-W", "[10.0.1.14]:22", "bastion",
	}
	if !slices.Equal(args, want) {
		t.Errorf("ProbeThrough() = %q, want %q", args, want)
	}
}

// A ProxyJump may name hop after hop. The last is the one to ask, since it is
// the one that can reach the host, and the rest are how to reach it — which is
// what -J is for, so ssh nests them itself.
func TestProbeThroughAsksTheLastHopOfAChain(t *testing.T) {
	h := store.Host{Addr: "10.0.1.14", ProxyJump: "edge,ops@bastion:2222"}

	args, hop, ok := ProbeThrough(h, 2*time.Second)
	if !ok {
		t.Fatal("a chain of hops was not asked")
	}
	want := []string{
		"-o", "BatchMode=yes", "-o", "ConnectTimeout=2",
		"-p", "2222", "-J", "edge", "-W", "[10.0.1.14]:22", "ops@bastion",
	}
	if !slices.Equal(args, want) {
		t.Errorf("ProbeThrough() = %q, want %q", args, want)
	}
	if hop.Addr != "bastion" {
		t.Errorf("hop = %+v, want the last of the chain", hop)
	}
}

// A ProxyCommand is a program only ssh knows how to run, to a host whose
// address may be an instance id and not an address at all, and a destination
// omassh cannot read is one it would have to guess at. Neither is asked.
func TestProbeThroughDeclinesWhatItCannotAsk(t *testing.T) {
	for what, h := range map[string]store.Host{
		"no route at all": {Addr: "10.0.1.14"},
		"a program only ssh can run": {Addr: "10.0.1.14",
			ConfigProxy: store.ConfigRoute{Via: "aws ssm start-session --target %h"}},
		"a hop that is not a destination": {Addr: "10.0.1.14", ProxyJump: "bastion:nope"},
	} {
		if _, _, ok := ProbeThrough(h, time.Second); ok {
			t.Errorf("%s: asked a hop omassh cannot reach", what)
		}
	}
}
