package sftpx

import (
	"fmt"
	"io"
)

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

	w, err := dst.Create(dstPath)
	if err != nil {
		return err
	}

	pw := &progressWriter{w: w, total: info.Size, fn: fn}
	if _, err := io.Copy(pw, r); err != nil {
		w.Close()
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	// Best effort: permissions are informative, and failing the whole transfer
	// because a mode could not be set would be worse than not setting it.
	dst.Chmod(dstPath, info.Mode.Perm())
	return nil
}
