package probe_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/cuonggt/omassh/internal/probe"
	"github.com/cuonggt/omassh/internal/store"
)

func listening(t *testing.T) (string, int) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	return "127.0.0.1", l.Addr().(*net.TCPAddr).Port
}

func closedPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port
}

func TestCheck(t *testing.T) {
	addr, open := listening(t)
	shut := closedPort(t)
	ctx := context.Background()

	if got := probe.Check(ctx, store.Host{Addr: addr, Port: open}, time.Second); got != probe.Up {
		t.Errorf("open port = %v, want up", got)
	}
	if got := probe.Check(ctx, store.Host{Addr: addr, Port: shut}, time.Second); got != probe.Down {
		t.Errorf("closed port = %v, want down", got)
	}
	if got := probe.Check(ctx, store.Host{}, time.Second); got != probe.Unknown {
		t.Errorf("host with no address = %v, want unknown", got)
	}
}

// Dialling a jump-host's address directly would report on whatever answers at
// that address locally, which is worse than saying nothing.
func TestProxiedHostsAreSkipped(t *testing.T) {
	addr, open := listening(t)

	viaJump := store.Host{Addr: addr, Port: open, ProxyJump: "bastion"}
	if got := probe.Check(context.Background(), viaJump, time.Second); got != probe.Skipped {
		t.Errorf("jump-host host = %v, want skipped", got)
	}

	viaCommand := store.Host{Addr: addr, Port: open, Note: "reached via ProxyCommand"}
	if got := probe.Check(context.Background(), viaCommand, time.Second); got != probe.Skipped {
		t.Errorf("ProxyCommand host = %v, want skipped", got)
	}
}

func TestCheckAll(t *testing.T) {
	addr, open := listening(t)
	shut := closedPort(t)

	hosts := []store.Host{
		{ID: "up", Name: "up", Addr: addr, Port: open},
		{ID: "down", Name: "down", Addr: addr, Port: shut},
		{ID: "jump", Name: "jump", Addr: addr, Port: open, ProxyJump: "b"},
	}

	seen := map[string]probe.State{}
	got := probe.CheckAll(context.Background(), hosts, 2, time.Second,
		func(key string, s probe.State) { seen[key] = s })

	if len(got) != 3 {
		t.Fatalf("got %d results, want 3", len(got))
	}
	if got["up"] != probe.Up || got["down"] != probe.Down || got["jump"] != probe.Skipped {
		t.Errorf("states = %v", got)
	}
	if len(seen) != 3 {
		t.Errorf("reported %d results, want 3", len(seen))
	}
}

func TestCheckAllHonoursCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// 203.0.113.0/24 is reserved for documentation and never routes, so this
	// would otherwise sit until the timeout.
	hosts := []store.Host{{ID: "x", Addr: "203.0.113.1", Port: 22}}
	start := time.Now()
	probe.CheckAll(ctx, hosts, 1, 30*time.Second, nil)

	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("took %s — cancellation was ignored", elapsed)
	}
}
