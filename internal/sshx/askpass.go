package sshx

import (
	"os"

	"github.com/cuonggt/omassh/internal/store"
)

// EnvCredential names the credential whose password ssh is asking for.
//
// Its presence is also what tells omassh it has been run as an askpass helper
// rather than by a person: ssh invokes the program with the prompt as its only
// argument, so there is no subcommand there to recognise.
const EnvCredential = "OMASSH_ASKPASS_CREDENTIAL"

// Env is what has to be added to the environment for a host that logs in with
// a password. Nothing at all for every other kind of credential.
//
// The password does not travel here, and that is the point. What travels is
// the credential's id and the path to omassh itself, which ssh runs to ask;
// the answer comes back on that helper's standard output, which belongs to the
// two processes and nothing else. An argument list is readable by everything
// on the machine, and an environment variable holding the secret would be
// readable by every child of it.
//
// SSH_ASKPASS_REQUIRE=force because ssh prefers the terminal whenever it has
// one, and here omassh already knows the answer — there is nothing to gain by
// asking the person again. It wants OpenSSH 8.4 or newer, which is 2020.
func Env(h store.Host) []string {
	if !wantsPassword(h) {
		return nil
	}
	self, err := os.Executable()
	if err != nil {
		// Nothing to point ssh at, so it falls back to asking on the terminal.
		// That still works for a session someone is sitting in front of, and
		// not for a tunnel; connecting less conveniently beats not at all.
		return nil
	}
	return []string{
		"SSH_ASKPASS=" + self,
		"SSH_ASKPASS_REQUIRE=force",
		EnvCredential + "=" + h.Cred.ID,
	}
}

// EnvAttended says someone is at the connection's terminal, so a question ssh
// asks that is not for the password can be put to them.
//
// ssh sends the helper every prompt the connection has, not only the password
// — SSH_ASKPASS_REQUIRE=force routes them all there — and the one that matters
// is whether to trust a host key it has not seen before. Only a person can say
// yes to that, so only a connection with one in front of it asks; everywhere
// else the answer is no, which is what the same connection says for a host
// that logs in with a key.
const EnvAttended = "OMASSH_ASKPASS_ATTENDED"

// AttendedEnv is Env for a connection someone is sitting in front of — the
// full-screen session and the pane.
func AttendedEnv(h store.Host) []string {
	env := Env(h)
	if len(env) == 0 {
		return nil
	}
	return append(env, EnvAttended+"=1")
}

// Unattended are the options for a connection with nobody in front of it: a
// forward in a detached tmux session, or an sftp child whose stdin is carrying
// the protocol rather than a keyboard.
//
// BatchMode is how that has always been said, and stays so for every
// credential but one. BatchMode turns off every prompt, the askpass helper
// included, so a password credential would be refused before it could answer.
// NumberOfPasswordPrompts=1 takes its place: ssh asks once, the helper answers
// out of the keychain, and a password that is wrong fails at once rather than
// looping — which is the thing BatchMode was guarding against. What must never
// happen here is a connection that waits, and neither of these waits.
func Unattended(h store.Host) []string {
	if wantsPassword(h) {
		return []string{"-o", "BatchMode=no", "-o", "NumberOfPasswordPrompts=1"}
	}
	return []string{"-o", "BatchMode=yes"}
}

func wantsPassword(h store.Host) bool {
	return h.Cred != nil && h.Cred.Kind == store.CredentialPassword
}
