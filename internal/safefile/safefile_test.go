package safefile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Dotfiles setups commonly link these files into a repository. Renaming over
// the link replaced it with an ordinary file — detaching the config from what
// was tracking it, and leaving the tracked copy without the change. This was
// fixed once in one writer and not the other, which is why there is now one.
func TestALinkIsFollowedToTheFileItNames(t *testing.T) {
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo", "settings.yaml")
	link := filepath.Join(dir, "home", "config.yaml")
	for _, d := range []string{filepath.Dir(repo), filepath.Dir(link)} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(repo, []byte("tracked\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(repo, link); err != nil {
		t.Fatal(err)
	}

	if err := Replace(link, []byte("written\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Error("the link was replaced by an ordinary file")
	}
	body, err := os.ReadFile(repo)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "written\n" {
		t.Errorf("the tracked file holds %q", body)
	}
}

// The mode is the one ssh insists on before it will read a config at all, and
// a file that is not there yet gets its directory made.
func TestItWritesAPrivateFileAndMakesTheDirectory(t *testing.T) {
	p := filepath.Join(t.TempDir(), "deeper", "config.yaml")
	if err := Replace(p, []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode = %o, want 600", perm)
	}
}

// Nothing of omassh's own is left beside the file it wrote.
func TestItLeavesNoScratchFileBehind(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	if err := Replace(p, []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "config.yaml" {
			t.Errorf("left %q beside it", e.Name())
		}
	}
}

// A directory that cannot be written is named, rather than the temporary file
// omassh tried to put in it.
func TestAnUnwritableDirectoryIsNamedRatherThanTheScratchFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ro")
	if err := os.Mkdir(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })

	err := Replace(filepath.Join(dir, "config.yaml"), []byte("x\n"), 0o600)
	if err == nil {
		t.Fatal("writing into an unwritable directory succeeded")
	}
	if strings.Contains(err.Error(), ".omassh-") {
		t.Errorf("names omassh's scratch file at the user: %v", err)
	}
	if !strings.Contains(err.Error(), dir) {
		t.Errorf("does not name the directory: %v", err)
	}
}
