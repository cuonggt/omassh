// Package probe checks whether hosts are reachable.
package probe

import (
	"context"
	"errors"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/cuonggt/omassh/internal/store"
)

// State is what the host list shows next to a host.
type State uint8

const (
	Unknown State = iota
	Up
	Down
	// Skipped marks a host that cannot meaningfully be dialled directly.
	Skipped
	// NotChecked marks a host the sweep never actually reached. It is the
	// absence of a verdict rather than a bad one: calling an undialled host
	// down is how a sweep that ran out of time used to report machines that
	// were perfectly fine.
	NotChecked
)

func (s State) String() string {
	switch s {
	case Up:
		return "up"
	case Down:
		return "down"
	case Skipped:
		return "not probed"
	case NotChecked:
		return "not checked"
	default:
		return "unknown"
	}
}

// Check opens a TCP connection to a host's ssh port.
//
// Hosts reached through a jump host are skipped rather than guessed at: their
// address is meaningful only from the far side of the proxy, so dialling it
// from here would report on the wrong machine — quite possibly something else
// entirely on the local network.
func Check(ctx context.Context, h store.Host, timeout time.Duration) State {
	if h.ProxyJump != "" {
		return Skipped
	}
	if h.Addr == "" {
		return Unknown
	}
	port := h.Port
	if port == 0 {
		port = 22
	}

	d := net.Dialer{Timeout: timeout}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(h.Addr, strconv.Itoa(port)))
	if err != nil {
		// Being cancelled is not an answer about the host. Dialling reports it
		// as an ordinary failure, and reading that as "down" would put a red
		// cross against a machine nobody finished dialling.
		//
		// Cancellation is the only interruption that can be told apart: a
		// context deadline and the dialer's own timeout both come back as
		// "i/o timeout" and both satisfy errors.Is(err, DeadlineExceeded).
		// That is why a sweep bounds each dial and never the run as a whole —
		// a ceiling on the run cannot be distinguished afterwards from a host
		// that is genuinely unreachable, so every host past it was called
		// down.
		if errors.Is(err, context.Canceled) {
			return NotChecked
		}
		return Down
	}
	conn.Close()
	return Up
}

// Workers is how many hosts CheckAll dials at once for a sweep of n.
//
// A fixed handful made a large group crawl: an unreachable host costs the full
// timeout and they queue behind each other, so two hundred of them took the
// better part of a minute. This scales with the sweep, while staying far
// inside the file-descriptor budget and low enough not to resemble a port scan.
func Workers(n int) int { return min(max(n/4, 8), 32) }

// CheckAll probes hosts concurrently, reporting each as it finishes. A limit
// below one means Workers picks one for the size of the sweep, which is what
// callers want unless they have a reason of their own.
//
// report is called from each worker, so it has to be safe to call from several
// goroutines at once. The interface sends the result down a channel; anything
// that writes to a map of its own needs a lock.
func CheckAll(ctx context.Context, hosts []store.Host, limit int, timeout time.Duration, report func(key string, s State)) map[string]State {
	if limit < 1 {
		limit = Workers(len(hosts))
	}
	out := make(map[string]State, len(hosts))
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, limit)

	for _, h := range hosts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			s := Check(ctx, h, timeout)
			mu.Lock()
			out[h.StatKey()] = s
			mu.Unlock()
			if report != nil {
				report(h.StatKey(), s)
			}
		}()
	}
	wg.Wait()
	return out
}
