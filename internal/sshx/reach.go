package sshx

import (
	"strconv"
	"strings"
	"time"

	"github.com/cuonggt/omassh/internal/store"
)

// ProbeThrough is the ssh invocation that asks a host's jump host whether the
// host's port is answering: a stdio forward to it, and nothing else.
//
// A host behind a jump host cannot be dialled from here, because its address
// means something only from the far side of the hop — which is why the probe
// passed over such a host entirely rather than report on whatever answered at
// that address locally. This puts the same question a direct probe asks to the
// one machine that can answer it: open a TCP connection to this address and
// port. -W is how ssh's own implicit ProxyCommand says it, and how omassh
// already spells the inner connection of a jump host it is given. Nothing
// authenticates to the host itself, nothing is written to known_hosts, and the
// far side is never sent a byte.
//
// ok is false for a host with no hop to ask; for the one route omassh cannot
// ask through, a ProxyCommand in ssh's own config, which is a program only ssh
// knows how to run, to a host whose address may be an instance id rather than
// an address at all; and for a hop written in a way that is no destination.
// Those stay unprobed rather than guessed at.
//
// The hop is returned as well, because connecting to it may want omassh's
// askpass helper in the environment, exactly as any other unattended
// connection to it would.
func ProbeThrough(h store.Host, timeout time.Duration) (args []string, hop store.Host, ok bool) {
	hop, ok = Hop(h)
	if !ok {
		return nil, store.Host{}, false
	}
	// ConnectTimeout bounds the dial to the hop the way the probe's own
	// timeout bounds a direct one. ssh takes whole seconds, and a timeout
	// shorter than one gets a second rather than nothing: a zero there means
	// ssh's own default, which is the system's and can run to minutes.
	secs := max(int(timeout/time.Second), 1)
	fixed := append(Unattended(hop), "-o", "ConnectTimeout="+strconv.Itoa(secs))
	return BuildWith(fixed, hop, "-W", forwardTarget(h)), hop, true
}

// Hop is the machine a probe of h has to go through, and whether omassh can
// spell the connection to it. Nothing for a host it dials itself.
//
// The three ways a host has a hop, in the order ssh resolves them. A jump host
// that is one of your own hosts arrives already resolved, with its port, its
// key and its user. One named as free text — `ops@edge.example.com` — is a
// destination, and is read back into a host so that the connection to it can
// be spelled: BuildWith renders it exactly as ssh renders the inner command of
// its own -J. A route from ssh's own config is the same once the file has said
// it was a ProxyJump rather than a ProxyCommand.
//
// What it is used for decides the order, and it is the same order Build uses:
// a jump host of omassh's own goes on ssh's command line ahead of the file,
// and ssh keeps the first route it is given.
//
// Only the one hop is read, never what stands behind it. The hop goes on ssh's
// command line as a destination, so everything the file says about the hop
// itself — a ProxyJump of its own, a Match, an Include — ssh reads as it
// always does. Following the file that far here would be omassh working out a
// route ssh is about to work out again, which is the drift this codebase keeps
// out of by not reading the file where ssh will read it.
func Hop(h store.Host) (store.Host, bool) {
	switch {
	case h.Jump != nil:
		return *h.Jump, true
	case h.ProxyJump != "":
		return hopFromDestination(h.ProxyJump)
	case h.ConfigProxy.Jump:
		return hopFromDestination(h.ConfigProxy.Via)
	}
	return store.Host{}, false
}

// hopFromDestination reads a hop written as a destination, chain and all.
//
// A ProxyJump may name hop after hop, comma separated. The last of them is the
// one to ask, because it is the one that can reach the host; the ones before
// it are how to reach that one, which is what -J is for and what BuildWith
// spells from a hop's own ProxyJump. So a chain is handed back as a host with
// the rest of the chain still in front of it, and ssh nests the lot.
func hopFromDestination(dest string) (store.Host, bool) {
	chain := strings.Split(dest, ",")
	hop, ok := store.ParseJumpTarget(chain[len(chain)-1])
	if !ok {
		return store.Host{}, false
	}
	if rest := chain[:len(chain)-1]; len(rest) > 0 {
		hop.ProxyJump = strings.Join(rest, ",")
	}
	return hop, true
}
