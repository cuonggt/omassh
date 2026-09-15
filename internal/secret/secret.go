// Package secret keeps the one thing omassh stores that its own files must
// not hold.
//
// Everything else omassh knows lives in bbolt, plain on disk, and travels in
// an export written to sit in a dotfiles repository. A password cannot go
// there — not in the database, which is a file like any other, and certainly
// not in the export, whose whole promise is that it holds nothing secret. So
// it goes where the operating system already keeps such things, and omassh
// stores a name for it rather than the thing itself.
//
// The same argument the rest of omassh makes about ssh-agent: there is a
// facility for this already, and reimplementing it would be worse than using
// it.
package secret

import (
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
)

// Service is the name every item is filed under, so that what omassh put in
// the keychain can be told from everything else there.
const Service = "omassh"

var (
	// ErrNotFound is a credential the store has never been told about. It is
	// not a failure of the store: a credential can exist in the list with no
	// password behind it yet, and the interface has to say which of the two
	// it is looking at.
	ErrNotFound = errors.New("no password stored for it")

	// ErrNoStore is a machine with nowhere to put one. A Linux box with no
	// keyring daemon is the ordinary case — someone ssh'd into a server — and
	// it has to be reported as what it is rather than as a password that
	// would not save.
	ErrNoStore = errors.New("no keychain on this machine to keep a password in")
)

// Store is where a password lives.
type Store interface {
	// Get returns the password for a credential id, or ErrNotFound.
	Get(id string) (string, error)
	// Set stores one, replacing whatever was there.
	Set(id, password string) error
	// Delete removes one. Removing what is not there is not an error: the
	// caller is making sure it is gone, and it is.
	Delete(id string) error
}

// runner executes a command and returns its standard output. It exists so a
// test can record what would have been run — above all, that the password
// never reaches the argument list.
type runner func(name string, args []string, stdin string) (string, error)

func execRun(name string, args []string, stdin string) (string, error) {
	cmd := exec.Command(name, args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	// In a session of its own, with no controlling terminal.
	//
	// security(1) asks for a password on /dev/tty when it can find one, and
	// only falls back to standard input when it cannot. Run from the browser
	// — which has a terminal, and is drawing an interface on it — it opened
	// that terminal behind omassh's back, printed "password data for new
	// item:" into the middle of the frame, and then waited for an answer on a
	// device nothing was typing into. The password went to the store, the
	// keychain got nothing, and the command hung until it was killed.
	//
	// Setsid takes the terminal away, which leaves stdin as the only thing it
	// can read. It costs nothing elsewhere: none of these commands has any
	// business talking to the terminal, and on macOS the prompt to unlock a
	// locked keychain is a window rather than a tty prompt, so that still
	// reaches the person.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			// The tools write their reason to stderr and nothing useful to
			// stdout, so that is the half worth keeping.
			if msg := strings.TrimSpace(string(ee.Stderr)); msg != "" {
				return "", errors.New(msg)
			}
		}
		return "", err
	}
	return string(out), nil
}

// Open finds the store this machine keeps passwords in.
//
// By what is actually installed rather than by the operating system alone: a
// Linux machine with no keyring daemon has no store, and saying so at the
// point someone types a password is far better than accepting it and losing
// it.
func Open() (Store, error) {
	switch runtime.GOOS {
	case "darwin":
		if _, err := exec.LookPath("security"); err == nil {
			return &keychain{run: execRun}, nil
		}
	case "linux":
		if _, err := exec.LookPath("secret-tool"); err == nil {
			return &secretTool{run: execRun}, nil
		}
		return nil, fmt.Errorf("%w — install libsecret-tools, or use a key instead of a password", ErrNoStore)
	}
	return nil, ErrNoStore
}
