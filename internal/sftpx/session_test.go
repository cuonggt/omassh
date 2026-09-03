package sftpx_test

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gssh "github.com/gliderlabs/ssh"
	"github.com/pkg/sftp"

	"github.com/cuonggt/omassh/internal/keys"
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

// connect opens a session against the test server.
func connect(t *testing.T) (*sftpx.Session, string) {
	t.Helper()
	dir := t.TempDir()
	hostKey, err := keys.Generate(filepath.Join(dir, "host"), "host", "")
	if err != nil {
		t.Fatal(err)
	}
	clientKey, err := keys.Generate(filepath.Join(dir, "client"), "client", "")
	if err != nil {
		t.Fatal(err)
	}
	port := startSFTPServer(t, hostKey.Path)

	host := store.Host{Name: "testsrv", Addr: "127.0.0.1", Port: port,
		User: "tester", Identity: clientKey.Path}

	var sess *sftpx.Session
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
