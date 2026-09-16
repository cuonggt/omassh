package smoke

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gssh "github.com/gliderlabs/ssh"
)

// run is a command and whatever it said, for the setup steps whose only
// interesting outcome is whether they worked.
func run(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	return string(out), err
}

// tmuxAvailable reports whether these tests can run at all.
func tmuxAvailable() bool {
	_, err := exec.LookPath("tmux")
	return err == nil
}

// build compiles omassh as it would be shipped, and returns the path to it.
//
// The binary rather than the packages: everything these tests are for lives in
// the wiring between them, and a test that imported the packages instead would
// be the kind of test that already passed while the program did not work.
func build(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "omassh")
	cmd := exec.Command("go", "build", "-o", bin, "github.com/cuonggt/omassh/cmd/omassh")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building omassh: %v\n%s", err, out)
	}
	return bin
}

// server is a throwaway SSH server on loopback that gives every session a
// shell, so a connection can be told from a refusal by what comes back.
type server struct {
	addr    string
	hostKey string
}

func startServer(t *testing.T, dir string) server {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	hostKey := filepath.Join(dir, "hostkey")
	if out, err := exec.Command("ssh-keygen", "-t", "ed25519", "-f", hostKey, "-N", "", "-q").CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v\n%s", err, out)
	}

	srv := &gssh.Server{
		// Any key: what is being tested is that omassh offered one at all, and
		// which one, not whether this server would have accepted it.
		PublicKeyHandler: func(gssh.Context, gssh.PublicKey) bool { return true },
		Handler: func(s gssh.Session) {
			// A remote command — which is what a snippet run is — is run and
			// answered, so the run ends with a result rather than with a
			// session nobody closes. Without this the banner below would be
			// the answer to every script alike, and to none of them.
			if script := s.RawCommand(); script != "" {
				cmd := exec.Command("sh", "-c", script)
				cmd.Stdout, cmd.Stderr = s, s.Stderr()
				var ee *exec.ExitError
				switch err := cmd.Run(); {
				case err == nil:
					s.Exit(0)
				case errors.As(err, &ee):
					s.Exit(ee.ExitCode())
				default:
					s.Exit(1)
				}
				return
			}
			// A banner rather than a shell: it needs no pty, arrives the
			// moment the session opens, and is the one thing the screen can be
			// searched for to know a connection was really made.
			fmt.Fprintln(s, connected)
			// Held open so the pane keeps showing it rather than reporting a
			// session that ended before anyone looked.
			<-s.Context().Done()
		},
	}
	if err := srv.SetOption(gssh.HostKeyFile(hostKey)); err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })

	return server{addr: ln.Addr().String(), hostKey: hostKey}
}

// connected is what the far side says, and what the screen is searched for.
const connected = "SMOKE-TEST-CONNECTED"

// pane is a terminal with omassh running in it, driven through tmux.
//
// Through tmux rather than a bare pty because it gives both halves at once: a
// real controlling terminal, which is what the keychain bug needed to appear,
// and capture-pane, which hands back what is on screen as text rather than as
// escape sequences to be parsed.
type pane struct {
	t   *testing.T
	dir string
}

func start(t *testing.T, bin, dir string, args ...string) *pane {
	return startWith(t, bin, dir, nil, args...)
}

// startWith is start with extra environment, for the programs omassh shells
// out to — $EDITOR being the one a snippet needs.
func startWith(t *testing.T, bin, dir string, env []string, args ...string) *pane {
	t.Helper()
	p := &pane{t: t, dir: dir}

	// The known_hosts is the test's own: connecting must not write into the
	// one belonging to whoever is running the suite.
	full := append([]string{
		bin,
		"-db", filepath.Join(dir, "omassh.db"),
		"-config", filepath.Join(dir, "config.yaml"),
		"-o", "UserKnownHostsFile=" + filepath.Join(dir, "known_hosts"),
		"-o", "StrictHostKeyChecking=accept-new",
		"-o", "IdentitiesOnly=yes",
	}, args...)

	cmd := exec.Command("tmux", "-L", driveSocket, "new-session", "-d", "-s", "v",
		"-x", "160", "-y", "44",
		strings.Join(append([]string{"OMASSH_TMUX_SOCKET=" + socket}, env...), " ")+
			" "+strings.Join(full, " ")+"; sleep 300")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("starting omassh in tmux: %v\n%s", err, out)
	}
	t.Cleanup(func() {
		exec.Command("tmux", "-L", driveSocket, "kill-server").Run()
		exec.Command("tmux", "-L", socket, "kill-server").Run()
	})
	p.waitFor("Groups")
	return p
}

// send presses keys, as tmux names them.
func (p *pane) send(keys ...string) {
	p.t.Helper()
	for _, k := range keys {
		args := []string{"-L", driveSocket, "send-keys", "-t", "v"}
		// Literal, so text with a space or a dash in it is typed rather than
		// read as a key name or a flag.
		if len(k) > 1 && !namedKey(k) {
			args = append(args, "-l")
		}
		args = append(args, k)
		if out, err := exec.Command("tmux", args...).CombinedOutput(); err != nil {
			p.t.Fatalf("send-keys %q: %v\n%s", k, err, out)
		}
		time.Sleep(120 * time.Millisecond)
	}
}

func namedKey(k string) bool {
	switch k {
	case "Enter", "Escape", "Tab", "Down", "Up", "Left", "Right", "BSpace", "Space":
		return true
	}
	// tmux spells a control key C-x, and typing that literally is how ctrl+e
	// arrived in a form as three characters.
	return len(k) == 3 && strings.HasPrefix(k, "C-")
}

// screen is what is on the terminal now, with tabs expanded so a column of
// spaces reads as spaces.
func (p *pane) screen() string {
	p.t.Helper()
	out, err := exec.Command("tmux", "-L", driveSocket, "capture-pane", "-p", "-t", "v").CombinedOutput()
	if err != nil {
		p.t.Fatalf("capture-pane: %v\n%s", err, out)
	}
	return strings.ReplaceAll(string(out), "\t", " ")
}

// waitTimeout is how long a step waits for the screen to say something.
//
// Generous, because this drives a built binary through a real terminal on
// whatever machine is running it, and every wait is bounded by the slowest of
// them rather than the usual one. At twenty seconds a shared CI runner failed
// to reach an edit form in time — a test that goes red for being on a busy
// machine teaches nothing, and teaches people to disbelieve it.
const waitTimeout = 45 * time.Second

// waitFor blocks until the screen says something, rather than sleeping for a
// guess. Connecting takes as long as it takes, and a fixed wait is either
// slower than it needs to be or flaky on a loaded machine.
func (p *pane) waitFor(want string) string {
	p.t.Helper()
	deadline := time.Now().Add(waitTimeout)
	var last string
	for time.Now().Before(deadline) {
		last = p.screen()
		if strings.Contains(last, want) {
			return last
		}
		time.Sleep(150 * time.Millisecond)
	}
	p.t.Fatalf("waited %s for %q; the screen says:\n%s", waitTimeout, want, last)
	return ""
}

// mustNotSay fails if something is on screen that should never be.
func (p *pane) mustNotSay(bad string) {
	p.t.Helper()
	if s := p.screen(); strings.Contains(s, bad) {
		p.t.Errorf("the screen shows %q:\n%s", bad, s)
	}
}

// genKey writes an unencrypted ed25519 key pair under dir and returns the
// private key's path, for a host seed that needs a key to offer.
//
// The smoke server accepts any key, so what a test proves by pointing a host
// at one is not that this key is right but that ssh has one at all. A host
// left without said "(agent)" and leaned on whatever key happened to sit in
// the developer's ~/.ssh — present on the Mac, absent in the container, so a
// run that worked here failed there with "Permission denied (publickey)". A
// key of the test's own is the same key wherever it runs.
func genKey(t *testing.T, dir string) string {
	t.Helper()
	key := filepath.Join(dir, "id_test")
	if out, err := exec.Command("ssh-keygen", "-t", "ed25519", "-f", key, "-N", "", "-q").CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v\n%s", err, out)
	}
	return key
}

// seed writes a host list through omassh's own import, so the database is made
// the way a person would make it rather than by reaching past the program.
func seed(t *testing.T, bin, dir, yaml string) {
	t.Helper()
	path := filepath.Join(dir, "seed.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "import", "-db", filepath.Join(dir, "omassh.db"), path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("seeding: %v\n%s", err, out)
	}
}
