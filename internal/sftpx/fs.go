// Package sftpx browses and transfers files over OpenSSH's sftp subsystem.
package sftpx

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pkg/sftp"
)

// Entry is one row in a file listing, on either side of a transfer.
type Entry struct {
	Name    string
	Size    int64
	Mode    os.FileMode
	ModTime time.Time
	IsDir   bool
}

// FS is the small slice of filesystem behaviour the browser needs. Local and
// remote panes implement the same interface so the UI has one code path.
type FS interface {
	Home() string
	Join(dir, name string) string
	Parent(dir string) string
	List(dir string) ([]Entry, error)
	Mkdir(p string) error
	Remove(p string) error
	Rename(old, neu string) error
	// Replace moves src over dst, whether or not dst is already there. It is
	// how a transfer lands: the file written beside the destination becomes
	// the destination in one step, so a reader never sees half of it.
	Replace(src, dst string) error
	Chmod(p string, mode os.FileMode) error
	Open(p string) (io.ReadCloser, error)
	Create(p string) (io.WriteCloser, error)
	Stat(p string) (Entry, error)
	Label() string
}

// Problem puts a failed operation on a name into omassh's words.
//
// pkg/sftp turns the two status codes with os equivalents into os.ErrNotExist
// and os.ErrPermission and hands back the rest as they came, so a server
// refusing a name that is already taken reached the user as
// `sftp: "Failure" (SSH_FX_FAILURE)` — a protocol constant, shown to someone
// who has just typed a directory name.
//
// SSH_FX_FAILURE does not say why, so the reason is established here rather
// than guessed at: the destination and the directory meant to hold it are
// asked about, the same way a forward binds its port itself before asking ssh
// to. What none of that explains is reported as the server refusing, which is
// all the protocol actually said.
func Problem(fs FS, dest, name string, err error) error {
	if err == nil {
		return nil
	}
	if _, serr := fs.Stat(dest); serr == nil {
		return fmt.Errorf("%s is already there", name)
	}
	if dir := fs.Parent(dest); dir != dest {
		if _, serr := fs.Stat(dir); serr != nil {
			// By its last part, not the whole path. The path is long, the
			// message is a line inside a dialog, and spelling it out put the
			// directory being complained about past the right-hand edge —
			// which is the one word the sentence exists to say.
			return fmt.Errorf("there is no %s here to put it in", path.Base(dir))
		}
	}
	switch {
	case errors.Is(err, os.ErrPermission):
		return fmt.Errorf("%s: permission denied", name)
	case errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("%s is not there any more", name)
	}
	var status *sftp.StatusError
	if errors.As(err, &status) {
		// Everything checkable has been checked, and the protocol carries no
		// reason of its own — saying which is better than naming its constant.
		return fmt.Errorf("%s: the server refused it, without saying why", name)
	}
	return err
}

// sortEntries puts directories first, then names, case-insensitively — the
// order people expect from a file manager.
func sortEntries(es []Entry) {
	sort.Slice(es, func(i, j int) bool {
		if es[i].IsDir != es[j].IsDir {
			return es[i].IsDir
		}
		return strings.ToLower(es[i].Name) < strings.ToLower(es[j].Name)
	})
}

// Local is the machine Omassh runs on.
type Local struct{}

func (Local) Label() string { return "local" }

func (Local) Home() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return "/"
	}
	return h
}

func (Local) Join(dir, name string) string { return filepath.Join(dir, name) }

func (Local) Parent(dir string) string { return filepath.Dir(dir) }

func (Local) List(dir string) ([]Entry, error) {
	des, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(des))
	for _, de := range des {
		fi, err := de.Info()
		if err != nil {
			continue // a file that vanished mid-listing is not fatal
		}
		out = append(out, Entry{
			Name: fi.Name(), Size: fi.Size(), Mode: fi.Mode(),
			ModTime: fi.ModTime(), IsDir: fi.IsDir(),
		})
	}
	sortEntries(out)
	return out, nil
}

func (Local) Mkdir(p string) error         { return os.Mkdir(p, 0o755) }
func (Local) Remove(p string) error        { return os.RemoveAll(p) }
func (Local) Rename(old, neu string) error { return os.Rename(old, neu) }

// os.Rename already replaces an existing destination on every platform Omassh
// runs on.
func (Local) Replace(src, dst string) error { return os.Rename(src, dst) }

func (Local) Chmod(p string, m os.FileMode) error  { return os.Chmod(p, m) }
func (Local) Open(p string) (io.ReadCloser, error) { return os.Open(p) }

func (Local) Create(p string) (io.WriteCloser, error) { return os.Create(p) }

func (Local) Stat(p string) (Entry, error) {
	fi, err := os.Stat(p)
	if err != nil {
		return Entry{}, err
	}
	return Entry{Name: fi.Name(), Size: fi.Size(), Mode: fi.Mode(),
		ModTime: fi.ModTime(), IsDir: fi.IsDir()}, nil
}

// remote paths are always slash-separated, whatever the local OS uses.
func remoteJoin(dir, name string) string { return path.Join(dir, name) }
func remoteParent(dir string) string     { return path.Dir(dir) }
