package sshx

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"syscall"

	"github.com/cuonggt/omassh/internal/store"
)

// ForwardArgs builds the ssh invocation that carries one forwarding rule.
//
// It goes through Build like every other connection, so a tunnel reaches its
// host by the same route, key and port an interactive session would — jump
// chains included. Only the flags that make a connection a tunnel are added:
//
//   - -N asks for no remote command, since a forward has nothing to run.
//   - BatchMode refuses to prompt. The tunnel runs where nobody is watching,
//     so a passphrase or host-key prompt would wait for an answer that is
//     never coming. Failing says which key to add instead.
//   - ExitOnForwardFailure makes ssh give up when the port cannot be bound,
//     rather than holding a connection that carries nothing.
//   - The keepalives mean a connection whose network went away is noticed,
//     instead of holding its port open indefinitely.
//
// The first two lead, ahead of the global -o settings, and ssh takes the first
// value it is given for a setting — so they cannot be overridden. They are not
// preferences competing with the user's: they are what makes "running" mean
// "carrying". With BatchMode turned off from omassh's own command line, a
// tunnel to a host that refused the key sat at a password prompt in a detached
// pane, and the interface reported it as up — a tunnel binding nothing, with
// nobody there to answer.
//
// The keepalives are a preference, and follow the global settings so that
// someone who has tuned their own still gets them.
func ForwardArgs(h store.Host, f store.Forward) []string {
	fixed := []string{
		"-o", "BatchMode=yes",
		"-o", "ExitOnForwardFailure=yes",
	}
	return append(fixed, Build(h,
		"-N",
		"-o", "ServerAliveInterval=30",
		"-o", "ServerAliveCountMax=3",
		f.Flag(), f.Spec(),
	)...)
}

// ListenAvailable reports whether the near end of a rule can be bound.
//
// ssh binds the port inside a session nobody is looking at, so a port already
// in use came back as a tunnel that vanished a moment after starting. Binding
// it here first turns that into a sentence naming the port. Something else
// can still take it in between, which is what ExitOnForwardFailure is for.
func ListenAvailable(f store.Forward) error {
	if f.Kind == store.ForwardRemote {
		// The port is bound on the host, which is the one place this side
		// cannot look. ssh reports that one itself.
		return nil
	}
	addr := f.Listen
	switch addr {
	case "*":
		// ssh spells "every interface" with a star; Go spells it with nothing.
		addr = ""
	case "":
		// ssh binds loopback unless GatewayPorts says otherwise, so that is
		// what has to be free.
		addr = "localhost"
	}

	l, err := net.Listen("tcp", net.JoinHostPort(addr, strconv.Itoa(f.ListenPort)))
	if err != nil {
		if errors.Is(err, syscall.EADDRINUSE) {
			return fmt.Errorf("port %d is already in use", f.ListenPort)
		}
		return err
	}
	return l.Close()
}
