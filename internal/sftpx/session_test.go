package sftpx_test

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gssh "github.com/gliderlabs/ssh"
	"github.com/pkg/sftp"

	"github.com/cuonggt/omassh/internal/sftpx"
	"github.com/cuonggt/omassh/internal/store"
)

// startSFTPServer runs a real SSH server exposing the sftp subsystem, so the
// test drives the same path production does: the ssh binary, the subsystem,
// and the SFTP protocol over its stdio.
func startSFTPServer(t *testing.T, hostKey string) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port

	srv := &gssh.Server{
		PublicKeyHandler: func(gssh.Context, gssh.PublicKey) bool { return true },
		SubsystemHandlers: map[string]gssh.SubsystemHandler{
			"sftp": func(s gssh.Session) {
				server, err := sftp.NewServer(s)
				if err != nil {
					return
				}
				defer server.Close()
				server.Serve()
			},
		},
	}
	if err := gssh.HostKeyFile(hostKey)(srv); err != nil {
		t.Fatalf("host key: %v", err)
	}
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close(); l.Close() })
	return port
}

// genKey writes an unencrypted ed25519 key pair at path and returns that path.
func genKey(t *testing.T, path string) string {
	t.Helper()
	cmd := exec.Command("ssh-keygen", "-t", "ed25519", "-N", "", "-C", "test", "-f", path, "-q")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v: %s", err, out)
	}
	return path
}

// connect opens a session against the test server.
func connect(t *testing.T) (*sftpx.Session, string) {
	t.Helper()
	dir := t.TempDir()
	hostKey := genKey(t, filepath.Join(dir, "host"))
	clientKey := genKey(t, filepath.Join(dir, "client"))
	port := startSFTPServer(t, hostKey)

	host := store.Host{Name: "testsrv", Addr: "127.0.0.1", Port: port,
		User: "tester", Identity: clientKey}

	var sess *sftpx.Session
	var err error
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		sess, err = sftpx.Connect(host,
			"-o", "StrictHostKeyChecking=no",
			"-o", "UserKnownHostsFile=/dev/null",
			"-o", "IdentitiesOnly=yes",
		)
		if err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if sess == nil {
		t.Fatalf("Connect: %v", err)
	}
	t.Cleanup(func() { sess.Close() })
	return sess, dir
}

func TestSFTPOverSSHSubsystem(t *testing.T) {
	sess, local := connect(t)
	work := t.TempDir() // the "remote" side is just another directory here

	t.Run("lists a directory", func(t *testing.T) {
		os.WriteFile(filepath.Join(work, "b.txt"), []byte("bee"), 0o644)
		os.Mkdir(filepath.Join(work, "adir"), 0o755)

		entries, err := sess.List(work)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(entries) != 2 {
			t.Fatalf("List = %+v, want 2 entries", entries)
		}
		// Directories sort first regardless of name.
		if entries[0].Name != "adir" || !entries[0].IsDir {
			t.Errorf("first entry = %+v, want the directory", entries[0])
		}
		if entries[1].Name != "b.txt" || entries[1].Size != 3 {
			t.Errorf("second entry = %+v", entries[1])
		}
	})

	t.Run("mkdir, rename and chmod", func(t *testing.T) {
		p := filepath.Join(work, "made")
		if err := sess.Mkdir(p); err != nil {
			t.Fatalf("Mkdir: %v", err)
		}
		if err := sess.Rename(p, filepath.Join(work, "moved")); err != nil {
			t.Fatalf("Rename: %v", err)
		}
		if _, err := os.Stat(filepath.Join(work, "moved")); err != nil {
			t.Errorf("renamed directory missing: %v", err)
		}
		if err := sess.Chmod(filepath.Join(work, "b.txt"), 0o600); err != nil {
			t.Fatalf("Chmod: %v", err)
		}
		fi, _ := os.Stat(filepath.Join(work, "b.txt"))
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("mode = %o, want 600", fi.Mode().Perm())
		}
	})

	// A real server, because the point is what the protocol answers. pkg/sftp
	// maps only the two status codes with os equivalents, so a name already
	// taken came back as `sftp: "Failure" (SSH_FX_FAILURE)` — a protocol
	// constant, shown to whoever had just typed the name.
	t.Run("says a name is taken without naming a protocol constant", func(t *testing.T) {
		dest := filepath.Join(work, "taken")
		if err := os.Mkdir(dest, 0o755); err != nil {
			t.Fatal(err)
		}
		raw := sess.Mkdir(dest)
		if raw == nil {
			t.Fatal("making a directory that is already there succeeded")
		}
		got := sftpx.Problem(sess, dest, "taken", raw).Error()
		if !strings.Contains(got, "taken is already there") {
			t.Errorf("says %q, want that the name is taken", got)
		}
		for _, leak := range []string{"SSH_FX", "sftp:", "Failure"} {
			if strings.Contains(got, leak) {
				t.Errorf("leaks %q at the user: %s", leak, got)
			}
		}
	})

	// Renaming into a directory that is not there answered "file does not
	// exist", which names the wrong subject: the file being renamed is fine,
	// and it is the destination's directory that is missing.
	t.Run("says which directory is missing", func(t *testing.T) {
		src := filepath.Join(work, "movable.txt")
		if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		dest := filepath.Join(work, "nowhere", "moved.txt")
		raw := sess.Rename(src, dest)
		if raw == nil {
			t.Fatal("renaming into a directory that is not there succeeded")
		}
		got := sftpx.Problem(sess, dest, "moved.txt", raw).Error()
		if !strings.Contains(got, "there is no nowhere") {
			t.Errorf("says %q, want it to name the missing directory", got)
		}
		if strings.Contains(got, "does not exist") {
			t.Errorf("still blames the file being renamed: %s", got)
		}
		// The message is one line inside a dialog. Spelling the directory out
		// in full pushed the name it exists to say past the right-hand edge.
		if strings.Contains(got, work) {
			t.Errorf("names the directory by its whole path, which will not fit: %s", got)
		}
	})

	t.Run("downloads with progress", func(t *testing.T) {
		body := strings.Repeat("payload", 5000) // big enough for several chunks
		src := filepath.Join(work, "big.bin")
		os.WriteFile(src, []byte(body), 0o644)

		dst := filepath.Join(local, "downloaded.bin")
		var calls int
		var lastDone, lastTotal int64
		err := sftpx.Copy(sftpx.Local{}, dst, sess, src, func(done, total int64) {
			calls++
			lastDone, lastTotal = done, total
		})
		if err != nil {
			t.Fatalf("Copy down: %v", err)
		}
		got, _ := os.ReadFile(dst)
		if string(got) != body {
			t.Errorf("downloaded %d bytes, want %d", len(got), len(body))
		}
		if calls == 0 {
			t.Error("no progress reported")
		}
		if lastDone != int64(len(body)) || lastTotal != int64(len(body)) {
			t.Errorf("final progress = %d/%d, want %d/%d", lastDone, lastTotal, len(body), len(body))
		}
	})

	t.Run("uploads", func(t *testing.T) {
		src := filepath.Join(local, "upload.txt")
		os.WriteFile(src, []byte("upward"), 0o644)

		dst := filepath.Join(work, "uploaded.txt")
		if err := sftpx.Copy(sess, dst, sftpx.Local{}, src, nil); err != nil {
			t.Fatalf("Copy up: %v", err)
		}
		got, err := os.ReadFile(dst)
		if err != nil || string(got) != "upward" {
			t.Errorf("uploaded content = %q, %v", got, err)
		}
	})

	t.Run("refuses to copy a directory", func(t *testing.T) {
		err := sftpx.Copy(sftpx.Local{}, filepath.Join(local, "x"), sess, filepath.Join(work, "adir"), nil)
		if err == nil {
			t.Error("Copy accepted a directory")
		}
	})

	// SFTP has no recursive delete, so the walk is Omassh's own.
	t.Run("removes a directory tree", func(t *testing.T) {
		root := filepath.Join(work, "tree")
		os.MkdirAll(filepath.Join(root, "a", "b"), 0o755)
		os.WriteFile(filepath.Join(root, "a", "b", "deep.txt"), []byte("x"), 0o644)
		os.WriteFile(filepath.Join(root, "top.txt"), []byte("y"), 0o644)

		if err := sess.Remove(root); err != nil {
			t.Fatalf("Remove: %v", err)
		}
		if _, err := os.Stat(root); !os.IsNotExist(err) {
			t.Errorf("tree still present: %v", err)
		}
	})

	t.Run("reports a home directory", func(t *testing.T) {
		if sess.Home() == "" {
			t.Error("Home() is empty")
		}
	})
}

// A failure to connect must explain itself in ssh's words, not as "EOF".
func TestConnectErrorSurfacesSSHDiagnostic(t *testing.T) {
	host := store.Host{Name: "nope", Addr: "127.0.0.1", Port: 1, User: "x"}
	_, err := sftpx.Connect(host, "-o", "ConnectTimeout=3")
	if err == nil {
		t.Fatal("Connect succeeded against a dead port")
	}
	if strings.Contains(err.Error(), "EOF") || err.Error() == "" {
		t.Errorf("err = %q, want ssh's own diagnostic", err)
	}
	t.Logf("reported: %v", err)
}

func TestLocalFS(t *testing.T) {
	dir := t.TempDir()
	l := sftpx.Local{}

	if err := l.Mkdir(filepath.Join(dir, "sub")); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	os.WriteFile(filepath.Join(dir, "file.txt"), []byte("hi"), 0o644)

	entries, err := l.List(dir)
	if err != nil || len(entries) != 2 {
		t.Fatalf("List = %+v, %v", entries, err)
	}
	if !entries[0].IsDir {
		t.Error("directories should sort first")
	}
	if got := l.Join("/a", "b"); got != filepath.Join("/a", "b") {
		t.Errorf("Join = %q", got)
	}
	if got := l.Parent("/a/b"); got != filepath.Dir("/a/b") {
		t.Errorf("Parent = %q", got)
	}
	if l.Home() == "" {
		t.Error("Home() is empty")
	}
	if l.Label() != "local" {
		t.Errorf("Label = %q", l.Label())
	}
	fmt.Fprint(os.Stderr, "")
}

// SSH_FX_FAILURE is the protocol's "no, and I will not say why". Once the
// name and its directory have both been asked about and neither explains it,
// that is the whole of what is known — and saying so beats printing the
// constant.
func TestARefusalWithNoReasonSaysThatRatherThanTheConstant(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "absent") // its parent is there, it is not

	raw := &sftp.StatusError{Code: 4} // SSH_FX_FAILURE
	got := sftpx.Problem(sftpx.Local{}, dest, "absent", raw).Error()

	if !strings.Contains(got, "the server refused it") {
		t.Errorf("says %q, want that the server refused without a reason", got)
	}
	for _, leak := range []string{"SSH_FX", "sftp:"} {
		if strings.Contains(got, leak) {
			t.Errorf("leaks %q at the user: %s", leak, got)
		}
	}
}

// Nothing wrong stays nothing wrong: Problem is on every one of these paths,
// so a success must pass straight through it.
func TestProblemLeavesSuccessAlone(t *testing.T) {
	if err := sftpx.Problem(sftpx.Local{}, t.TempDir(), "x", nil); err != nil {
		t.Errorf("Problem turned success into %v", err)
	}
}
