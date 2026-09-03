// Package sftpx browses and transfers files over OpenSSH's sftp subsystem.
package sftpx

import (
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
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
	Chmod(p string, mode os.FileMode) error
	Open(p string) (io.ReadCloser, error)
	Create(p string) (io.WriteCloser, error)
	Stat(p string) (Entry, error)
	Label() string
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

func (Local) Mkdir(p string) error                 { return os.Mkdir(p, 0o755) }
func (Local) Remove(p string) error                { return os.RemoveAll(p) }
func (Local) Rename(old, neu string) error         { return os.Rename(old, neu) }
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
