//go:build !unix

package term

import "os/exec"

func attachTTY(cmd *exec.Cmd) {}
