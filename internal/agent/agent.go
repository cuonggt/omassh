// Package agent drives ssh-agent through the ssh-add command.
package agent

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/cuonggt/omassh/internal/keys"
)

// ErrNoAgent reports that no agent is reachable.
var ErrNoAgent = errors.New("no ssh-agent (SSH_AUTH_SOCK is unset)")

// Available reports whether an agent socket is configured.
func Available() bool { return os.Getenv("SSH_AUTH_SOCK") != "" }

// List returns the keys currently loaded in the agent.
//
// ssh-add exits 1 when the agent simply holds nothing, which is an ordinary
// state rather than a failure; only exit 2 means the agent is unreachable.
func List() ([]keys.Info, error) {
	if !Available() {
		return nil, ErrNoAgent
	}
	out, err := exec.Command("ssh-add", "-l").Output()
	text := strings.TrimSpace(string(out))

	var ee *exec.ExitError
	if errors.As(err, &ee) {
		switch ee.ExitCode() {
		case 1:
			return nil, nil // "The agent has no identities."
		default:
			return nil, ErrNoAgent
		}
	}
	if err != nil {
		return nil, err
	}

	var loaded []keys.Info
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); line == "" {
			continue
		}
		if info, err := keys.ParseKeyLine(line); err == nil {
			loaded = append(loaded, info)
		}
	}
	return loaded, nil
}

// Add loads a key into the agent. env should come from secrets.WithAskpass so
// that an encrypted key can be unlocked without a terminal prompt; pass nil for
// keys with no passphrase. A non-zero lifetime expires the key automatically.
func Add(env []string, path string, lifetime time.Duration) error {
	if !Available() {
		return ErrNoAgent
	}
	args := []string{}
	if lifetime > 0 {
		args = append(args, "-t", fmt.Sprintf("%d", int(lifetime.Seconds())))
	}
	args = append(args, path)

	cmd := exec.Command("ssh-add", args...)
	if env != nil {
		cmd.Env = env
	}
	// ssh-add must not fall back to reading a passphrase from our terminal:
	// the TUI owns it, and a hidden prompt would deadlock the UI.
	cmd.Stdin = nil
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("ssh-add: %s", msg)
	}
	return nil
}

// Remove unloads a key from the agent.
func Remove(path string) error {
	if !Available() {
		return ErrNoAgent
	}
	out, err := exec.Command("ssh-add", "-d", path).CombinedOutput()
	if err != nil {
		return fmt.Errorf("ssh-add -d: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// Loaded reports whether a fingerprint is currently in the agent.
func Loaded(fingerprint string) bool {
	loaded, err := List()
	if err != nil {
		return false
	}
	for _, k := range loaded {
		if k.Fingerprint == fingerprint {
			return true
		}
	}
	return false
}
