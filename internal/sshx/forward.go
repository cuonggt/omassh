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
//   - ExitOnForwardFailure makes ssh give up when the port cannot be bound,
//     rather than holding a connection that carries nothing.
//   - BatchMode refuses to prompt. The tunnel runs where nobody is watching,
//     so a passphrase or host-key prompt would wait for an answer that is
//     never coming, and the interface would report a tunnel that is up when
//     what is up is a question. Failing says which key to add instead.
//   - The keepalives are what make "running" mean something: without them a
//     connection whose network went away holds its port open indefinitely and
//     the interface goes on showing it as up.
//
// These are appended after the global -o settings, so an option given on
// omassh's own command line still wins — ssh takes the first value it is
// given for a setting.
func ForwardArgs(h store.Host, f store.Forward) []string {
	return Build(h,
		"-N",
		"-o", "ExitOnForwardFailure=yes",
		"-o", "BatchMode=yes",
		"-o", "ServerAliveInterval=30",
		"-o", "ServerAliveCountMax=3",
		f.Flag(), f.Spec(),
	)
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
