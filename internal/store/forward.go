package store

import (
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
)

// ForwardKind is which of ssh's three tunnels a rule is.
type ForwardKind string

const (
	// ForwardLocal binds a port on this machine and carries what arrives on
	// it out of the far side: ssh -L.
	ForwardLocal ForwardKind = "local"
	// ForwardRemote binds a port on the host and carries it back here: -R.
	ForwardRemote ForwardKind = "remote"
	// ForwardDynamic binds a SOCKS proxy here, and each connection through it
	// names its own destination: -D.
	ForwardDynamic ForwardKind = "dynamic"
)

// ForwardKinds is every kind, in the order a picker offers them.
var ForwardKinds = []ForwardKind{ForwardLocal, ForwardRemote, ForwardDynamic}

// Forward is a port-forwarding rule belonging to one host.
//
// The pieces are stored apart rather than as the string ssh takes, so an
// address that contains colons can be bracketed when the spec is built and
// read plainly everywhere else.
type Forward struct {
	ID     string      `json:"id"`
	HostID string      `json:"host_id"`
	Kind   ForwardKind `json:"kind"`

	// Listen is the address the tunnel binds and the port on it. Empty binds
	// wherever ssh's own default puts it — loopback, unless GatewayPorts says
	// otherwise. For a remote forward this side is on the host.
	Listen     string `json:"listen,omitempty"`
	ListenPort int    `json:"listen_port"`

	// Dest is what the far end opens a connection to. A dynamic forward has
	// none: every connection through it chooses for itself.
	Dest     string `json:"dest,omitempty"`
	DestPort int    `json:"dest_port,omitempty"`
}

// Flag is the ssh option carrying this kind of tunnel.
func (f Forward) Flag() string {
	switch f.Kind {
	case ForwardRemote:
		return "-R"
	case ForwardDynamic:
		return "-D"
	default:
		return "-L"
	}
}

// Spec is the argument to that flag, in ssh's own syntax.
func (f Forward) Spec() string {
	listen := hostPort(f.Listen, f.ListenPort)
	if f.Kind == ForwardDynamic {
		return listen
	}
	return listen + ":" + hostPort(f.Dest, f.DestPort)
}

// Route is the two ends of a rule, with the arrow running from the port you
// connect to towards whatever answers.
//
// It does not say which machine each end is on — that is the kind's job — so
// anywhere a rule is named on its own, Label is the one to use.
func (f Forward) Route() string {
	if f.Kind == ForwardDynamic {
		return hostPort(f.Listen, f.ListenPort) + " → socks"
	}
	return hostPort(f.Listen, f.ListenPort) + " → " + hostPort(f.Dest, f.DestPort)
}

// Label is the whole rule in words, kind included.
//
// The kind is not decoration: local and remote bind the same port number on
// opposite machines, so two rules that differ only in it are two different
// tunnels that read as one. Naming a rule without it produced a host showing
// the same line twice, and a status message that could have meant either.
func (f Forward) Label() string { return string(f.Kind) + " " + f.Route() }

// ListenText and DestText are the two halves of a rule as the form writes
// them, and reads them back: the same syntax ssh uses, so what is on screen is
// what would be typed on a command line.
func (f Forward) ListenText() string { return hostPort(f.Listen, f.ListenPort) }
func (f Forward) DestText() string   { return hostPort(f.Dest, f.DestPort) }

// Validate reports what is wrong with a rule, so a form can say so rather than
// handing ssh something it will reject in a detached session nobody is
// watching.
func (f Forward) Validate() error {
	switch f.Kind {
	case ForwardLocal, ForwardRemote, ForwardDynamic:
	default:
		return fmt.Errorf("%q is not a kind of forward (local, remote or dynamic)", f.Kind)
	}
	if err := checkPort(f.ListenPort, "listen"); err != nil {
		return err
	}
	if f.Kind == ForwardDynamic {
		return nil
	}
	if f.Dest == "" {
		return fmt.Errorf("a %s forward needs somewhere to come out", f.Kind)
	}
	return checkPort(f.DestPort, "destination")
}

// hostPort joins an address and a port for a forwarding spec, bracketing an
// IPv6 literal so the colons in it are not read as separators.
func hostPort(addr string, port int) string {
	p := strconv.Itoa(port)
	switch {
	case addr == "":
		return p
	case strings.Contains(addr, ":"):
		return "[" + addr + "]:" + p
	default:
		return addr + ":" + p
	}
}

// ParseListen reads the bound side of a rule as it is written in the form: a
// bare port, or an address and a port.
func ParseListen(s string) (addr string, port int, err error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", 0, fmt.Errorf("a forward needs a port to listen on")
	}
	if n, err := strconv.Atoi(s); err == nil {
		return "", n, checkPort(n, "listen")
	}
	return splitAddr(s, "listen")
}

// ParseDest reads the far side, where both halves are required: a tunnel has
// no sensible default for where it comes out.
func ParseDest(s string) (addr string, port int, err error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", 0, fmt.Errorf("a forward needs a destination, as host:port")
	}
	return splitAddr(s, "destination")
}

func splitAddr(s, what string) (string, int, error) {
	host, p, err := net.SplitHostPort(s)
	if err != nil {
		return "", 0, fmt.Errorf("%s %q is not an address and port", what, s)
	}
	n, err := strconv.Atoi(p)
	if err != nil {
		return "", 0, fmt.Errorf("%s port %q is not a number", what, p)
	}
	return host, n, checkPort(n, what)
}

func checkPort(n int, what string) error {
	if n < 1 || n > 65535 {
		return fmt.Errorf("%s port must be between 1 and 65535", what)
	}
	return nil
}

// SortForwards orders rules by the port they bind, which is how they are
// remembered — "the tunnel on 5432" — rather than by when they were added.
//
// The order is total, down to the id. sort.Slice is not stable, so rules alike
// in every field it compared could swap places from one read to the next — and
// a local and a remote rule over the same two ports are exactly that pair. The
// highlighted rule is an index into this list, so a list that reorders itself
// moves the selection with nobody having pressed anything.
func SortForwards(fs []Forward) {
	sort.Slice(fs, func(i, j int) bool {
		a, b := fs[i], fs[j]
		if a.ListenPort != b.ListenPort {
			return a.ListenPort < b.ListenPort
		}
		if a.Listen != b.Listen {
			return a.Listen < b.Listen
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.ID < b.ID
	})
}
