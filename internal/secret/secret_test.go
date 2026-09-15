package secret

import (
	"errors"
	"strings"
	"testing"
)

// recorder stands in for the command, keeping what it was asked to run.
type recorder struct {
	name  string
	args  []string
	stdin string
	out   string
	err   error
}

func (r *recorder) run(name string, args []string, stdin string) (string, error) {
	r.name, r.args, r.stdin = name, args, stdin
	return r.out, r.err
}

const password = "correct horse battery staple"

// The password must never reach the argument list.
//
// Every process on the machine can read the arguments of every other, for as
// long as it runs, so `security -w hunter2` is a password handed to anyone
// running ps at the wrong moment. It goes on standard input instead, and this
// is the test that says so — the shape of the command is easy to simplify back
// into the insecure one by someone who does not know why it is like this.
func TestThePasswordNeverReachesTheArgumentList(t *testing.T) {
	for _, tc := range []struct {
		name  string
		store func(*recorder) Store
	}{
		{"keychain", func(r *recorder) Store { return &keychain{run: r.run} }},
		{"secret-tool", func(r *recorder) Store { return &secretTool{run: r.run} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &recorder{}
			if err := tc.store(r).Set("cred-1", password); err != nil {
				t.Fatal(err)
			}
			for _, a := range r.args {
				if strings.Contains(a, password) {
					t.Errorf("the password is in the arguments: %q", strings.Join(r.args, " "))
				}
			}
			if !strings.Contains(r.stdin, password) {
				t.Errorf("the password did not go in on stdin; stdin was %q", r.stdin)
			}
		})
	}
}

// security asks twice, to confirm. Sent once, the retype reads end-of-file,
// the command says the passwords do not match and then stores an empty one —
// which looks exactly like success until the first connection fails.
func TestTheKeychainAnswersTheRetypePrompt(t *testing.T) {
	r := &recorder{}
	if err := (&keychain{run: r.run}).Set("cred-1", "hunter2"); err != nil {
		t.Fatal(err)
	}
	if got, want := r.stdin, "hunter2\nhunter2\n"; got != want {
		t.Errorf("stdin = %q, want the password twice (%q)", got, want)
	}
}

// A credential with no password behind it yet is an ordinary state, not a
// broken store: the interface has to tell those two apart to know whether to
// say "not in the keychain" or to report a failure.
func TestAMissingPasswordIsNotAFailure(t *testing.T) {
	t.Run("keychain", func(t *testing.T) {
		r := &recorder{err: errors.New("security: SecKeychainSearchCopyNext: The specified item could not be found in the keychain.")}
		if _, err := (&keychain{run: r.run}).Get("cred-1"); !errors.Is(err, ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})
	// secret-tool says nothing at all and still exits zero, so the empty
	// answer is what a missing item looks like there.
	t.Run("secret-tool", func(t *testing.T) {
		r := &recorder{out: ""}
		if _, err := (&secretTool{run: r.run}).Get("cred-1"); !errors.Is(err, ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})
}

// Deleting what is not there is what the caller asked for: they wanted it
// gone, and it is.
func TestDeletingAPasswordThatIsNotThereIsNotAnError(t *testing.T) {
	r := &recorder{err: errors.New("security: SecKeychainSearchCopyNext: The specified item could not be found in the keychain.")}
	if err := (&keychain{run: r.run}).Delete("cred-1"); err != nil {
		t.Errorf("err = %v, want it to pass quietly", err)
	}
}

// A password may end in whitespace, so only the newline the tool added comes
// off — trimming freely would quietly change what someone typed.
func TestOnlyTheToolsOwnNewlineIsTrimmed(t *testing.T) {
	r := &recorder{out: "two trailing spaces  \n"}
	got, err := (&keychain{run: r.run}).Get("cred-1")
	if err != nil {
		t.Fatal(err)
	}
	if want := "two trailing spaces  "; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// The account is the credential's id, never its name, so renaming a
// credential does not orphan the password behind it.
func TestThePasswordIsFiledUnderTheIdRatherThanTheName(t *testing.T) {
	r := &recorder{}
	if err := (&keychain{run: r.run}).Set("cred-abc123", "x"); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(r.args, " ")
	if !strings.Contains(joined, "cred-abc123") {
		t.Errorf("the id is not in %q", joined)
	}
	if !strings.Contains(joined, "-s "+Service) && !strings.Contains(joined, Service) {
		t.Errorf("the service name is not in %q", joined)
	}
}

// The in-memory store is what the rest of omassh is tested against, so it has
// to behave like the real ones at the edges.
func TestTheMemoryStoreBehavesLikeTheRealOnes(t *testing.T) {
	m := Memory()

	if _, err := m.Get("nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get of a missing id = %v, want ErrNotFound", err)
	}
	if err := m.Delete("nope"); err != nil {
		t.Errorf("Delete of a missing id = %v, want it to pass quietly", err)
	}
	if err := m.Set("cred-1", password); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.Get("cred-1"); got != password {
		t.Errorf("got %q, want %q", got, password)
	}
	if err := m.Set("cred-1", "replaced"); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.Get("cred-1"); got != "replaced" {
		t.Errorf("a second Set did not replace: %q", got)
	}
	if err := m.Delete("cred-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Get("cred-1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("after Delete, Get = %v, want ErrNotFound", err)
	}
}

// What goes in on standard input reaches the command, and what the command
// prints comes back. /bin/cat is the smallest thing that proves both.
func TestTheRunnerCarriesStdinAndReturnsStdout(t *testing.T) {
	got, err := execRun("/bin/cat", nil, "the password\n")
	if err != nil {
		t.Fatal(err)
	}
	if want := "the password\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// The tools explain themselves on stderr and print nothing useful on stdout,
// so the error has to be the half they wrote. Without this a locked keychain
// arrived as "exit status 1", which says nothing anyone can act on.
func TestTheRunnerReportsWhatTheToolSaidOnStderr(t *testing.T) {
	_, err := execRun("/bin/sh", []string{"-c", "echo 'the keychain is locked' >&2; exit 44"}, "")
	if err == nil {
		t.Fatal("a command that failed was reported as success")
	}
	if got := err.Error(); got != "the keychain is locked" {
		t.Errorf("err = %q, want the tool's own sentence", got)
	}
}

// A command that is not there at all still has to say so.
func TestTheRunnerReportsACommandThatIsNotThere(t *testing.T) {
	if _, err := execRun("omassh-no-such-command", nil, ""); err == nil {
		t.Error("a missing command was reported as success")
	}
}

// An error that is not "no such item" is a real failure and must not be
// mistaken for an empty store — a locked keychain reported as ErrNotFound
// would have the interface offer to store a password that is already there.
func TestARealFailureIsNotMistakenForAMissingPassword(t *testing.T) {
	r := &recorder{err: errors.New("the keychain is locked")}
	if _, err := (&keychain{run: r.run}).Get("cred-1"); errors.Is(err, ErrNotFound) {
		t.Error("a locked keychain was reported as no password being stored")
	}
	if err := (&keychain{run: r.run}).Delete("cred-1"); err == nil {
		t.Error("a locked keychain was reported as a successful delete")
	}
}

// secret-tool is already quiet about clearing what was never there, so the
// backend passes its answer straight through.
func TestClearingThroughSecretToolIsQuiet(t *testing.T) {
	r := &recorder{}
	if err := (&secretTool{run: r.run}).Delete("cred-1"); err != nil {
		t.Fatal(err)
	}
	if r.args[0] != "clear" {
		t.Errorf("ran %q, want clear", strings.Join(r.args, " "))
	}
	if strings.Contains(strings.Join(r.args, " "), "\x00") {
		t.Error("unexpected NUL in the arguments")
	}
}

// Open answers with a store or with a reason, never with neither.
func TestOpenGivesAStoreOrSaysWhyNot(t *testing.T) {
	st, err := Open()
	switch {
	case err != nil && st != nil:
		t.Error("both a store and an error")
	case err == nil && st == nil:
		t.Error("neither a store nor an error")
	case err != nil && !errors.Is(err, ErrNoStore):
		t.Errorf("err = %v, want it to be ErrNoStore", err)
	}
}
