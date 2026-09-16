package smoke

import (
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gssh "github.com/gliderlabs/ssh"

	"github.com/cuonggt/omassh/internal/secret"
	"github.com/cuonggt/omassh/internal/store"
)

// passwordServer takes a password and nothing else, so a connection to it
// proves the password arrived rather than a key happening to work.
func startPasswordServer(t *testing.T, dir, user, password string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	hostKey := filepath.Join(dir, "pw_hostkey")
	if out, err := run("ssh-keygen", "-t", "ed25519", "-f", hostKey, "-N", "", "-q"); err != nil {
		t.Fatalf("ssh-keygen: %v\n%s", err, out)
	}
	srv := &gssh.Server{
		// No public-key handler at all: there is no way in but the password.
		PasswordHandler: func(ctx gssh.Context, given string) bool {
			return ctx.User() == user && given == password
		},
		Handler: func(s gssh.Session) {
			fmt.Fprintln(s, connected)
			<-s.Context().Done()
		},
		SubsystemHandlers: map[string]gssh.SubsystemHandler{
			"sftp": func(s gssh.Session) { io.Copy(io.Discard, s) },
		},
	}
	if err := srv.SetOption(gssh.HostKeyFile(hostKey)); err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return ln.Addr().String()
}

// A password credential, made in the interface and used to connect.
//
// Opt-in, and for one reason: this is the only test in the tree that cannot be
// isolated. The password has to go into the keychain of whoever runs it,
// because that is the thing being tested — security(1) behaves differently
// when it has a terminal, and a fake would behave the same either way, which
// is exactly how this shipped broken.
//
//	OMASSH_KEYCHAIN_TEST=1 go test ./internal/smoke -run Password
//
// It is also the test that would have caught both of the bugs this package
// exists for: the keychain write hanging on a terminal it found by itself, and
// the askpass environment never reaching the ssh that tmux runs.
func TestAPasswordCredentialConnectsThroughTheKeychain(t *testing.T) {
	if os.Getenv(secret.EnvLive) == "" {
		t.Skipf("set %s=1 to run this; it writes a password into your keychain", secret.EnvLive)
	}
	if !tmuxAvailable() {
		t.Skip("no tmux; this drives the interface through one")
	}
	dir := t.TempDir()
	bin := build(t)
	const user, password = "admin", "smoke-trial-password"
	addr := startPasswordServer(t, dir, user, password)
	host, port, _ := strings.Cut(addr, ":")

	seed(t, bin, dir, fmt.Sprintf(
		"version: 1\nhosts:\n  - name: box\n    addr: %s\n    port: %s\n", host, port))

	// Whatever this test puts in the keychain comes out again, however it
	// ends. The id is minted by omassh, so it is read back out of the store
	// rather than guessed at.
	db := filepath.Join(dir, "omassh.db")
	t.Cleanup(func() {
		st, err := store.Open(db)
		if err != nil {
			return
		}
		defer st.Close()
		creds, _ := st.Credentials()
		ks, err := secret.Open()
		if err != nil {
			return
		}
		for _, c := range creds {
			ks.Delete(c.ID)
		}
	})

	p := start(t, bin, dir)
	p.send("C")
	p.waitFor("no credentials yet")
	p.send("n")
	p.waitFor("New credential")

	// Name, then the kind picker down to password, then user and password.
	p.send("Trial", "Tab", "Down", "Down", "Down", "Enter")
	p.waitFor("Password")
	p.send("Tab", user, "Tab", password, "Enter")
	p.waitFor("Trial")

	// The prompt security(1) writes when it finds a terminal of its own. If it
	// is on the screen the write is hanging, and nothing below will happen.
	p.mustNotSay("password data for new item")
	p.mustNotSay("no password in the keychain")

	// The browser's own keys coming back, not the host's name arriving: the
	// name is behind the dialog the whole time, and an absence can be
	// satisfied by a half-drawn frame.
	p.send("Escape")
	p.waitGone("Credentials")
	p.waitFor("enter connect")
	p.send("2", "e")
	p.waitFor("Edit box")
	p.send("Tab", "Tab", "Tab", "Down", "Down", "Enter")
	p.waitFor("from credential: " + user)
	p.send("Enter")
	p.waitFor(user + "@" + host)

	// The connection itself, through tmux — which is where the environment
	// carrying the credential's id was being dropped.
	p.send("t")
	p.waitFor(connected)
	// ssh only asks when it has not been answered.
	p.mustNotSay("password:")
}
