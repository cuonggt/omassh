package sshx

import (
	"net"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/cuonggt/omassh/internal/store"
)

func TestForwardArgs(t *testing.T) {
	h := store.Host{Addr: "10.0.1.14", User: "admin", Port: 2222}
	f := store.Forward{Kind: store.ForwardLocal, ListenPort: 5432, Dest: "db.internal", DestPort: 5432}

	got := ForwardArgs(h, f)

	// The tunnel itself, spelled the way ssh spells it.
	if i := slices.Index(got, "-L"); i < 0 || i+1 >= len(got) || got[i+1] != "5432:db.internal:5432" {
		t.Errorf("the rule is missing or malformed in %v", got)
	}
	// A forward has no remote command to run.
	if !slices.Contains(got, "-N") {
		t.Errorf("-N is missing from %v", got)
	}
	// It runs where nobody can answer a prompt, and a port it cannot bind is
	// a failure rather than a connection carrying nothing.
	for _, want := range []string{"BatchMode=yes", "ExitOnForwardFailure=yes"} {
		if !slices.Contains(got, want) {
			t.Errorf("%s is missing from %v", want, got)
		}
	}
	// Everything Build knows about reaching this host still applies.
	if got[len(got)-1] != "admin@10.0.1.14" {
		t.Errorf("the target is not last in %v", got)
	}
	if !slices.Contains(got, "-p") {
		t.Errorf("the host's port was dropped: %v", got)
	}
}

// A tunnel reaches its host by the same route a session does, because it is
// built the same way. Were it not, a forward to a host behind a bastion would
// dial the address directly — which either fails or, worse, reaches whatever
// answers at that address on this side of the network.
func TestForwardGoesThroughTheJumpChain(t *testing.T) {
	jump := store.Host{Name: "bastion", Addr: "edge.example.com", User: "ops", Port: 2222}
	h := store.Host{Addr: "10.0.1.14", User: "admin", Jump: &jump}
	f := store.Forward{Kind: store.ForwardLocal, ListenPort: 5432, Dest: "localhost", DestPort: 5432}

	got := strings.Join(ForwardArgs(h, f), " ")

	if !strings.Contains(got, "ProxyCommand=") {
		t.Fatalf("no ProxyCommand in %s", got)
	}
	if !strings.Contains(got, "-W '[10.0.1.14]:22'") {
		t.Errorf("the hop is not told this host's address: %s", got)
	}
	if !strings.Contains(got, "ops@edge.example.com") {
		t.Errorf("the hop itself is missing: %s", got)
	}
}

// A dynamic forward has no destination, and writing one would make a spec ssh
// rejects.
func TestForwardArgsForADynamicRule(t *testing.T) {
	got := ForwardArgs(store.Host{Addr: "h"}, store.Forward{Kind: store.ForwardDynamic, ListenPort: 1080})
	if i := slices.Index(got, "-D"); i < 0 || got[i+1] != "1080" {
		t.Errorf("got %v", got)
	}
}

// ssh binds the port inside a session nobody is watching, so a port already
// taken came back as a tunnel that vanished a moment after starting. Saying so
// first is the difference between a sentence and a mystery.
func TestListenAvailable(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	taken := l.Addr().(*net.TCPAddr).Port

	busy := store.Forward{Kind: store.ForwardLocal, Listen: "127.0.0.1", ListenPort: taken}
	err = ListenAvailable(busy)
	if err == nil {
		t.Fatal("a port already listening was reported as free")
	}
	if !strings.Contains(err.Error(), strconv.Itoa(taken)) {
		t.Errorf("the complaint does not name the port: %v", err)
	}

	// The far side is the one place this cannot look, so it must not guess:
	// a remote forward binds its port on the host, where a local listener
	// says nothing at all about it.
	remote := store.Forward{Kind: store.ForwardRemote, Listen: "127.0.0.1", ListenPort: taken,
		Dest: "localhost", DestPort: 80}
	if err := ListenAvailable(remote); err != nil {
		t.Errorf("a remote rule was judged by a local port: %v", err)
	}
}

func TestListenAvailableAcceptsAFreePort(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()

	f := store.Forward{Kind: store.ForwardLocal, Listen: "127.0.0.1", ListenPort: port}
	if err := ListenAvailable(f); err != nil {
		t.Errorf("a free port was refused: %v", err)
	}
	// And having looked, it must leave the port free for ssh to take.
	if err := ListenAvailable(f); err != nil {
		t.Errorf("the check kept the port to itself: %v", err)
	}
}
