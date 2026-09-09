package probe_test

import (
	"context"
	"fmt"
	"net"
	"sync"
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
}

func TestCheckAll(t *testing.T) {
	addr, open := listening(t)
	shut := closedPort(t)

	hosts := []store.Host{
		{ID: "up", Name: "up", Addr: addr, Port: open},
		{ID: "down", Name: "down", Addr: addr, Port: shut},
		{ID: "jump", Name: "jump", Addr: addr, Port: open, ProxyJump: "b"},
	}

	// report is called from each worker, so collecting it needs a lock. This
	// test wrote to the map bare, which -race calls what it is.
	var mu sync.Mutex
	seen := map[string]probe.State{}
	got := probe.CheckAll(context.Background(), hosts, 2, time.Second,
		func(key string, s probe.State) {
			mu.Lock()
			defer mu.Unlock()
			seen[key] = s
		})

	mu.Lock()
	defer mu.Unlock()

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

// A cancelled context is not an answer about the host. DialContext reports it
// as an ordinary failure, and reading that as "down" put a red cross against
// machines nobody had dialled.
func TestACancelledDialIsNotAVerdict(t *testing.T) {
	addr, port := listening(t)
	h := store.Host{ID: "real", Addr: addr, Port: port}

	if got := probe.Check(context.Background(), h, 2*time.Second); got != probe.Up {
		t.Fatalf("the listener is unreachable even uncancelled: %v", got)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := probe.Check(ctx, h, 2*time.Second); got != probe.NotChecked {
		t.Errorf("a listening host dialled on a cancelled context reported %v, want not checked", got)
	}
}

// The hosts an abandoned sweep never reached say so, rather than joining the
// ones it genuinely found down.
func TestACancelledSweepDoesNotCallHostsDown(t *testing.T) {
	var hosts []store.Host
	for i := range 20 {
		hosts = append(hosts, store.Host{
			ID: fmt.Sprint(i), Addr: fmt.Sprintf("203.0.113.%d", i+1), Port: 22,
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	out := probe.CheckAll(ctx, hosts, 4, 5*time.Second, nil)
	for _, h := range hosts {
		if got := out[h.StatKey()]; got != probe.NotChecked {
			t.Fatalf("%s reported %v, want not checked", h.Addr, got)
		}
	}
}

func TestWorkersScaleWithTheSweep(t *testing.T) {
	for _, c := range []struct{ hosts, want int }{
		{1, 8}, {8, 8}, {40, 10}, {200, 32}, {1000, 32},
	} {
		if got := probe.Workers(c.hosts); got != c.want {
			t.Errorf("Workers(%d) = %d, want %d", c.hosts, got, c.want)
		}
	}
}

// The default concurrency has to actually reach the sweep: eight workers took
// twenty-five rounds over two hundred hosts where thirty-two take seven.
func TestCheckAllDefaultsToScaledConcurrency(t *testing.T) {
	var hosts []store.Host
	for i := range 200 {
		hosts = append(hosts, store.Host{
			ID: fmt.Sprint(i), Addr: fmt.Sprintf("203.0.113.%d", i%250+1), Port: 22,
		})
	}
	start := time.Now()
	probe.CheckAll(context.Background(), hosts, 0, 100*time.Millisecond, nil)

	if elapsed := time.Since(start); elapsed > 1500*time.Millisecond {
		t.Errorf("200 hosts took %v — the sweep is not scaling its concurrency", elapsed)
	}
}
