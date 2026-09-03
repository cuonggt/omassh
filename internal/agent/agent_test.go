package agent_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/cuonggt/omassh/internal/agent"
	"github.com/cuonggt/omassh/internal/keys"
	"github.com/cuonggt/omassh/internal/secrets"
)

// TestMain doubles as the SSH_ASKPASS helper: when ssh-add executes this test
// binary to ask for a passphrase, it serves the secret and exits. That lets the
// tests exercise the real unlock path rather than a stand-in.
func TestMain(m *testing.M) {
	if secrets.ServeAskpass() {
		os.Exit(0)
	}
	os.Exit(m.Run())
}

var (
	sockRE = regexp.MustCompile(`SSH_AUTH_SOCK=([^;]+);`)
	pidRE  = regexp.MustCompile(`SSH_AGENT_PID=(\d+);`)
)

// startAgent runs a private ssh-agent for the test, so the developer's own
// agent is never modified.
func startAgent(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ssh-agent"); err != nil {
		t.Skip("no ssh-agent")
	}
	out, err := exec.Command("ssh-agent", "-s").Output()
	if err != nil {
		t.Skipf("ssh-agent would not start: %v", err)
	}
	sock := sockRE.FindStringSubmatch(string(out))
	pid := pidRE.FindStringSubmatch(string(out))
	if sock == nil || pid == nil {
		t.Skipf("unrecognised ssh-agent output: %q", out)
	}

	t.Setenv("SSH_AUTH_SOCK", sock[1])
	t.Cleanup(func() {
		kill := exec.Command("ssh-agent", "-k")
		kill.Env = append(os.Environ(), "SSH_AGENT_PID="+pid[1])
		kill.Run()
	})
}

func TestAgentLifecycle(t *testing.T) {
	startAgent(t)
	dir := t.TempDir()

	plain, err := keys.Generate(filepath.Join(dir, "id_plain"), "plain", "")
	if err != nil {
		t.Fatal(err)
	}

	t.Run("empty agent is not an error", func(t *testing.T) {
		loaded, err := agent.List()
		if err != nil {
			t.Fatalf("List on empty agent = %v, want nil", err)
		}
		if len(loaded) != 0 {
			t.Errorf("List = %v, want empty", loaded)
		}
	})

	t.Run("adds an unencrypted key", func(t *testing.T) {
		if err := agent.Add(nil, plain.Path, 0); err != nil {
			t.Fatalf("Add: %v", err)
		}
		if !agent.Loaded(plain.Fingerprint) {
			loaded, _ := agent.List()
			t.Errorf("key not in agent; agent holds %+v", loaded)
		}
	})

	t.Run("removes a key", func(t *testing.T) {
		if err := agent.Remove(plain.Path); err != nil {
			t.Fatalf("Remove: %v", err)
		}
		if agent.Loaded(plain.Fingerprint) {
			t.Error("key still in agent after Remove")
		}
	})
}

// The point of the vault: unlocking a passphrase-protected key with no TTY and
// no prompt, driving the real ssh-add.
func TestAddEncryptedKeyViaAskpass(t *testing.T) {
	startAgent(t)
	dir := t.TempDir()

	const passphrase = "correct horse battery staple"
	locked, err := keys.Generate(filepath.Join(dir, "id_locked"), "locked", passphrase)
	if err != nil {
		t.Fatal(err)
	}
	if !locked.Encrypted {
		t.Fatal("test key is not encrypted")
	}

	err = secrets.WithAskpass(passphrase, func(env []string) error {
		return agent.Add(env, locked.Path, 0)
	})
	if err != nil {
		t.Fatalf("unlock via askpass: %v", err)
	}
	if !agent.Loaded(locked.Fingerprint) {
		loaded, _ := agent.List()
		t.Errorf("encrypted key not loaded; agent holds %+v", loaded)
	}
}

func TestWrongPassphraseFails(t *testing.T) {
	startAgent(t)
	dir := t.TempDir()

	locked, err := keys.Generate(filepath.Join(dir, "id_locked"), "locked", "the real one")
	if err != nil {
		t.Fatal(err)
	}

	err = secrets.WithAskpass("not the passphrase", func(env []string) error {
		return agent.Add(env, locked.Path, 0)
	})
	if err == nil {
		t.Fatal("Add succeeded with the wrong passphrase")
	}
	if agent.Loaded(locked.Fingerprint) {
		t.Error("key was loaded despite a wrong passphrase")
	}
}

func TestNoAgent(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", "")

	if agent.Available() {
		t.Error("Available() = true with SSH_AUTH_SOCK unset")
	}
	if _, err := agent.List(); err != agent.ErrNoAgent {
		t.Errorf("List = %v, want ErrNoAgent", err)
	}
}
