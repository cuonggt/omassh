package secret

// secretTool is the freedesktop secret service, reached through secret-tool(1)
// from libsecret. GNOME Keyring and KWallet both answer it, which is most of
// the Linux desktops there are; a machine with neither has no store, and Open
// says so rather than pretending.
//
// It differs from security(1) in two ways that matter, both established by
// running it rather than by reading about it:
//
// It says "there is no such item" by exiting 1 with nothing on stdout and
// nothing on stderr — for a lookup and for a clear alike. Silence and a
// failing code are also what a real problem looks like from the outside, so
// the two are told apart by the code and the absence of a message together.
//
// And it returns the secret exactly as it was given: no trailing newline of
// its own, unlike security(1), which adds one. So nothing is trimmed here. A
// password that ends in a newline is a password that ends in a newline, and
// trimming it would hand ssh something the person never typed.
type secretTool struct {
	run runner
}

// notFound is the exit code secret-tool uses for an item that is not there.
const notFound = 1

func (s *secretTool) Get(id string) (string, error) {
	out, err := s.run("secret-tool", []string{
		"lookup", "service", Service, "account", id,
	}, "")
	switch {
	case quietFailure(err, notFound):
		return "", ErrNotFound
	case err != nil:
		return "", err
	}
	return out, nil
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
	err := func() error {
		_, err := s.run("secret-tool", []string{
			"clear", "service", Service, "account", id,
		}, "")
		return err
	}()
	if quietFailure(err, notFound) {
		return nil // making sure it is gone, and it is
	}
	return err
}
