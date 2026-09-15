package term

import (
	"strings"
	"testing"

	"github.com/cuonggt/omassh/internal/sshx"
	"github.com/cuonggt/omassh/internal/store"
)

// passwordHost logs in with a stored password, the only kind of credential
// that needs anything in the environment at all.
func passwordHost() store.Host {
	return store.Host{
		Name: "vault",
		Addr: "10.0.4.4",
		Cred: &store.Credential{
			ID:   "cred-1",
			Name: "root on the vault",
			Kind: store.CredentialPassword,
			User: "root",
		},
	}
}

// effective is the value the child actually sees. A later assignment wins, so
// TERM can be overridden by appending rather than by filtering what we
// inherited — and a test that reads the first match would pass on the runner's
// own TERM without noticing ours was never added.
func effective(env []string, key string) (string, bool) {
	val, ok := "", false
	for _, e := range env {
		if name, v, found := strings.Cut(e, "="); found && name == key {
			val, ok = v, true
		}
	}
	return val, ok
}

// Without tmux the pane runs ssh itself, and what a password credential needs
// has nowhere to go but this process's environment.
//
// It went there and was then thrown away: TERM was assigned over the whole of
// cmd.Env a few lines further down, so ssh started with no SSH_ASKPASS and
// asked in the pane for a password Omassh was already holding. Nothing failed
// and nothing was logged. The tmux path builds its environment into the
// command instead and was unaffected, which is why every machine the suite
// runs on saw nothing wrong.
func TestAPaneWithoutTmuxKeepsWhatAPasswordCredentialNeeds(t *testing.T) {
	cmd, session := sessionCommand(passwordHost(), false)

	if session != "" {
		t.Errorf("a plain ssh child has no tmux session, got %q", session)
	}
	if v, ok := effective(cmd.Env, "SSH_ASKPASS"); !ok || v == "" {
		t.Error("SSH_ASKPASS is not in the environment ssh was given")
	}
	if v, _ := effective(cmd.Env, "SSH_ASKPASS_REQUIRE"); v != "force" {
		t.Errorf("SSH_ASKPASS_REQUIRE = %q, want force — ssh prefers the terminal without it", v)
	}
	if v, _ := effective(cmd.Env, sshx.EnvCredential); v != "cred-1" {
		t.Errorf("the credential the helper looks up = %q, want cred-1", v)
	}
	if v, _ := effective(cmd.Env, "TERM"); v != "xterm-256color" {
		t.Errorf("TERM = %q, want xterm-256color", v)
	}
}

// A host with no credential is the plain child it always was, carrying only
// the TERM the emulator implements.
func TestAPaneWithoutTmuxAndWithoutACredentialCarriesOnlyTERM(t *testing.T) {
	cmd, _ := sessionCommand(store.Host{Name: "web", Addr: "10.0.1.1"}, false)

	if _, ok := effective(cmd.Env, "SSH_ASKPASS"); ok {
		t.Error("SSH_ASKPASS appeared for a host that does not use a password")
	}
	if v, _ := effective(cmd.Env, "TERM"); v != "xterm-256color" {
		t.Errorf("TERM = %q, want xterm-256color", v)
	}
}

// With tmux the same environment travels in the command tmux records, and must
// not also be set on the process that asks tmux for the session: that server
// is shared with every other host, and its environment is inherited by panes
// this credential has nothing to do with.
func TestATmuxPaneKeepsACredentialOutOfTheServersEnvironment(t *testing.T) {
	cmd, session := sessionCommand(passwordHost(), true)

	if session == "" {
		t.Fatal("a tmux-backed pane has a session name to reattach to")
	}
	if args := strings.Join(cmd.Args, " "); !strings.Contains(args, sshx.EnvCredential+"=cred-1") {
		t.Errorf("the credential is not in the command tmux records:\n  %s", args)
	}
	if _, ok := effective(cmd.Env, sshx.EnvCredential); ok {
		t.Error("the credential reached the tmux server's environment, where other panes inherit it")
	}
	if v, _ := effective(cmd.Env, "TERM"); v != "xterm-256color" {
		t.Errorf("TERM = %q, want xterm-256color — the tmux client draws into our emulator", v)
	}
}
