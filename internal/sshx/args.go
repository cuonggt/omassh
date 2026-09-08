// Package sshx builds and runs OpenSSH invocations on omassh's behalf.
package sshx

import (
	"strconv"
	"strings"

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
	switch {
	case h.Jump != nil:
		// ssh -J hands the hop only -l, -p and -v, so the jump host's own key
		// and options would be dropped. Spelling the inner connection out is
		// what ssh does internally anyway, and it composes: a hop behind
		// another hop carries its own ProxyCommand.
		args = append(args, "-o", "ProxyCommand="+proxyCommand(*h.Jump))
	case h.ProxyJump != "":
		// Not one of our hosts, so it is already an ssh destination.
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

// proxyCommand is the command ssh runs to reach a host through a jump host.
//
// -W makes the hop forward stdio to the next address, which is exactly what
// ssh's own implicit ProxyCommand does; %h and %p are placeholders ssh fills
// in with the address it is trying to reach.
func proxyCommand(jump store.Host) string {
	inner := append([]string{"ssh"}, Build(jump, "-W", "[%h]:%p")...)
	quoted := make([]string, len(inner))
	for i, a := range inner {
		quoted[i] = shellQuote(a)
	}
	return strings.Join(quoted, " ")
}

// shellQuote makes one argument safe for the shell that runs a ProxyCommand.
//
// ssh runs it through the user's login shell, not /bin/sh, so glob characters
// matter: an unquoted -W [%h]:%p is a bracket expression, and zsh treats a
// pattern that matches nothing as a fatal error rather than passing it
// through. ssh quotes that argument in its own implicit ProxyCommand for
// exactly this reason. Key paths and addresses are user-supplied too, so they
// can contain spaces and anything else a shell would act on.
//
// %h and %p survive quoting: ssh substitutes them into the string before the
// shell ever sees it.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	safe := true
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' ||
			strings.ContainsRune("@%_+=:,./-", r)) {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}
