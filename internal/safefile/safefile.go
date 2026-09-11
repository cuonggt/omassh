// Package safefile replaces a file without ever leaving it half written.
package safefile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Replace writes data over path, atomically and with the given mode.
//
// The files omassh rewrites are ones a machine is reached through — the ssh
// client config, its own settings — and a truncated one of those is every
// host at once. So the content goes to a file beside the target and is
// renamed over it, which is the one operation a filesystem promises is all or
// nothing: an interrupted write leaves the old file exactly as it was.
//
// A symlink is followed to the file it names. Dotfiles setups commonly link
// these into a repository, and renaming over the link would replace it with
// an ordinary file — detaching the config from whatever was tracking it, and
// leaving the tracked copy without a word of what was written. Resolving also
// puts the temporary file on the same filesystem as the real one, which is
// what keeps the rename atomic rather than a copy.
//
// It lives in one place because it was written twice, and the second copy did
// not get the symlink fix the first one had.
func Replace(path string, data []byte, mode os.FileMode) error {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	f, err := os.CreateTemp(dir, ".omassh-*")
	if err != nil {
		// The temporary file is omassh's own business. Named at the user, it
		// is a filename they have never seen standing in for the directory
		// they asked about, so the directory is named instead.
		var pe *os.PathError
		if errors.As(err, &pe) {
			return fmt.Errorf("%s: %w", dir, pe.Err)
		}
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)

	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
