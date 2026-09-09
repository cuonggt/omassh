package sftpx

import (
	"fmt"
	"io"
)

// PartialSuffix names the file a transfer is still writing. It sits beside the
// destination rather than in a temporary directory, because the move onto the
// destination has to stay on the same filesystem to be a move at all.
const PartialSuffix = ".omassh-part"

// Progress reports bytes copied so far for one file.
type Progress func(done, total int64)

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
		return fmt.Errorf("%s is a directory", info.Name)
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
