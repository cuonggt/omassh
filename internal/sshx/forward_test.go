package sshx

import (
	"net"
	"os"
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
	// Go's raw error names the port too, so naming it is not enough to show
	// the message was written for a person.
	if strings.Contains(err.Error(), "listen tcp") {
		t.Errorf("Go's own error shape reached the message: %v", err)
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

// ssh takes the first value it is given for a setting, so what leads the argv
// is what holds. Two of a forward's options are not preferences: without them
// "running" stops meaning "carrying", and the interface reported a tunnel as
// up while its ssh sat at a password prompt in a pane nobody was looking at.
func TestAForwardsLoadBearingOptionsCannotBeOverridden(t *testing.T) {
	t.Cleanup(func() { SetGlobalOptions(nil) })
	SetGlobalOptions([]string{"BatchMode=no", "ExitOnForwardFailure=no", "ServerAliveInterval=99"})

	got := ForwardArgs(store.Host{Addr: "10.0.1.14"},
		store.Forward{Kind: store.ForwardLocal, ListenPort: 5432, Dest: "db", DestPort: 5432})

	first := func(prefix string) int {
		for i, a := range got {
			if strings.HasPrefix(a, prefix) {
				return i
			}
		}
		return -1
	}
	for _, o := range []struct{ ours, theirs string }{
		{"BatchMode=yes", "BatchMode=no"},
		{"ExitOnForwardFailure=yes", "ExitOnForwardFailure=no"},
	} {
		ours, theirs := first(o.ours), first(o.theirs)
		if ours < 0 || theirs < 0 {
			t.Fatalf("expected both %q and %q in %v", o.ours, o.theirs, got)
		}
		if ours > theirs {
			t.Errorf("%q comes after %q, so the override wins: %v", o.ours, o.theirs, got)
		}
	}

	// The keepalives are a preference, so someone who tuned their own keeps it.
	if mine, yours := first("ServerAliveInterval=30"), first("ServerAliveInterval=99"); yours > mine {
		t.Errorf("a tuned keepalive was overridden: %v", got)
	}
}

// ssh spells "every interface" with a star and Go spells it with nothing, so
// the check has to translate. Left alone, net.Listen would reject "*:port" and
// every such rule would be refused as a port already in use.
func TestListenAvailableUnderstandsTheStar(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()

	f := store.Forward{Kind: store.ForwardLocal, Listen: "*", ListenPort: port}
	if err := ListenAvailable(f); err != nil {
		t.Errorf("a free port bound to every interface was refused: %v", err)
	}
}

// The kernel's answers reach the interface as sentences, not as Go's own
// error shape. Each of these is an ordinary thing to get wrong, and the raw
// form named the symptom rather than the reason.
func TestListenAvailableExplainsWhatTheKernelSaid(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root, so a privileged port is not privileged")
	}
	for _, c := range []struct {
		name string
		f    store.Forward
		want string
	}{
		{
			"a port below 1024",
			store.Forward{Kind: store.ForwardLocal, Listen: "127.0.0.1", ListenPort: 443},
			"needs root",
		},
		{
			// TEST-NET-1, which no interface is ever configured with.
			"an address this machine does not have",
			store.Forward{Kind: store.ForwardLocal, Listen: "192.0.2.1", ListenPort: 8080},
			"no interface here has the address",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := ListenAvailable(c.f)
			if err == nil {
				t.Fatal("the bind was reported as available")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("said %q, want it to mention %q", err, c.want)
			}
			if strings.Contains(err.Error(), "listen tcp") {
				t.Errorf("Go's own error shape reached the message: %q", err)
			}
		})
	}
}

// A hop is reached by an ssh of its own, and options given to the outer one do
// not reach it. BatchMode has to, or the prompt it exists to refuse simply
// moves one connection further in: a bastion asking for a passphrase left that
// inner ssh waiting inside a detached pane, with the tunnel reading as running
// and carrying nothing — the very failure the outer BatchMode prevents.
func TestAForwardsLoadBearingOptionsReachEveryHop(t *testing.T) {
	hopA := store.Host{Name: "edge", Addr: "10.0.0.1", User: "ops", Port: 2222}
	hopB := store.Host{Name: "inner", Addr: "10.0.0.2", User: "ops", Jump: &hopA}
	h := store.Host{Addr: "10.0.1.14", User: "admin", Jump: &hopB}
	f := store.Forward{Kind: store.ForwardLocal, ListenPort: 5432, Dest: "db", DestPort: 5432}

	got := strings.Join(ForwardArgs(h, f), " ")

	// One per ssh in the chain: the tunnel itself and both hops. A hop behind
	// a hop is the same failure one level deeper, so it is counted too.
	if n := strings.Count(got, "BatchMode=yes"); n != 3 {
		t.Errorf("BatchMode reaches %d of the 3 ssh invocations:\n  %s", n, got)
	}
}
