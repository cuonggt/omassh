package sshx

import (
	"strconv"
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
// ok is false for a host whose hop omassh cannot spell the connection to.
// Reaching a hop means knowing its address, its port, its key and its user,
// and a jump host that is one of your own hosts is where those are written
// down; a hop named as free text is a destination for ssh to resolve, and a
// route written in ssh's own config is one only ssh can follow. Those stay
// unprobed rather than guessed at.
//
// The hop is returned as well, because connecting to it may want omassh's
// askpass helper in the environment, exactly as any other unattended
// connection to it would.
func ProbeThrough(h store.Host, timeout time.Duration) (args []string, hop store.Host, ok bool) {
	if h.Jump == nil {
		return nil, store.Host{}, false
	}
	hop = *h.Jump
	// ConnectTimeout bounds the dial to the hop the way the probe's own
	// timeout bounds a direct one. ssh takes whole seconds, and a timeout
	// shorter than one gets a second rather than nothing: a zero there means
	// ssh's own default, which is the system's and can run to minutes.
	secs := max(int(timeout/time.Second), 1)
	fixed := append(Unattended(hop), "-o", "ConnectTimeout="+strconv.Itoa(secs))
	return BuildWith(fixed, hop, "-W", forwardTarget(h)), hop, true
}
