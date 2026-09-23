package sshx

import (
	"strings"
	"testing"

	"github.com/cuonggt/omassh/internal/store"
)

func passwordHost() store.Host {
	return store.Host{Name: "switch", Addr: "10.0.9.1", User: "admin",
		Cred: &store.Credential{ID: "c1", Name: "Legacy switch",
			Kind: store.CredentialPassword, User: "admin"}}
}

func keyHost() store.Host {
	return store.Host{Name: "web", Addr: "10.0.1.1", User: "deploy",
		Cred: &store.Credential{ID: "c2", Name: "Prod", Kind: store.CredentialKey,
			User: "deploy", Identity: "~/.ssh/k"}}
}

// The password is never in the environment. Only the id of the credential is,
// and the environment is inherited by every child of the process: a password
// put here would be handed to ssh, and to everything ssh runs, including the
// ProxyCommand of a jump host.
func TestTheEnvironmentCarriesAnIdAndNotAPassword(t *testing.T) {
	env := Env(passwordHost())
	if len(env) == 0 {
		t.Fatal("a password credential produced no environment at all")
	}
	joined := strings.Join(env, " ")
	if !strings.Contains(joined, EnvCredential+"=c1") {
		t.Errorf("the credential id is not in %q", joined)
	}
	if !strings.Contains(joined, "SSH_ASKPASS_REQUIRE=force") {
		t.Errorf("ssh would still prefer the terminal: %q", joined)
	}
	for _, e := range env {
		if strings.Contains(strings.ToLower(e), "password=") &&
			!strings.HasPrefix(e, "SSH_ASKPASS") {
			t.Errorf("something password-shaped is in the environment: %q", e)
		}
	}
}

// Every other kind of credential is the connection omassh has always made.
func TestOnlyAPasswordCredentialChangesTheEnvironment(t *testing.T) {
	for _, h := range []store.Host{keyHost(), {Name: "plain", Addr: "10.0.0.1"}} {
		if env := Env(h); len(env) != 0 {
			t.Errorf("%s got %v, want nothing", h.Name, env)
		}
	}
}

// Only a connection someone is sitting in front of says so. Told someone is
// there, the helper puts a question to the terminal and waits for an answer —
// and a tunnel or an sftp session told that would wait on a prompt nobody can
// see, which is the one thing an unattended connection must never do.
func TestOnlyAnAttendedConnectionSaysSomeoneIsThere(t *testing.T) {
	if env := Env(passwordHost()); strings.Contains(strings.Join(env, " "), EnvAttended) {
		t.Errorf("the unattended environment says someone is there: %v", env)
	}
	attended := strings.Join(AttendedEnv(passwordHost()), " ")
	if !strings.Contains(attended, EnvAttended+"=1") {
		t.Errorf("the attended environment does not say so: %q", attended)
	}
	if !strings.Contains(attended, EnvCredential+"=c1") {
		t.Errorf("saying so lost the credential: %q", attended)
	}
	// A host with no password has no helper to tell anything.
	if env := AttendedEnv(keyHost()); len(env) != 0 {
		t.Errorf("a key host got %v, want nothing", env)
	}
}

// ssh is told not to walk the agent's keys first. On a host offering several,
// it can exhaust MaxAuthTries on keys it was never going to be let in with and
// be refused before it reaches the password at all.
func TestAPasswordCredentialStopsSshTryingKeysFirst(t *testing.T) {
	got := strings.Join(Build(passwordHost()), " ")
	if !strings.Contains(got, "PreferredAuthentications=password") {
		t.Errorf("args = %q, want the authentication order set", got)
	}
	// And it is ahead of the -o options omassh itself was given, since ssh
	// keeps the first value it sees for a setting.
	SetGlobalOptions([]string{"PreferredAuthentications=publickey"})
	defer SetGlobalOptions(nil)
	got = strings.Join(Build(passwordHost()), " ")
	ours := strings.Index(got, "PreferredAuthentications=password")
	theirs := strings.Index(got, "PreferredAuthentications=publickey")
	if ours < 0 || theirs < 0 || ours > theirs {
		t.Errorf("the credential's own preference has to come first:\n  %s", got)
	}
}

// A connection with nobody in front of it must never wait. BatchMode is how
// that has always been said, and it stays so for everything but a password
// credential — which BatchMode would refuse before the askpass helper could
// answer.
func TestAnUnattendedConnectionNeverWaits(t *testing.T) {
	t.Run("without a password credential", func(t *testing.T) {
		got := strings.Join(Unattended(keyHost()), " ")
		if !strings.Contains(got, "BatchMode=yes") {
			t.Errorf("got %q, want BatchMode=yes", got)
		}
	})
	t.Run("with one", func(t *testing.T) {
		got := strings.Join(Unattended(passwordHost()), " ")
		if strings.Contains(got, "BatchMode=yes") {
			t.Errorf("BatchMode=yes would refuse the askpass helper: %q", got)
		}
		// The guard that replaces it: one attempt, then failure. Without this
		// a wrong password loops on a prompt nobody can see, which is the very
		// thing BatchMode was there to prevent.
		if !strings.Contains(got, "NumberOfPasswordPrompts=1") {
			t.Errorf("got %q, want a single attempt and no more", got)
		}
	})
}

// The subsystem path carries it too: an sftp child's stdin is the protocol,
// so the askpass helper is the only thing that can answer for it.
func TestTheSubsystemPathCarriesTheCredentialsOptions(t *testing.T) {
	got := strings.Join(SubsystemArgs(passwordHost(), "sftp", Unattended(passwordHost()), nil), " ")
	if strings.Contains(got, "BatchMode=yes") {
		t.Errorf("sftp would refuse a password credential: %q", got)
	}
	if !strings.Contains(got, "NumberOfPasswordPrompts=1") {
		t.Errorf("args = %q, want the single-attempt guard", got)
	}
}
