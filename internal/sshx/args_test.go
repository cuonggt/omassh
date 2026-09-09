package sshx

import (
	"slices"
	"strings"
	"testing"

	"github.com/cuonggt/omassh/internal/store"
)

func TestBuild(t *testing.T) {
	tests := []struct {
		name  string
		host  store.Host
		extra []string
		want  []string
	}{
		{
			name: "default port is left implicit",
			host: store.Host{Addr: "example.com", Port: 22, User: "root"},
			want: []string{"root@example.com"},
		},
		{
			name: "zero port is treated as default",
			host: store.Host{Addr: "example.com", User: "root"},
			want: []string{"root@example.com"},
		},
		{
			name: "non-default port",
			host: store.Host{Addr: "example.com", Port: 2222, User: "root"},
			want: []string{"-p", "2222", "root@example.com"},
		},
		{
			name: "no user means no @ prefix",
			host: store.Host{Addr: "example.com"},
			want: []string{"example.com"},
		},
		{
			name: "identity and jump host",
			host: store.Host{Addr: "10.0.1.14", User: "admin", Identity: "~/.ssh/id_ed25519", ProxyJump: "bastion.corp"},
			want: []string{"-i", "~/.ssh/id_ed25519", "-J", "bastion.corp", "admin@10.0.1.14"},
		},
		{
			name:  "extra flags precede the target",
			host:  store.Host{Addr: "example.com", User: "root"},
			extra: []string{"-N", "-L", "5432:db:5432"},
			want:  []string{"-N", "-L", "5432:db:5432", "root@example.com"},
		},
		{
			name: "ipv6 literal is passed through untouched",
			host: store.Host{Addr: "2001:db8::1", User: "root", Port: 2222},
			want: []string{"-p", "2222", "root@2001:db8::1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Build(tt.host, tt.extra...)
			if !slices.Equal(got, tt.want) {
				t.Errorf("Build() = %q, want %q", got, tt.want)
			}
		})
	}
}

// Build must not retain or alias the caller's extra slice.
func TestBuildDoesNotAliasExtra(t *testing.T) {
	extra := []string{"-N", "-L", "1:2:3"}
	before := slices.Clone(extra)
	Build(store.Host{Addr: "h", User: "u"}, extra...)
	if !slices.Equal(extra, before) {
		t.Errorf("Build mutated extra: %q, want %q", extra, before)
	}
}

// -o settings must reach every connection path.
func TestGlobalOptions(t *testing.T) {
	t.Cleanup(func() { SetGlobalOptions(nil) })
	SetGlobalOptions([]string{"ConnectTimeout=5", "BatchMode=yes"})

	got := Build(store.Host{Addr: "example.com", User: "root"})
	want := []string{"-o", "ConnectTimeout=5", "-o", "BatchMode=yes", "root@example.com"}
	if !slices.Equal(got, want) {
		t.Errorf("Build() = %q, want %q", got, want)
	}

	SetGlobalOptions(nil)
	if got := Build(store.Host{Addr: "h"}); !slices.Equal(got, []string{"h"}) {
		t.Errorf("after reset Build() = %q", got)
	}
}

// The jump host's own key, port and user must reach the hop. ssh -J passes
// only -l, -p and -v to it, which is why the connection is spelled out.
func TestBuildSpellsOutTheJumpConnection(t *testing.T) {
	jump := store.Host{Name: "bastion", Addr: "10.0.0.1", User: "jump", Port: 2222, Identity: "/keys/b"}
	h := store.Host{Name: "web", Addr: "10.0.1.9", User: "deploy", Identity: "/keys/w", Jump: &jump}

	got := strings.Join(Build(h), " ")
	want := "-i /keys/w -o ProxyCommand=ssh -p 2222 -i /keys/b -W '[10.0.1.9]:22' jump@10.0.0.1 deploy@10.0.1.9"
	if got != want {
		t.Errorf("Build() = %q\nwant           %q", got, want)
	}
}

// A destination Omassh does not know stays a plain -J, since ssh understands
// it already.
func TestBuildUsesPlainJumpForAnUnknownDestination(t *testing.T) {
	h := store.Host{Name: "web", Addr: "10.0.1.9", ProxyJump: "ops@edge.example.com:2222"}

	got := Build(h)
	if !slices.Contains(got, "-J") || !slices.Contains(got, "ops@edge.example.com:2222") {
		t.Errorf("Build() = %q, want a -J with the raw destination", got)
	}
}

// A key path with a space would otherwise split into two arguments inside the
// ProxyCommand, which the shell runs.
func TestBuildQuotesAJumpHostPathWithSpaces(t *testing.T) {
	jump := store.Host{Name: "bastion", Addr: "10.0.0.1", Identity: "/my keys/id ed25519"}
	h := store.Host{Name: "web", Addr: "10.0.1.9", Jump: &jump}

	got := strings.Join(Build(h), " ")
	if !strings.Contains(got, `'/my keys/id ed25519'`) {
		t.Errorf("Build() = %q, want the path quoted", got)
	}
}

// The shell that runs a ProxyCommand globs, and zsh fails outright on a
// bracket pattern that matches nothing, so -W has to arrive quoted.
func TestBuildQuotesTheForwardAddress(t *testing.T) {
	jump := store.Host{Name: "bastion", Addr: "10.0.0.1"}
	h := store.Host{Name: "web", Addr: "10.0.1.9", Jump: &jump}

	got := strings.Join(Build(h), " ")
	if strings.Contains(got, "-W [10.0.1.9]:22") {
		t.Errorf("Build() = %q, want the -W address quoted against globbing", got)
	}
	if !strings.Contains(got, `-W '[10.0.1.9]:22'`) {
		t.Errorf("Build() = %q, want the -W address quoted", got)
	}
}

// Each hop is told to reach the *next* one. ssh expands %h and %p across the
// whole ProxyCommand, nested levels included, so writing them meant every hop
// in a chain was handed the final destination: the first dialled it directly
// and the ones between were never contacted. A bastion that forwards only to
// its next hop refused outright, and nothing connected.
func TestEachHopInAChainReachesTheNextOne(t *testing.T) {
	hopA := store.Host{Name: "hopA", Addr: "10.0.0.1", Port: 2201, User: "u"}
	hopB := store.Host{Name: "hopB", Addr: "10.0.0.2", Port: 2202, User: "u", Jump: &hopA}
	target := store.Host{Name: "target", Addr: "10.0.0.3", Port: 2203, User: "u", Jump: &hopB}

	got := strings.Join(Build(target), " ")

	// hopB's address has to appear as somewhere a hop is asked to reach. It
	// never did: every level was handed the final destination, so hopA dialled
	// the target directly and hopB was never contacted at all.
	if !strings.Contains(got, "[10.0.0.2]:2202") {
		t.Errorf("nothing in the chain is asked to reach hopB:\n  %s", got)
	}
	if !strings.Contains(got, "[10.0.0.3]:2203") {
		t.Errorf("nothing in the chain is asked to reach the target:\n  %s", got)
	}
	if strings.Contains(got, "%h") || strings.Contains(got, "%p") {
		t.Errorf("an address is still left for ssh to substitute:\n  %s", got)
	}
	// And it is hopA's own leg of the chain that reaches hopB, quoting and all.
	inner := shellQuote("ProxyCommand=" + proxyCommand(hopA, forwardTarget(hopB)))
	if !strings.Contains(got, inner) {
		t.Errorf("hopA's leg is not the one pointed at hopB:\n  %s", got)
	}
}

// A hop on the default port still gets a port written out, since the address
// has to be complete for -W.
func TestAHopOnTheDefaultPortIsStillWrittenOut(t *testing.T) {
	jump := store.Host{Name: "bastion", Addr: "10.0.0.1"}
	h := store.Host{Name: "web", Addr: "10.0.1.9", Jump: &jump}

	if got := strings.Join(Build(h), " "); !strings.Contains(got, "'[10.0.1.9]:22'") {
		t.Errorf("Build() = %q, want the destination port spelled out", got)
	}
}
