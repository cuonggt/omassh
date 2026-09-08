// Package sshx builds and runs OpenSSH invocations on omassh's behalf.
package sshx

import (
	"strconv"

	"github.com/cuonggt/omassh/internal/store"
)

// Build returns the argv (excluding the program name) used to reach h.
//
// Every connection path in omassh — interactive sessions, reachability probes
// and the native SFTP dialer — funnels through this one function, so
// connection behaviour cannot drift between them.
//
// extra is inserted before the target, which is where ssh wants flags like
// -N and -W.
// globalOptions are -o settings applied to every ssh invocation, mirroring
// ssh's own flag. Process-wide configuration set once at startup, before any
// connection is made, so there is nothing to synchronise.
var globalOptions []string

// SetGlobalOptions installs -o settings for every connection Omassh makes.
func SetGlobalOptions(opts []string) { globalOptions = append([]string(nil), opts...) }

func Build(h store.Host, extra ...string) []string {
	args := make([]string, 0, 10+2*len(globalOptions)+len(extra))
	for _, o := range globalOptions {
		args = append(args, "-o", o)
	}

	if h.Port != 0 && h.Port != 22 {
		args = append(args, "-p", strconv.Itoa(h.Port))
	}
	if h.Identity != "" {
		args = append(args, "-i", h.Identity)
	}
	if h.ProxyJump != "" {
		args = append(args, "-J", h.ProxyJump)
	}

	args = append(args, extra...)
	return append(args, h.Target())
}

// SubsystemArgs builds the argv for invoking a remote subsystem, such as
// "sftp". ssh places the subsystem name where a remote command would go.
func SubsystemArgs(h store.Host, subsystem string, opts ...string) []string {
	extra := append([]string{"-s"}, opts...)
	return append(Build(h, extra...), subsystem)
}
