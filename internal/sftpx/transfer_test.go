package sftpx_test

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/cuonggt/omassh/internal/sftpx"
)

// haltingFS reads like the local filesystem until a set number of bytes have
// gone by, then fails the way a dropped connection does.
type haltingFS struct {
	sftpx.Local
	after int64
}

type haltingReader struct {
	r    io.ReadCloser
	left int64
}

func (h *haltingReader) Read(p []byte) (int, error) {
	if h.left <= 0 {
		return 0, errors.New("connection lost")
	}
	if int64(len(p)) > h.left {
		p = p[:h.left]
	}
	n, err := h.r.Read(p)
	h.left -= int64(n)
	return n, err
}

func (h *haltingReader) Close() error { return h.r.Close() }

func (f haltingFS) Open(p string) (io.ReadCloser, error) {
	r, err := f.Local.Open(p)
	if err != nil {
		return nil, err
	}
	return &haltingReader{r: r, left: f.after}, nil
}

const wasThere = "the file that was already here"

// Writing straight to the destination truncated it before a single byte had
// arrived, so a transfer that then failed left neither the file that was there
// nor the one that was wanted.
func TestAFailedTransferLeavesTheDestinationAlone(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "payload.bin")
	dst := filepath.Join(dir, "existing.bin")
	write(t, src, bytes.Repeat([]byte("x"), 200_000))
	write(t, dst, []byte(wasThere))

	err := sftpx.Copy(sftpx.Local{}, dst, haltingFS{after: 4096}, src, nil)
	if err == nil {
		t.Fatal("the transfer was supposed to fail")
	}

	got, rerr := os.ReadFile(dst)
	if rerr != nil {
		t.Fatalf("the destination is gone entirely: %v", rerr)
	}
	if string(got) != wasThere {
		t.Errorf("the destination is now %d bytes of a transfer that failed", len(got))
	}
	if _, err := os.Stat(dst + sftpx.PartialSuffix); !os.IsNotExist(err) {
		t.Errorf("a half-written file was left beside the destination")
	}
}

// The same, with nothing at the destination to begin with: a failure should
// not leave a file that was never completed.
func TestAFailedTransferLeavesNothingBehind(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "payload.bin")
	dst := filepath.Join(dir, "new.bin")
	write(t, src, bytes.Repeat([]byte("x"), 200_000))

	if err := sftpx.Copy(sftpx.Local{}, dst, haltingFS{after: 4096}, src, nil); err == nil {
		t.Fatal("the transfer was supposed to fail")
	}
	for _, p := range []string{dst, dst + sftpx.PartialSuffix} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s was left behind by a transfer that failed", filepath.Base(p))
		}
	}
}

// And the ordinary case still works, mode and all.
func TestATransferThatWorksLandsCompletely(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "payload.bin")
	dst := filepath.Join(dir, "existing.bin")
	body := bytes.Repeat([]byte("y"), 200_000)
	write(t, src, body)
	write(t, dst, []byte(wasThere))
	if err := os.Chmod(src, 0o640); err != nil {
		t.Fatal(err)
	}

	if err := sftpx.Copy(sftpx.Local{}, dst, sftpx.Local{}, src, nil); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, body) {
		t.Errorf("the destination holds %d bytes, want %d", len(got), len(body))
	}
	if fi, err := os.Stat(dst); err != nil {
		t.Fatal(err)
	} else if fi.Mode().Perm() != 0o640 {
		t.Errorf("mode = %v, want the source's 0640", fi.Mode().Perm())
	}
	if _, err := os.Stat(dst + sftpx.PartialSuffix); !os.IsNotExist(err) {
		t.Errorf("the partial file was not moved into place")
	}
}

func write(t *testing.T, p string, body []byte) {
	t.Helper()
	if err := os.WriteFile(p, body, 0o644); err != nil {
		t.Fatal(err)
	}
}
