//go:build unix

package term

import (
	"os/exec"
	"syscall"
)

// attachTTY makes the child a session leader whose controlling terminal is the
// pty slave.
//
// xpty.Start only wires stdin/stdout/stderr to the slave. Without a
// controlling terminal the kernel delivers no SIGWINCH on a window-size
// change, so ssh never learns the pane resized and the remote keeps drawing to
// the geometry it was given at connect time. Ctty is 0 because the slave is
// the child's stdin.
func attachTTY(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid:  true,
		Setctty: true,
		Ctty:    0,
	}
}
