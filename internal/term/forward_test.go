package term_test

import (
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	gssh "github.com/gliderlabs/ssh"

	"github.com/cuonggt/omassh/internal/sshx"
	"github.com/cuonggt/omassh/internal/store"
	"github.com/cuonggt/omassh/internal/term"
)

// startGreeter is the thing on the far side of a tunnel: a plain TCP server
// that says one thing and hangs up. Nothing about it involves ssh, which is
// what makes reading its greeting through a forwarded port mean something.
func startGreeter(t *testing.T, greeting string) int {
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
			io.WriteString(c, greeting)
			c.Close()
		}
	}()
	return l.Addr().(*net.TCPAddr).Port
}

// startForwardingServer is an SSH server that opens direct-tcpip channels,
// which is the one thing a local forward needs of a host.
func startForwardingServer(t *testing.T, hostKey string, accept bool) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &gssh.Server{
		PublicKeyHandler:            func(gssh.Context, gssh.PublicKey) bool { return accept },
		LocalPortForwardingCallback: func(gssh.Context, string, uint32) bool { return true },
		ChannelHandlers: map[string]gssh.ChannelHandler{
			"direct-tcpip": gssh.DirectTCPIPHandler,
			"session":      gssh.DefaultSessionHandler,
		},
		Handler: func(s gssh.Session) { s.Exit(0) },
	}
	if err := gssh.HostKeyFile(hostKey)(srv); err != nil {
		t.Fatal(err)
	}
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close(); l.Close() })
	return l.Addr().(*net.TCPAddr).Port
}

// forwardingHost is a host whose server will carry a tunnel, or refuse the key.
func forwardingHost(t *testing.T, accept bool) store.Host {
	t.Helper()
	dir := t.TempDir()
	hk := genKey(t, filepath.Join(dir, "host"))
	ck := genKey(t, filepath.Join(dir, "client"))
	port := startForwardingServer(t, hk, accept)
	return store.Host{ID: "fwd-host", Name: "tunnelbox", Addr: "127.0.0.1", Port: port,
		User: "tester", Identity: ck}
}

// freePort is a port nothing is listening on, for ssh to bind.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port
}

// readThrough dials a port and returns what answers, retrying until the
// deadline: ssh binds its listener a moment after the connection is up.
func readThrough(t *testing.T, port int, d time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(d)
	var last error
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), time.Second)
		if err != nil {
			last = err
			time.Sleep(100 * time.Millisecond)
			continue
		}
		b, err := io.ReadAll(c)
		c.Close()
		if err == nil && len(b) > 0 {
			return string(b)
		}
		last = err
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("nothing came back through port %d: %v", port, last)
	return ""
}

func forwardOptions(t *testing.T) {
	t.Helper()
	sshx.SetGlobalOptions([]string{
		"StrictHostKeyChecking=no", "UserKnownHostsFile=/dev/null", "IdentitiesOnly=yes",
	})
	t.Cleanup(func() { sshx.SetGlobalOptions(nil) })
}

// The whole point of a forward, end to end: bytes written on a local port come
// out of a server that only the host can reach, and stopping it puts the port
// back.
func TestForwardCarriesTraffic(t *testing.T) {
	if !term.TmuxAvailable() {
		t.Skip("tmux not installed; forwards need it to outlive the interface")
	}
	killServer(t)
	forwardOptions(t)

	const greeting = "hello from the far side"
	target := startGreeter(t, greeting)
	h := forwardingHost(t, true)

	local := freePort(t)
	f := store.Forward{
		ID: "fwd-carries", HostID: h.ID, Kind: store.ForwardLocal,
		ListenPort: local, Dest: "127.0.0.1", DestPort: target,
	}
	if err := term.StartForward(f, sshx.ForwardArgs(h, f)); err != nil {
		t.Fatalf("StartForward: %v", err)
	}
	t.Cleanup(func() { term.StopForward(f) })

	if got := readThrough(t, local, 15*time.Second); got != greeting {
		t.Errorf("through the tunnel: %q, want %q", got, greeting)
	}

	states, err := term.ForwardStates()
	if err != nil {
		t.Fatalf("ForwardStates: %v", err)
	}
	if st, ok := states[term.ForwardSessionName(f)]; !ok || !st.Running {
		t.Errorf("a tunnel carrying traffic is reported as %+v, present=%v", st, ok)
	}

	// Stopping ends the tunnel, and with it the claim to the port.
	if err := term.StopForward(f); err != nil {
		t.Fatalf("StopForward: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := sshx.ListenAvailable(f); err == nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Errorf("port %d is still held after stopping the forward", local)
}

// A tunnel that does not come up has to say why. The session is kept after ssh
// exits for exactly this: without it the failure would be indistinguishable
// from a tunnel nobody started.
func TestAFailedForwardSaysWhy(t *testing.T) {
	if !term.TmuxAvailable() {
		t.Skip("tmux not installed")
	}
	killServer(t)
	forwardOptions(t)

	h := forwardingHost(t, false) // the server refuses the key
	f := store.Forward{
		ID: "fwd-refused", HostID: h.ID, Kind: store.ForwardLocal,
		ListenPort: freePort(t), Dest: "127.0.0.1", DestPort: 80,
	}
	t.Cleanup(func() { term.StopForward(f) })

	err := term.StartForward(f, sshx.ForwardArgs(h, f))
	if err == nil {
		t.Fatal("a forward whose key was refused reported success")
	}
	if !strings.Contains(err.Error(), "Permission denied") {
		t.Errorf("the reason is not what ssh said: %v", err)
	}

	// And the stopped tunnel is still there to be asked about, rather than
	// having vanished along with its reason.
	states, err := term.ForwardStates()
	if err != nil {
		t.Fatalf("ForwardStates: %v", err)
	}
	st, ok := states[term.ForwardSessionName(f)]
	if !ok {
		t.Fatal("the stopped tunnel left nothing behind to explain itself")
	}
	if st.Running {
		t.Error("a tunnel that failed is reported as running")
	}
	if st.Exit == 0 {
		t.Error("a tunnel that failed exited 0")
	}
	if reason := term.ForwardReason(term.ForwardSessionName(f)); !strings.Contains(reason, "Permission denied") {
		t.Errorf("ForwardReason = %q", reason)
	}
}

// Starting a rule that already has a stopped session must not trip over it:
// tmux refuses a duplicate name, and the corpse of a previous attempt is
// exactly what the retry is a response to.
func TestStartingOverAStoppedTunnel(t *testing.T) {
	if !term.TmuxAvailable() {
		t.Skip("tmux not installed")
	}
	killServer(t)
	forwardOptions(t)

	const greeting = "second time lucky"
	target := startGreeter(t, greeting)
	refuse := forwardingHost(t, false)
	local := freePort(t)

	f := store.Forward{
		ID: "fwd-retry", HostID: refuse.ID, Kind: store.ForwardLocal,
		ListenPort: local, Dest: "127.0.0.1", DestPort: target,
	}
	t.Cleanup(func() { term.StopForward(f) })
	if err := term.StartForward(f, sshx.ForwardArgs(refuse, f)); err == nil {
		t.Fatal("the refusing server accepted the connection")
	}

	// Now the same rule against a server that will have it.
	accept := forwardingHost(t, true)
	if err := term.StartForward(f, sshx.ForwardArgs(accept, f)); err != nil {
		t.Fatalf("starting over a stopped tunnel: %v", err)
	}
	if got := readThrough(t, local, 15*time.Second); got != greeting {
		t.Errorf("through the tunnel: %q, want %q", got, greeting)
	}
}

// The session name is what stopping, listing and restarting all go through, so
// it has to be inside the namespace those are gated on — and it must not
// collide with the session a host's own shell runs in.
func TestForwardSessionNaming(t *testing.T) {
	f := store.Forward{ID: "abc123"}
	name := term.ForwardSessionName(f)

	if name != term.ForwardSessionName(f) {
		t.Error("the name is not stable")
	}
	if term.ForwardSessionName(store.Forward{ID: "def456"}) == name {
		t.Error("two rules share a session")
	}
	// KillSession guards the omassh namespace; a name outside it could never
	// be stopped.
	if err := term.KillSession(name); err != nil && strings.Contains(err.Error(), "refusing") {
		t.Errorf("KillSession will not accept a forward's own session: %v", err)
	}
	// A host's session and a rule's must not be the same string, or stopping
	// one would end the other.
	if term.SessionName(store.Host{ID: "abc123", Name: "fwd"}) == name {
		t.Error("a host's session collides with a forward's")
	}
}

// On a machine where tmux has never run there is no socket file, and tmux says
// so differently from the way it reports a socket with nobody behind it.
// Reading only the second spelling made every one of these report a failure,
// so the first forward ever started could not be — it is stopped before it is
// started, and that stop was the error.
func TestNothingRunningIsNotAFailure(t *testing.T) {
	if !term.TmuxAvailable() {
		t.Skip("tmux not installed")
	}
	was := os.Getenv(term.SocketEnv)
	t.Setenv(term.SocketEnv, "omassh-never-started-"+strconv.Itoa(os.Getpid()))
	t.Cleanup(func() { os.Setenv(term.SocketEnv, was) })

	if live, err := term.LiveSessions(); err != nil || len(live) != 0 {
		t.Errorf("LiveSessions on a socket that was never made = %+v, %v", live, err)
	}
	if states, err := term.ForwardStates(); err != nil || len(states) != 0 {
		t.Errorf("ForwardStates on a socket that was never made = %+v, %v", states, err)
	}
	if err := term.StopForward(store.Forward{ID: "nothing"}); err != nil {
		t.Errorf("stopping a tunnel that was never started: %v", err)
	}
}

// Editing a rule reaches nothing already running: the tunnel is named by the
// rule's id, so it goes on being found and reported as up while carrying the
// route it was started with. The fingerprint is what tells the two apart.
func TestARunningTunnelSaysWhatItIsCarrying(t *testing.T) {
	if !term.TmuxAvailable() {
		t.Skip("tmux not installed")
	}
	killServer(t)
	forwardOptions(t)

	const greeting = "the original destination"
	target := startGreeter(t, greeting)
	h := forwardingHost(t, true)

	f := store.Forward{
		ID: "fwd-stale", HostID: h.ID, Kind: store.ForwardLocal,
		ListenPort: freePort(t), Dest: "127.0.0.1", DestPort: target,
	}
	args := sshx.ForwardArgs(h, f)
	if err := term.StartForward(f, args); err != nil {
		t.Fatalf("StartForward: %v", err)
	}
	t.Cleanup(func() { term.StopForward(f) })

	states, err := term.ForwardStates()
	if err != nil {
		t.Fatal(err)
	}
	st, ok := states[term.ForwardSessionName(f)]
	if !ok || !st.Running {
		t.Fatalf("the tunnel is not running: %+v", st)
	}
	if st.Args != term.ForwardFingerprint(args) {
		t.Errorf("the tunnel reports %q, want the fingerprint of what it was started with (%q)",
			st.Args, term.ForwardFingerprint(args))
	}

	// Now the rule says something else. The tunnel has not changed, and must
	// not claim to have.
	edited := f
	edited.DestPort = target + 1
	if st.Args == term.ForwardFingerprint(sshx.ForwardArgs(h, edited)) {
		t.Error("a rule pointed somewhere else fingerprints the same as the tunnel carrying the old one")
	}
}

// Restarting a running tunnel is how a rule that changed under it is made to
// agree again, so it has to work while the old one is still up. Checking the
// port before stopping that one failed every time on the port its own
// predecessor was holding — the one case restarting exists for.
func TestRestartingARunningTunnelMovesIt(t *testing.T) {
	if !term.TmuxAvailable() {
		t.Skip("tmux not installed")
	}
	killServer(t)
	forwardOptions(t)

	first := startGreeter(t, "the first destination")
	second := startGreeter(t, "the second destination")
	h := forwardingHost(t, true)
	local := freePort(t)

	f := store.Forward{
		ID: "fwd-moves", HostID: h.ID, Kind: store.ForwardLocal,
		ListenPort: local, Dest: "127.0.0.1", DestPort: first,
	}
	if err := term.StartForward(f, sshx.ForwardArgs(h, f)); err != nil {
		t.Fatalf("StartForward: %v", err)
	}
	t.Cleanup(func() { term.StopForward(f) })
	if got := readThrough(t, local, 15*time.Second); !strings.Contains(got, "first") {
		t.Fatalf("through the tunnel: %q, want the first destination", got)
	}

	// The rule now points elsewhere, and the tunnel is still up on the old one.
	moved := f
	moved.DestPort = second
	if err := term.StartForward(moved, sshx.ForwardArgs(h, moved)); err != nil {
		t.Fatalf("restarting a running tunnel: %v", err)
	}
	if got := readThrough(t, local, 15*time.Second); !strings.Contains(got, "second") {
		t.Errorf("through the tunnel after moving it: %q, want the second destination", got)
	}

	// And it now says it is carrying the rule it was restarted on.
	states, err := term.ForwardStates()
	if err != nil {
		t.Fatal(err)
	}
	if st := states[term.ForwardSessionName(moved)]; st.Args != term.ForwardFingerprint(sshx.ForwardArgs(h, moved)) {
		t.Errorf("the restarted tunnel reports %q, want the fingerprint of the rule it now carries", st.Args)
	}
}

// A port something else is holding is worth finding out about before a session
// is made for it: the message names the port rather than being ssh's, and
// nothing is left behind to be explained away afterwards.
func TestAPortAlreadyTakenIsRefusedBeforeAnythingIsStarted(t *testing.T) {
	if !term.TmuxAvailable() {
		t.Skip("tmux not installed")
	}
	killServer(t)
	forwardOptions(t)

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	taken := l.Addr().(*net.TCPAddr).Port

	h := forwardingHost(t, true)
	f := store.Forward{
		ID: "fwd-taken", HostID: h.ID, Kind: store.ForwardLocal,
		ListenPort: taken, Dest: "127.0.0.1", DestPort: 80,
	}
	t.Cleanup(func() { term.StopForward(f) })

	err = term.StartForward(f, sshx.ForwardArgs(h, f))
	if err == nil {
		t.Fatal("a port already listening was accepted")
	}
	if !strings.Contains(err.Error(), strconv.Itoa(taken)) {
		t.Errorf("the complaint does not name the port: %v", err)
	}

	// And no session was made, so there is no stopped tunnel to explain.
	states, err := term.ForwardStates()
	if err != nil {
		t.Fatal(err)
	}
	if st, ok := states[term.ForwardSessionName(f)]; ok {
		t.Errorf("a session was left behind for a forward that never started: %+v", st)
	}
}

// tmux reports a signal death with an empty exit status, so reading the status
// alone scored it as zero — and a tunnel the system killed read exactly like
// one stopped on purpose, which is the distinction the marks exist to draw.
func TestATunnelKilledBySignalCountsAsFailed(t *testing.T) {
	if !term.TmuxAvailable() {
		t.Skip("tmux not installed")
	}
	killServer(t)
	forwardOptions(t)

	target := startGreeter(t, "the service")
	h := forwardingHost(t, true)
	f := store.Forward{
		ID: "fwd-killed", HostID: h.ID, Kind: store.ForwardLocal,
		ListenPort: freePort(t), Dest: "127.0.0.1", DestPort: target,
	}
	if err := term.StartForward(f, sshx.ForwardArgs(h, f)); err != nil {
		t.Fatalf("StartForward: %v", err)
	}
	t.Cleanup(func() { term.StopForward(f) })

	name := term.ForwardSessionName(f)
	// The pane's own process, as tmux knows it — matching the command line
	// would also catch the tmux server, whose argv carries the same text.
	out, err := exec.Command("tmux", "-L", os.Getenv(term.SocketEnv),
		"list-panes", "-t", name, "-F", "#{pane_pid}").Output()
	if err != nil {
		t.Fatalf("finding the pane's process: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatalf("pane pid %q: %v", out, err)
	}
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatalf("killing the tunnel's ssh: %v", err)
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if st, ok := mustStates(t)[name]; ok && !st.Running {
			if !st.Failed() {
				t.Fatalf("a tunnel killed by a signal reports %+v, which reads as a clean stop", st)
			}
			if st.Signal == "" {
				t.Errorf("nothing was recorded about what killed it: %+v", st)
			}
			if r := term.FailureReason(name, st); !strings.Contains(r, "kill") {
				t.Errorf("FailureReason = %q, want it to say it was killed", r)
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("the tunnel never showed as stopped")
}

func mustStates(t *testing.T) map[string]term.ForwardState {
	t.Helper()
	states, err := term.ForwardStates()
	if err != nil {
		t.Fatal(err)
	}
	return states
}
