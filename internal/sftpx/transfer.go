package sftpx

import (
	"errors"
	"fmt"
	"io"
	"path"
)

// PartialSuffix names the file a transfer is still writing. It sits beside the
// destination rather than in a temporary directory, because the move onto the
// destination has to stay on the same filesystem to be a move at all.
const PartialSuffix = ".omassh-part"

// Progress reports bytes copied so far for one file.
type Progress func(done, total int64)

// ErrIsDirectory is what Copy refuses a source that has no bytes of its own.
// CopyDir descends a directory instead of copying it, and needs to tell that
// refusal apart from a transfer that actually went wrong.
var ErrIsDirectory = errors.New("is a directory")

// progressWriter counts bytes on their way through.
type progressWriter struct {
	w     io.Writer
	done  int64
	total int64
	fn    Progress
}

func (p *progressWriter) Write(b []byte) (int, error) {
	n, err := p.w.Write(b)
	p.done += int64(n)
	if p.fn != nil {
		p.fn(p.done, p.total)
	}
	return n, err
}

// Copy transfers one file between two filesystems. Either side may be local or
// remote, so the same call handles upload and download.
func Copy(dst FS, dstPath string, src FS, srcPath string, fn Progress) error {
	info, err := src.Stat(srcPath)
	if err != nil {
		return err
	}
	if info.IsDir {
		return fmt.Errorf("%s %w", info.Name, ErrIsDirectory)
	}

	r, err := src.Open(srcPath)
	if err != nil {
		return err
	}
	defer r.Close()

	// The file is written beside the destination and moved over it at the
	// end, so that the destination is only ever the old file or the new one.
	// Writing straight to it truncated it before a single byte had arrived: a
	// transfer that then failed left neither what was there nor what was
	// wanted, and since the browser only reloaded a pane after a transfer that
	// worked, the listing went on reporting the size the file used to be.
	tmpPath := dstPath + PartialSuffix
	w, err := dst.Create(tmpPath)
	if err != nil {
		return err
	}

	pw := &progressWriter{w: w, total: info.Size, fn: fn}
	// Once before anything is written, so the size being moved is known from
	// the start rather than after the first chunk — and is known at all for a
	// file with no chunks in it. A caller that shows progress has something to
	// show immediately, and one that reports what it moved is not left with
	// whatever it guessed beforehand.
	if fn != nil {
		fn(0, info.Size)
	}
	if _, err := io.Copy(pw, r); err != nil {
		w.Close()
		dst.Remove(tmpPath)
		return err
	}
	if err := w.Close(); err != nil {
		dst.Remove(tmpPath)
		return err
	}
	// Best effort: permissions are informative, and failing the whole transfer
	// because a mode could not be set would be worse than not setting it. It
	// happens before the move so the file arrives already wearing them.
	dst.Chmod(tmpPath, info.Mode.Perm())

	if err := dst.Replace(tmpPath, dstPath); err != nil {
		dst.Remove(tmpPath)
		return err
	}
	return nil
}

// DirProgress reports where a directory copy has reached: the file on its way
// now, named relative to the directory that was picked, and how far through
// that one file the copy is.
type DirProgress func(rel string, done, total int64)

// DirResult is what a directory copy moved. It is worth reading after a
// failure too: the copy stops where it stopped, and this says how far it got
// and on what.
type DirResult struct {
	Files   int   // files that arrived
	Bytes   int64 // what they came to
	Skipped int   // names with no bytes to copy — see CopyDir
	// Failed is the file or directory the copy stopped on, relative to the
	// one picked, or "" for the picked directory itself. The name is kept
	// here rather than written into the error because it comes off the far
	// side, and the caller is the half of this that knows how to make remote
	// text safe to draw.
	Failed string
}

// CopyDir copies a directory and everything under it to the other side.
//
// The tree is walked by the listing rather than by following each name, and
// that is what keeps it finite: List reports a symlink as the link rather than
// as the thing it points at, so the walk descends only real directories, and a
// real directory cannot contain itself. Following instead would turn one link
// pointing at an ancestor into a copy that never ends.
//
// That leaves the links themselves. Nothing in FS can write one, so a link is
// copied the way copying its own row already copies it — followed, and the
// file it names moved. A link to a directory has no bytes to move, so it is
// counted in Skipped and stepped over rather than failing a tree that is
// otherwise fine: one link is a poor reason to abandon ten thousand files.
//
// Each file still lands atomically, by the part-file dance in Copy. The tree
// as a whole does not, and cannot: a copy that fails halfway leaves behind
// what it had already written, which is why Failed says where it stopped
// rather than pretending nothing happened.
func CopyDir(dst FS, dstPath string, src FS, srcPath string, fn DirProgress) (DirResult, error) {
	var r DirResult
	err := copyTree(dst, dstPath, src, srcPath, "", fn, &r)
	return r, err
}

func copyTree(dst FS, dstDir string, src FS, srcDir, rel string, fn DirProgress, r *DirResult) error {
	info, err := dst.Stat(dstDir)
	switch {
	case err != nil:
		if err := dst.Mkdir(dstDir); err != nil {
			r.Failed = rel
			return err
		}
	case !info.IsDir:
		// Belt and braces: the browser refuses this before asking, so getting
		// here means the name was taken between the question and the answer.
		r.Failed = rel
		return errors.New("there is already a file by that name")
	}
	// An existing directory is copied into rather than refused, so sending a
	// tree twice brings what has been added to it without disturbing the rest.

	entries, err := src.List(srcDir)
	if err != nil {
		r.Failed = rel
		return err
	}
	for _, e := range entries {
		s, d := src.Join(srcDir, e.Name), dst.Join(dstDir, e.Name)
		// Slash-separated whatever either side uses locally: this is the name
		// the copy reports itself by, not a path anything opens.
		child := path.Join(rel, e.Name)

		if e.IsDir {
			if err := copyTree(dst, d, src, s, child, fn, r); err != nil {
				return err // whatever it stopped on is already recorded
			}
			continue
		}

		// What actually moved, which is not always what the row said: a
		// listing reports a link's own size — the length of the path in it —
		// while the copy follows it and moves the file it names.
		moved := e.Size
		err := Copy(dst, d, src, s, func(done, total int64) {
			moved = total
			if fn != nil {
				fn(child, done, total)
			}
		})
		switch {
		case errors.Is(err, ErrIsDirectory):
			r.Skipped++
		case err != nil:
			r.Failed = child
			return err
		default:
			r.Files++
			r.Bytes += moved
		}
	}

	// The mode last, because setting it first can lock the copy out of the
	// directory it is about to fill: a source at 0555 would be recreated
	// read-only and the first file into it refused. Best effort, as with a
	// file's: arriving with the wrong permissions beats not arriving.
	if srcInfo, err := src.Stat(srcDir); err == nil {
		dst.Chmod(dstDir, srcInfo.Mode.Perm())
	}
	return nil
}
