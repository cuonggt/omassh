package sftpx_test

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
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

// Progress is reported once before anything is written.
//
// A caller showing a bar has something to show immediately, and — the reason
// it is here — one that reports what it moved is not left with whatever it
// guessed beforehand. A file with nothing in it produces no writes and so no
// progress at all, and the browser's guess is the size from the listing, which
// for a symlink is the length of the path it holds rather than the size of the
// file it names.
func TestProgressIsReportedBeforeAnythingIsWritten(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "empty")
	if err := os.WriteFile(src, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	var calls int
	var first, last [2]int64
	err := sftpx.Copy(sftpx.Local{}, filepath.Join(dir, "out"), sftpx.Local{}, src,
		func(done, total int64) {
			if calls == 0 {
				first = [2]int64{done, total}
			}
			last = [2]int64{done, total}
			calls++
		})
	if err != nil {
		t.Fatal(err)
	}
	if calls == 0 {
		t.Fatal("nothing was reported for a file with no bytes in it")
	}
	if first != [2]int64{0, 0} {
		t.Errorf("the first report is %v, want none of zero bytes", first)
	}
	if last[1] != 0 {
		t.Errorf("the size reported is %d, want 0", last[1])
	}
}

// mkTree builds a small tree worth copying: files at two depths, and a
// directory with nothing in it.
func mkTree(t *testing.T, root string) {
	t.Helper()
	for _, d := range []string{"", "guide", "guide/deep", "empty"} {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(d)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write(t, filepath.Join(root, "readme.txt"), []byte("top"))
	write(t, filepath.Join(root, "guide", "intro.md"), []byte("intro"))
	write(t, filepath.Join(root, "guide", "deep", "note.txt"), bytes.Repeat([]byte("z"), 5000))
}

// A directory copy brings the whole tree, empty directories included.
func TestADirectoryCopyTakesTheWholeTree(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "docs"), filepath.Join(dir, "copy")
	mkTree(t, src)

	res, err := sftpx.CopyDir(sftpx.Local{}, dst, sftpx.Local{}, src, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Files != 3 {
		t.Errorf("Files = %d, want the 3 files in the tree", res.Files)
	}
	if want := int64(3 + 5 + 5000); res.Bytes != want {
		t.Errorf("Bytes = %d, want %d", res.Bytes, want)
	}
	for _, p := range []string{"readme.txt", "guide/intro.md", "guide/deep/note.txt"} {
		if _, err := os.Stat(filepath.Join(dst, filepath.FromSlash(p))); err != nil {
			t.Errorf("%s did not arrive: %v", p, err)
		}
	}
	// A directory with nothing in it is still part of the tree.
	if fi, err := os.Stat(filepath.Join(dst, "empty")); err != nil || !fi.IsDir() {
		t.Errorf("the empty directory was not recreated")
	}
}

// A link pointing back up the tree must not send the copy round for ever.
//
// This is the whole reason the walk reads the listing instead of following
// each name: List reports the link rather than what it points at, so only real
// directories are descended, and a real directory cannot contain itself. If
// this ever regresses the test does not fail, it hangs — which is its own kind
// of report.
func TestADirectoryCopyDoesNotFollowALinkBackUpTheTree(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "docs"), filepath.Join(dir, "copy")
	mkTree(t, src)
	if err := os.Symlink("..", filepath.Join(src, "guide", "up")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	res, err := sftpx.CopyDir(sftpx.Local{}, dst, sftpx.Local{}, src, nil)
	if err != nil {
		t.Fatalf("a link pointing upwards stopped the copy: %v", err)
	}
	if res.Files != 3 {
		t.Errorf("Files = %d, want the 3 real files", res.Files)
	}
	if res.Skipped != 1 {
		t.Errorf("Skipped = %d, want the one link", res.Skipped)
	}
}

// A link to a file is followed, the same as copying its own row does.
func TestADirectoryCopyFollowsALinkToAFile(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "docs"), filepath.Join(dir, "copy")
	mkTree(t, src)
	if err := os.Symlink("readme.txt", filepath.Join(src, "alias.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	res, err := sftpx.CopyDir(sftpx.Local{}, dst, sftpx.Local{}, src, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Skipped != 0 {
		t.Errorf("Skipped = %d, want the link to a file to have been copied", res.Skipped)
	}
	got, err := os.ReadFile(filepath.Join(dst, "alias.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "top" {
		t.Errorf("alias.txt holds %q, want the bytes of the file it names", got)
	}
}

// Copying onto a directory that is already there merges into it: the names
// that clash are replaced and everything else is left where it is.
func TestADirectoryCopyMergesWithWhatIsAlreadyThere(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "docs"), filepath.Join(dir, "copy")
	mkTree(t, src)
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dst, "readme.txt"), []byte("the old one"))
	write(t, filepath.Join(dst, "theirs.txt"), []byte("not ours"))

	if _, err := sftpx.CopyDir(sftpx.Local{}, dst, sftpx.Local{}, src, nil); err != nil {
		t.Fatal(err)
	}

	if got, _ := os.ReadFile(filepath.Join(dst, "readme.txt")); string(got) != "top" {
		t.Errorf("the clashing file holds %q, want the copied one", got)
	}
	if got, err := os.ReadFile(filepath.Join(dst, "theirs.txt")); err != nil || string(got) != "not ours" {
		t.Errorf("a file that was already there and clashed with nothing was disturbed")
	}
}

// A copy that fails partway says where it stopped, and what it had moved by
// then — the tree is not atomic and pretending otherwise would be a lie.
func TestADirectoryCopyThatFailsSaysWhereItStopped(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "docs"), filepath.Join(dir, "copy")
	mkTree(t, src)

	// Small files go through; the 5000-byte one does not.
	res, err := sftpx.CopyDir(sftpx.Local{}, dst, haltingFS{after: 10}, src, nil)
	if err == nil {
		t.Fatal("the copy was supposed to fail")
	}
	if res.Failed != "guide/deep/note.txt" {
		t.Errorf("Failed = %q, want the file it stopped on", res.Failed)
	}
	// The name is carried beside the error, not baked into it: it comes off
	// the far side, and only the caller knows how to make it safe to draw.
	if got := err.Error(); got == "" || filepath.Base(res.Failed) == got {
		t.Errorf("the error should be the reason alone, got %q", got)
	}
}

// Progress names each file by where it sits in the directory that was picked,
// which is what the browser puts in front of someone watching a long copy.
func TestADirectoryCopyReportsEachFileByItsPlaceInTheTree(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "docs"), filepath.Join(dir, "copy")
	mkTree(t, src)

	seen := map[string]bool{}
	if _, err := sftpx.CopyDir(sftpx.Local{}, dst, sftpx.Local{}, src,
		func(rel string, done, total int64) { seen[rel] = true }); err != nil {
		t.Fatal(err)
	}

	got := make([]string, 0, len(seen))
	for k := range seen {
		got = append(got, k)
	}
	sort.Strings(got)
	want := []string{"guide/deep/note.txt", "guide/intro.md", "readme.txt"}
	if len(got) != len(want) {
		t.Fatalf("reported %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("reported %v, want %v", got, want)
			break
		}
	}
}

// The mode goes on after the directory is filled, not before: a source that
// nobody may write to would otherwise be recreated read-only and refuse the
// first file into it.
func TestADirectoryCopyFillsAReadOnlyDirectoryBeforeSealingIt(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "docs"), filepath.Join(dir, "copy")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(src, "readme.txt"), []byte("top"))
	if err := os.Chmod(src, 0o555); err != nil {
		t.Fatal(err)
	}
	// Both ends are put back before the temporary directory is swept up, which
	// cannot remove what it is not allowed to write into.
	t.Cleanup(func() { os.Chmod(src, 0o755); os.Chmod(dst, 0o755) })

	res, err := sftpx.CopyDir(sftpx.Local{}, dst, sftpx.Local{}, src, nil)
	if err != nil {
		t.Fatalf("a read-only source directory stopped the copy: %v", err)
	}
	if res.Files != 1 {
		t.Fatalf("Files = %d, want 1", res.Files)
	}
	if fi, err := os.Stat(dst); err != nil {
		t.Fatal(err)
	} else if fi.Mode().Perm() != 0o555 {
		t.Errorf("mode = %v, want the source's 0555", fi.Mode().Perm())
	}
}

// Copy refuses a directory with an error a walk can recognise, so CopyDir can
// step over a link to one without mistaking it for a transfer gone wrong.
func TestCopyRefusesADirectoryRecognisably(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "adir")
	if err := os.Mkdir(src, 0o755); err != nil {
		t.Fatal(err)
	}

	err := sftpx.Copy(sftpx.Local{}, filepath.Join(dir, "out"), sftpx.Local{}, src, nil)
	if !errors.Is(err, sftpx.ErrIsDirectory) {
		t.Errorf("err = %v, want it to say it is a directory", err)
	}
	if got := err.Error(); got != "adir is a directory" {
		t.Errorf("err = %q, want it still to read as it did", got)
	}
}
