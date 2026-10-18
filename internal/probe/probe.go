// Package probe checks whether hosts are reachable.
package probe

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cuonggt/omassh/internal/sshx"
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

// Check reports whether a host's ssh port is answering.
//
// A host omassh dials itself gets a TCP connection, opened and closed. A host
// reached through a jump host cannot be dialled from here at all: its address
// is meaningful only from the far side of the hop, so dialling it would report
// on the wrong machine — quite possibly something else entirely on the local
// network. That is as true of a ProxyJump or a ProxyCommand in ssh's own
// config as of a jump host omassh passes itself, and such a host used to be
// passed over for it. It is asked of the hop instead.
func Check(ctx context.Context, h store.Host, timeout time.Duration) State {
	// Before the route is considered, because neither way of asking has
	// anything to ask about without an address.
	if h.Addr == "" {
		return Unknown
	}
	if h.ProxyJump != "" || h.ConfigProxy != "" {
		return checkThroughJump(ctx, h, timeout)
	}
	d := net.Dialer{Timeout: timeout}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(h.Addr, strconv.Itoa(portOr22(h.Port))))
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

// checkThroughJump asks a host's jump host whether the host's port is
// answering, because from here the host's address says nothing.
//
// Up is the far side having spoken. ssh carries the hop's connection to this
// process, and sshd greets whoever opens one before it is spoken to, so a byte
// arriving is the port answering — for an ssh port that is the same answer a
// dial gives. A port answering with something that waits to be addressed first
// is the one case this cannot tell from silence.
//
// Down is the hop saying it could not connect, and it is the one thing ssh's
// own words have to be read for: a forward the hop refuses and a hop that
// would not carry one at all both end in status 255, and only stderr says
// which. Asked for a port that was not listening, ssh 10.3 said "channel 0:
// open failed: connect failed: ... connection refused" and then "stdio
// forwarding failed"; refused by the hop itself it said "Permission denied
// (publickey)". Either of the first two is taken, since a wording that drifts
// is likelier to take one than both.
//
// Anything else leaves the host unprobed rather than called down — the hop
// refusing omassh's key, and the deadline too, which is what a host the hop
// can reach but whose packets are dropped will hit. That is the safe way round
// for this to break, and it is why it reads for the failure rather than for
// the success: should those words ever change, a refused host reads as one
// nobody could ask about instead of a host wrongly called down. Twice now this
// package has put a red cross against machines nobody had managed to ask
// about, and neither bug would have been possible if a missing answer had
// never been read as a bad one.
func checkThroughJump(ctx context.Context, h store.Host, timeout time.Duration) State {
	args, hop, ok := sshx.ProbeThrough(h, timeout)
	if !ok {
		return Skipped
	}

	// The attempt as a whole is given more than a dial's worth of time: the
	// hop has to be dialled, exchange keys and authenticate before it can so
	// much as try, and the timeout was chosen for a dial. Each of those is
	// bounded in its own right — ConnectTimeout the dial, BatchMode every
	// prompt — so this is the backstop and not the budget.
	sweep := ctx
	ctx, cancel := context.WithTimeout(ctx, 3*timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ssh", args...)
	// A password credential answers through omassh itself, as it does for a
	// tunnel or an sftp session. Env is empty for every other kind.
	if env := sshx.Env(hop); len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	// ssh forwards this process's stdin to the far side, so the child needs
	// one that stays open and says nothing: end of file on it closes the
	// forward, which can race the greeting this is waiting for. An os.Pipe is
	// handed over as it is, with no copying goroutine to end — and nothing is
	// ever written to it.
	quiet, hold, err := os.Pipe()
	if err != nil {
		return Skipped
	}
	defer quiet.Close()
	defer hold.Close()
	cmd.Stdin = quiet

	spoke := make(chan struct{}, 1)
	// Read only once the child is reaped: until Wait returns, ssh's output is
	// still being copied in from another goroutine.
	var said bytes.Buffer
	cmd.Stdout, cmd.Stderr = greeter{spoke}, &said

	if err := cmd.Start(); err != nil {
		if sweep.Err() != nil {
			return NotChecked
		}
		return Skipped
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case <-spoke:
		// The port is answering; there is nothing else to wait for, and the
		// child is reaped rather than left to the deadline.
		cancel()
		<-done
		return Up
	case <-done:
	}
	switch {
	case sweep.Err() != nil:
		// The sweep was abandoned, which is not an answer about the host.
		return NotChecked
	case strings.Contains(said.String(), "open failed"),
		strings.Contains(said.String(), "stdio forwarding failed"):
		return Down
	}
	return Skipped
}

// greeter reports the first thing the far side says and keeps none of it.
//
// It always claims the whole write: telling ssh it wrote short would break the
// pipe, and what was said after the first byte is of no interest anyway.
type greeter struct{ spoke chan<- struct{} }

func (g greeter) Write(b []byte) (int, error) {
	if len(b) > 0 {
		select {
		case g.spoke <- struct{}{}:
		default:
		}
	}
	return len(b), nil
}

// JumpWorkers is how many of a sweep's probes one jump host carries at once.
//
// A fleet behind one bastion makes a sweep a burst of connections to that
// bastion, and sshd's MaxStartups starts dropping them past the tenth that has
// yet to authenticate. A dropped connection reads here as a hop that would not
// carry the probe, which would leave "not probed" against hosts that are
// perfectly fine — the bastion's own load-shedding reported as ignorance about
// somebody else. Four is what a snippet run across a group allows itself, for
// the same reason: nothing good comes of arriving thirty at once.
const JumpWorkers = 4

// jumpKey names the machine a host's probe has to go through, so that a sweep
// can hold down how many connections it makes to any one of them. Empty for a
// host that is dialled from here.
//
// The address and port rather than the host's id, because the cap is about the
// sshd on the far side: two of your hosts naming the same bastion are one
// machine to it, however they are written down.
func jumpKey(h store.Host) string {
	if h.Jump == nil {
		return ""
	}
	return net.JoinHostPort(h.Jump.Addr, strconv.Itoa(portOr22(h.Jump.Port)))
}

func portOr22(p int) int {
	if p == 0 {
		return 22
	}
	return p
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
//
// Hosts behind a jump host are additionally limited per hop, since those are
// not dials but connections to somebody's bastion.
func CheckAll(ctx context.Context, hosts []store.Host, limit int, timeout time.Duration, report func(key string, s State)) map[string]State {
	if limit < 1 {
		limit = Workers(len(hosts))
	}
	out := make(map[string]State, len(hosts))
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, limit)

	// Built up front, so the workers only ever read it.
	hops := map[string]chan struct{}{}
	for _, h := range hosts {
		if k := jumpKey(h); k != "" && hops[k] == nil {
			hops[k] = make(chan struct{}, JumpWorkers)
		}
	}

	for _, h := range hosts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// The hop's slot is taken before the sweep's, so a host queueing
			// for its jump host is not sitting on a worker that another host
			// could have been dialled with.
			if hop := hops[jumpKey(h)]; hop != nil {
				hop <- struct{}{}
				defer func() { <-hop }()
			}
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
