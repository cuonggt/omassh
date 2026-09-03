//go:build unix

package forward

import (
	"errors"
	"os/exec"
	"syscall"
)

// inUse distinguishes "something already holds this port" from other bind
// failures, such as an address family that is not configured at all.
func inUse(err error) bool { return errors.Is(err, syscall.EADDRINUSE) }

// isolate puts the child in its own process group.
//
// A host reached through a ProxyCommand spawns a grandchild; killing only ssh
// would leave it running and holding the port. Signalling the whole group takes
// the entire tunnel down.
func isolate(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func signalGroup(pid int, sig syscall.Signal) error {
	return syscall.Kill(-pid, sig)
}

func terminate(pid int) error { return signalGroup(pid, syscall.SIGTERM) }
func kill(pid int) error      { return signalGroup(pid, syscall.SIGKILL) }
