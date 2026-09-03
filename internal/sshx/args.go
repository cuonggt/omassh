// Package sshx builds and runs OpenSSH invocations on omassh's behalf.
package sshx

import (
	"strconv"

	"github.com/cuonggt/omassh/internal/store"
)

// Build returns the argv (excluding the program name) used to reach h.
//
// Every connection path in omassh — interactive sessions, port forwards,
// reachability probes and the native SFTP dialer — funnels through this one
// function, so connection behaviour cannot drift between them.
//
// extra is inserted before the target, which is where ssh wants flags like
// -N, -L and -W.
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

	// Hosts read from ~/.ssh/config are addressed by their alias alone. The
	// user's own directives — Match blocks, canonicalisation, IdentityAgent,
	// certificates — already describe how to reach them, and re-specifying a
	// subset here would silently override the rest.
	if h.Source == store.SourceSSHConfig {
		args = append(args, extra...)
		return append(args, h.Name)
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

// ForwardArgs builds the argv for a port-forwarding child process.
//
// -N runs no remote command, and ExitOnForwardFailure makes ssh exit rather
// than sitting there connected with the forward silently not established —
// without it a failed bind looks identical to a working tunnel.
func ForwardArgs(h store.Host, f store.Forward, opts ...string) []string {
	extra := append([]string{"-N", "-o", "ExitOnForwardFailure=yes"}, opts...)
	return Build(h, append(extra, f.Kind.Flag(), f.Spec())...)
}

// SubsystemArgs builds the argv for invoking a remote subsystem, such as
// "sftp". ssh places the subsystem name where a remote command would go.
func SubsystemArgs(h store.Host, subsystem string, opts ...string) []string {
	extra := append([]string{"-s"}, opts...)
	return append(Build(h, extra...), subsystem)
}

// ExecArgs builds the argv for running a command on a host and capturing its
// output. The command is passed as a single argument so the remote shell sees
// it exactly as written, quoting intact.
//
// BatchMode is forced on: a snippet run has no terminal to prompt at, and
// failing immediately beats hanging on a prompt nobody can answer. LogLevel
// is raised to ERROR because ssh writes its own chatter to the same stderr the
// remote command uses — without it, a banner or a "Permanently added" notice
// would be captured as if the command had produced it. Real failures are
// logged at ERROR or above and still come through.
func ExecArgs(h store.Host, command string, opts ...string) []string {
	extra := append([]string{"-o", "BatchMode=yes", "-o", "LogLevel=ERROR", "-T"}, opts...)
	return append(Build(h, extra...), command)
}
