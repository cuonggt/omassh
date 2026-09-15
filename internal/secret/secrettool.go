package secret

import "strings"

// secretTool is the freedesktop secret service, reached through secret-tool(1)
// from libsecret. GNOME Keyring and KWallet both answer it, which is most of
// the Linux desktops there are; a machine with neither has no store, and Open
// says so rather than pretending.
type secretTool struct {
	run runner
}

func (s *secretTool) Get(id string) (string, error) {
	out, err := s.run("secret-tool", []string{
		"lookup", "service", Service, "account", id,
	}, "")
	if err != nil {
		return "", err
	}
	// Unlike security, lookup prints nothing at all when there is no match and
	// still exits zero, so the empty answer is what "not found" looks like.
	if out == "" {
		return "", ErrNotFound
	}
	return strings.TrimSuffix(out, "\n"), nil
}

// Set passes the password on standard input, for the same reason the keychain
// does — store takes it there by design, and never from the argument list.
func (s *secretTool) Set(id, password string) error {
	_, err := s.run("secret-tool", []string{
		"store", "--label=omassh", "service", Service, "account", id,
	}, password)
	return err
}

func (s *secretTool) Delete(id string) error {
	// clear is already quiet about a match that was not there.
	_, err := s.run("secret-tool", []string{
		"clear", "service", Service, "account", id,
	}, "")
	return err
}
