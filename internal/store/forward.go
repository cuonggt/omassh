package store

import (
	"fmt"
	"strings"
)

// ForwardKind selects which ssh forwarding flag a rule uses.
type ForwardKind uint8

const (
	ForwardLocal   ForwardKind = iota // -L
	ForwardRemote                     // -R
	ForwardDynamic                    // -D, a local SOCKS proxy
)

func (k ForwardKind) Flag() string {
	switch k {
	case ForwardRemote:
		return "-R"
	case ForwardDynamic:
		return "-D"
	default:
		return "-L"
	}
}

func (k ForwardKind) String() string {
	switch k {
	case ForwardRemote:
		return "remote"
	case ForwardDynamic:
		return "dynamic"
	default:
		return "local"
	}
}

// ParseForwardKind reads a kind from a form field.
func ParseForwardKind(s string) (ForwardKind, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "local", "l", "-l":
		return ForwardLocal, nil
	case "remote", "r", "-r":
		return ForwardRemote, nil
	case "dynamic", "socks", "d", "-d":
		return ForwardDynamic, nil
	}
	return 0, fmt.Errorf("kind must be local, remote or dynamic")
}

// Forward is one port-forwarding rule attached to a host.
//
// The field names follow ssh's own grammar — [bind:]port:host:hostport — so the
// same shape describes all three kinds without any per-kind reinterpretation:
//
//	-L  listen locally on ListenPort, connect to Target from the server
//	-R  listen on the server on ListenPort, connect to Target from here
//	-D  listen locally on ListenPort as a SOCKS proxy; no target
type Forward struct {
	ID      string `json:"id"`
	HostKey string `json:"host_key"` // Host.StatKey, so config hosts work too
	Name    string `json:"name,omitempty"`

	Kind       ForwardKind `json:"kind"`
	BindAddr   string      `json:"bind_addr,omitempty"`
	ListenPort int         `json:"listen_port"`
	TargetHost string      `json:"target_host,omitempty"`
	TargetPort int         `json:"target_port,omitempty"`
}

// Spec renders the argument to -L, -R or -D.
func (f Forward) Spec() string {
	prefix := ""
	if f.BindAddr != "" {
		prefix = bracketed(f.BindAddr) + ":"
	}
	if f.Kind == ForwardDynamic {
		return fmt.Sprintf("%s%d", prefix, f.ListenPort)
	}
	return fmt.Sprintf("%s%d:%s:%d", prefix, f.ListenPort, bracketed(f.TargetHost), f.TargetPort)
}

// BindsLocally reports whether starting this rule occupies a local port, which
// is what makes a pre-flight check possible.
func (f Forward) BindsLocally() bool { return f.Kind != ForwardRemote }

// Label is a short human description used in lists.
func (f Forward) Label() string {
	if f.Name != "" {
		return f.Name
	}
	if f.Kind == ForwardDynamic {
		return fmt.Sprintf("socks :%d", f.ListenPort)
	}
	return fmt.Sprintf(":%d → %s:%d", f.ListenPort, f.TargetHost, f.TargetPort)
}

// bracketed wraps bare IPv6 literals, which ssh's colon-separated forward
// grammar cannot otherwise parse.
func bracketed(host string) string {
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		return "[" + host + "]"
	}
	return host
}

// Validate reports why a rule cannot be used, if it cannot.
func (f Forward) Validate() error {
	if f.ListenPort < 1 || f.ListenPort > 65535 {
		return fmt.Errorf("listen port must be between 1 and 65535")
	}
	if f.Kind == ForwardDynamic {
		return nil
	}
	if f.TargetHost == "" {
		return fmt.Errorf("a %s forward needs a target host", f.Kind)
	}
	if f.TargetPort < 1 || f.TargetPort > 65535 {
		return fmt.Errorf("target port must be between 1 and 65535")
	}
	return nil
}
