//go:build !unix

package forward

import (
	"os/exec"
	"strings"
)

func inUse(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "in use")
}

func isolate(cmd *exec.Cmd) {}

func terminate(pid int) error { return nil }
func kill(pid int) error      { return nil }
