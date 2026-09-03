package forward_test

import (
	"fmt"
	"io"
	"net"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	gssh "github.com/gliderlabs/ssh"

	"github.com/cuonggt/omassh/internal/forward"
	"github.com/cuonggt/omassh/internal/keys"
	"github.com/cuonggt/omassh/internal/sshx"
	"github.com/cuonggt/omassh/internal/store"
)

// freePort returns a port nothing is listening on. There is an inherent race
// between releasing it and ssh claiming it, which is tolerable in a test.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// startEchoServer is the thing on the far side of the tunnel.
func startEchoServer(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() { defer c.Close(); io.Copy(c, c) }()
		}
	}()
	return l.Addr().(*net.TCPAddr).Port
}

// startSSHServer runs a real SSH server in-process that permits local port
// forwarding, so the tunnel under test is carried by genuine ssh on both ends.
func startSSHServer(t *testing.T, hostKey string) int {
	t.Helper()
	port := freePort(t)
	srv := &gssh.Server{
		Addr:                        fmt.Sprintf("127.0.0.1:%d", port),
		PublicKeyHandler:            func(gssh.Context, gssh.PublicKey) bool { return true },
		LocalPortForwardingCallback: func(gssh.Context, string, uint32) bool { return true },
		ChannelHandlers: map[string]gssh.ChannelHandler{
			"direct-tcpip": gssh.DirectTCPIPHandler,
			"session":      gssh.DefaultSessionHandler,
		},
		Handler: func(s gssh.Session) { s.Exit(0) },
	}
	if err := gssh.HostKeyFile(hostKey)(srv); err != nil {
		t.Fatalf("host key: %v", err)
	}
	go srv.ListenAndServe()
	t.Cleanup(func() { srv.Close() })

	// Wait for it to accept before handing the port to ssh.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if c, err := net.DialTimeout("tcp", srv.Addr, 200*time.Millisecond); err == nil {
			c.Close()
			return port
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("ssh test server never came up")
	return 0
}

// TestTunnelCarriesTraffic proves the whole chain: the argv Omassh builds, the
// real ssh binary, a real SSH server, and the supervisor holding it open.
func TestTunnelCarriesTraffic(t *testing.T) {
	dir := t.TempDir()
	hostKey, err := keys.Generate(filepath.Join(dir, "host"), "host", "")
	if err != nil {
		t.Fatal(err)
	}
	clientKey, err := keys.Generate(filepath.Join(dir, "client"), "client", "")
	if err != nil {
		t.Fatal(err)
	}

	sshPort := startSSHServer(t, hostKey.Path)
	echoPort := startEchoServer(t)
	localPort := freePort(t)

	host := store.Host{
		Name: "testsrv", Addr: "127.0.0.1", Port: sshPort,
		User: "tester", Identity: clientKey.Path,
	}
	rule := store.Forward{
		ID: "t1", HostKey: "h", Kind: store.ForwardLocal,
		BindAddr: "127.0.0.1", ListenPort: localPort,
		TargetHost: "127.0.0.1", TargetPort: echoPort,
	}

	sup := forward.New(func(f store.Forward) (*exec.Cmd, error) {
		// Real ForwardArgs, plus the host-key relaxations a throwaway server
		// needs. Everything else is exactly what Omassh runs in production.
		args := sshx.ForwardArgs(host, f,
			"-o", "StrictHostKeyChecking=no",
			"-o", "UserKnownHostsFile=/dev/null",
			"-o", "IdentitiesOnly=yes",
			"-o", "BatchMode=yes",
		)
		return exec.Command("ssh", args...), nil
	})

	if err := sup.Start(rule); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(sup.StopAll)

	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(localPort))
	var conn net.Conn
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if st := sup.Status(rule.ID); st.State == forward.Failed {
			t.Fatalf("tunnel failed: %s", st.Err)
		}
		if conn, err = net.DialTimeout("tcp", addr, 300*time.Millisecond); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if conn == nil {
		t.Fatalf("nothing accepted on %s; status %+v, stderr %q",
			addr, sup.Status(rule.ID), sup.LastError(rule.ID))
	}
	defer conn.Close()

	// Bytes must survive the round trip through ssh to the echo server.
	const msg = "omassh tunnel check"
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write([]byte(msg)); err != nil {
		t.Fatalf("write through tunnel: %v", err)
	}
	buf := make([]byte, len(msg))
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("read through tunnel: %v", err)
	}
	if string(buf) != msg {
		t.Fatalf("got %q through the tunnel, want %q", buf, msg)
	}
	conn.Close()

	// Stopping must actually close the listener, not just relabel the rule.
	sup.Stop(rule.ID)
	closed := false
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		if c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond); err != nil {
			closed = true
			break
		} else {
			c.Close()
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !closed {
		t.Errorf("%s still accepts connections after Stop", addr)
	}
}
