// Package probe checks whether hosts are reachable.
package probe

import (
	"context"
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
)

func (s State) String() string {
	switch s {
	case Up:
		return "up"
	case Down:
		return "down"
	case Skipped:
		return "not probed"
	default:
		return "unknown"
	}
}

// Check opens a TCP connection to a host's ssh port.
//
// Hosts reached through a jump host or a ProxyCommand are skipped rather than
// guessed at: their address is meaningful only from the far side of the proxy,
// so dialling it from here would report on the wrong machine — quite possibly
// something else entirely on the local network.
func Check(ctx context.Context, h store.Host, timeout time.Duration) State {
	if h.ProxyJump != "" || h.Note != "" {
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
		return Down
	}
	conn.Close()
	return Up
}

// CheckAll probes hosts concurrently, reporting each as it finishes.
func CheckAll(ctx context.Context, hosts []store.Host, limit int, timeout time.Duration, report func(key string, s State)) map[string]State {
	if limit < 1 {
		limit = 8
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
