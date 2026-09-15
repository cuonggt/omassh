package secret

import "strings"

// keychain is the macOS login keychain, reached through security(1).
//
// Through the command rather than the Security framework, because the release
// is built with CGO_ENABLED=0 and the framework is only reachable through cgo.
// A statically linked binary that runs everywhere is worth more here than
// saving a process.
type keychain struct {
	run runner
}

// The account is the credential's id rather than its name, so renaming a
// credential in the interface does not orphan the password behind it.

func (k *keychain) Get(id string) (string, error) {
	out, err := k.run("security", []string{
		"find-generic-password", "-a", id, "-s", Service, "-w",
	}, "")
	if err != nil {
		if missing(err) {
			return "", ErrNotFound
		}
		return "", err
	}
	// security prints the password and a newline; a password may legitimately
	// end in whitespace, so only the one newline it added comes off.
	return strings.TrimSuffix(out, "\n"), nil
}

// Set writes the password in on standard input.
//
// -w is given no value, which makes security prompt for one instead of taking
// it from the argument list — and the argument list is the whole point: it is
// readable by every process on the machine for as long as the command runs,
// so a password passed as `-w hunter2` is a password shown to anyone running
// ps at the wrong moment.
//
// The prompt asks twice, to confirm, so the password is written twice. Sent
// once, the retype reads end-of-file, security says the passwords do not
// match, and then stores an empty one — the failure mode that looks exactly
// like success until the first connection.
//
// A named keychain cannot be combined with this: security takes the keychain
// as a trailing argument and reads it as the value of -w. So this writes to
// the default keychain, which is where a login password belongs anyway.
func (k *keychain) Set(id, password string) error {
	_, err := k.run("security", []string{
		"add-generic-password", "-a", id, "-s", Service, "-U", "-w",
	}, password+"\n"+password+"\n")
	return err
}

func (k *keychain) Delete(id string) error {
	_, err := k.run("security", []string{
		"delete-generic-password", "-a", id, "-s", Service,
	}, "")
	if err != nil && missing(err) {
		return nil // making sure it is gone, and it is
	}
	return err
}

// missing reports whether the tool's complaint was that there is no such item.
//
// By the text, because security exits 44 for this and 1 for a good many other
// things, and the code alone has been known to differ between releases. The
// sentence has been stable for as long as the command has existed.
func missing(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "could not be found")
}
