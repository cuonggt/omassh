package secrets

import (
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"
)

// AskpassSockEnv names the environment variable carrying the handoff socket.
const AskpassSockEnv = "OMASSH_ASKPASS_SOCK"

// askBudget is how many times one WithAskpass call will hand over the secret.
// OpenSSH asks up to three times before treating the key as unusable.
const askBudget = 3

// WithAskpass runs fn with an environment that lets ssh-add obtain secret
// without prompting on a TTY.
//
// OpenSSH only accepts a passphrase from a terminal or from an SSH_ASKPASS
// helper, so Omassh points SSH_ASKPASS at itself and hands the secret over a
// single-use unix socket. The socket lives in a 0700 directory owned by the
// user and is removed as soon as fn returns; the secret never appears in an
// environment variable, a command line, or a file on disk.
func WithAskpass(secret string, fn func(env []string) error) error {
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate self for askpass: %w", err)
	}

	dir, err := os.MkdirTemp("", "omassh-askpass-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}

	sock := filepath.Join(dir, "s")
	l, err := net.Listen("unix", sock)
	if err != nil {
		return err
	}
	defer l.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		// ssh-add re-asks after a rejected passphrase and will keep doing so for
		// as long as the helper answers, so the budget is what makes a wrong
		// passphrase terminate. Past it, connections are accepted and closed
		// with no data: the helper returns an empty passphrase immediately and
		// ssh-add gives up, rather than stalling on a socket that never
		// answers at all.
		for served := 0; ; served++ {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			if served < askBudget {
				conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
				io.WriteString(conn, secret)
			}
			conn.Close()
		}
	}()

	env := append(os.Environ(),
		"SSH_ASKPASS="+self,
		"SSH_ASKPASS_REQUIRE=force",
		AskpassSockEnv+"="+sock,
		// Older ssh-add refuses to use an askpass helper without DISPLAY set.
		"DISPLAY=omassh",
	)
	err = fn(env)
	l.Close()
	<-done
	return err
}

// ServeAskpass is the SSH_ASKPASS side of the handoff: it prints the secret
// held by the parent process. It returns false when not invoked as an askpass
// helper, so main can carry on normally.
func ServeAskpass() bool {
	sock := os.Getenv(AskpassSockEnv)
	if sock == "" {
		return false
	}
	conn, err := net.DialTimeout("unix", sock, 5*time.Second)
	if err != nil {
		return true // fail closed: print nothing rather than prompting
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	io.Copy(os.Stdout, conn)
	return true
}
