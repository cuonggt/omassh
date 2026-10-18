package probe_test

import (
	"fmt"
	"io"
	"net"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	gssh "github.com/gliderlabs/ssh"

	"github.com/cuonggt/omassh/internal/probe"
	"github.com/cuonggt/omassh/internal/sshx"
	"github.com/cuonggt/omassh/internal/store"
)

// needSSH skips where there is no ssh to drive. A probe through a jump host is
// the one kind this package does not make for itself.
//
// It also points ssh away from whoever ran the suite: the servers below mint a
// host key per run, and accepting one must never be written into their
// known_hosts. IdentitiesOnly keeps a key sitting in their agent from standing
// in for the one the test means to offer — a test that passed only because of
// a key in one developer's ~/.ssh has shipped from here before.
func needSSH(t *testing.T) {
	t.Helper()
	for _, bin := range []string{"ssh", "ssh-keygen"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s is not installed; asking a jump host is ssh's work", bin)
		}
	}
	sshx.SetGlobalOptions(offTheRunnersOwnKeys)
	t.Cleanup(func() { sshx.SetGlobalOptions(nil) })
}

// offTheRunnersOwnKeys points ssh away from whoever ran the suite, as the
// settings omassh is given reach every connection it makes.
var offTheRunnersOwnKeys = []string{
	"StrictHostKeyChecking=no", "UserKnownHostsFile=/dev/null", "IdentitiesOnly=yes",
}

func genKey(t *testing.T, path string) string {
	t.Helper()
	cmd := exec.Command("ssh-keygen", "-t", "ed25519", "-N", "", "-C", "test", "-f", path, "-q")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v: %s", err, out)
	}
	return path
}

// visits counts what reached a bastion, which is how a test tells a probe that
// asked the hop from one that quietly dialled the address itself. Everything
// here is on loopback, so a direct dial answers just the same — several of
// these tests would pass against a probe that never went near the hop.
type visits struct {
	mu sync.Mutex
	n  int
}

func (v *visits) seen() {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.n++
}

func (v *visits) count() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.n
}

// bastion is a jump host: an SSH server that opens direct-tcpip channels,
// which is the whole of what a probe asks of a hop. accept is whether it lets
// the probe in at all.
func bastion(t *testing.T, accept bool) (store.Host, *visits) {
	t.Helper()
	var seen visits
	dir := t.TempDir()
	hostKey := genKey(t, filepath.Join(dir, "host"))
	clientKey := genKey(t, filepath.Join(dir, "client"))

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
		ConnCallback: func(_ gssh.Context, c net.Conn) net.Conn {
			seen.seen()
			return c
		},
	}
	if err := gssh.HostKeyFile(hostKey)(srv); err != nil {
		t.Fatal(err)
	}
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close(); l.Close() })

	return store.Host{
		ID: "bastion", Name: "bastion", Addr: "127.0.0.1",
		Port: l.Addr().(*net.TCPAddr).Port, User: "tester", Identity: clientKey,
	}, &seen
}

// behind is a host reached through hop, as the resolver hands one over: the
// jump host's name as it was written, and the host itself resolved beside it.
func behind(hop *store.Host, port int) store.Host {
	return store.Host{
		ID: "h", Name: "private", Addr: "127.0.0.1", Port: port,
		ProxyJump: hop.Name, Jump: hop,
	}
}

// greeting is an sshd's stand-in: it says something to whoever connects
// without being spoken to first, which is what makes a byte arriving mean the
// port is answering.
func greeting(t *testing.T) int {
	t.Helper()
	return serve(t, func(c net.Conn) {
		io.WriteString(c, "SSH-2.0-OpenSSH_for_a_test\r\n")
		c.Close()
	})
}

// mute answers and then says nothing, holding the connection open. A probe of
// it runs until its deadline, which is what makes a sweep's queue observable
// while it is still full.
func mute(t *testing.T) int {
	t.Helper()
	return serve(t, func(c net.Conn) { t.Cleanup(func() { c.Close() }) })
}

func serve(t *testing.T, handle func(net.Conn)) int {
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
			go handle(c)
		}
	}()
	return l.Addr().(*net.TCPAddr).Port
}

func shutPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port
}

// A host behind a jump host of your own used to say nothing at all: its
// address means nothing from here, so the probe passed over it. The hop is
// asked instead, and it can answer.
func TestAHostBehindAJumpHostIsAskedOfItsJumpHost(t *testing.T) {
	needSSH(t)
	hop, asked := bastion(t, true)
	h := behind(&hop, greeting(t))

	if got := probe.Check(t.Context(), h, 3*time.Second); got != probe.Up {
		t.Errorf("a host answering behind a jump host = %v, want up", got)
	}
	if asked.count() == 0 {
		t.Error("the jump host was never asked, so the address was dialled from here")
	}
}

// The hop could not connect, which is news about the host and the one thing
// ssh's own words are read for.
func TestAHostItsJumpHostCannotReachIsDown(t *testing.T) {
	needSSH(t)
	hop, asked := bastion(t, true)
	h := behind(&hop, shutPort(t))

	if got := probe.Check(t.Context(), h, 3*time.Second); got != probe.Down {
		t.Errorf("a host refusing the hop's connection = %v, want down", got)
	}
	if asked.count() == 0 {
		t.Error("the jump host was never asked, so a port shut here is all this found")
	}
}

// A hop that will not let the probe in says nothing about the host, and must
// not be read as saying it is down: that would put a red cross against every
// machine behind a bastion whose key is not in the agent. Twice already this
// package has called machines down that nobody had managed to ask about.
func TestAJumpHostThatWillNotCarryTheProbeLeavesTheHostUnprobed(t *testing.T) {
	needSSH(t)
	hop, asked := bastion(t, false)
	h := behind(&hop, greeting(t))

	if got := probe.Check(t.Context(), h, 3*time.Second); got != probe.Skipped {
		t.Errorf("a host whose hop refused the probe = %v, want not probed", got)
	}
	if asked.count() == 0 {
		t.Error("the hop was never asked, so this says nothing about being refused by one")
	}
}

// A sweep of a fleet behind one bastion is a burst of connections to that
// bastion, and sshd shuts the door past the tenth that has yet to
// authenticate — which would arrive here as a hop that would not carry the
// probe, against hosts that are perfectly fine.
//
// Nothing the sweep starts finishes before its deadline, so the connections
// the bastion has seen while it is still running are the ones in flight.
func TestAJumpHostCarriesNoMoreProbesThanItsShare(t *testing.T) {
	needSSH(t)
	hop, asked := bastion(t, true)
	silent := mute(t)
	var hosts []store.Host
	for i := range 2 * probe.JumpWorkers {
		h := behind(&hop, silent)
		h.ID = fmt.Sprint(i)
		hosts = append(hosts, h)
	}

	timeout := 400 * time.Millisecond
	done := make(chan struct{})
	go func() {
		defer close(done)
		probe.CheckAll(t.Context(), hosts, 0, timeout, nil)
	}()
	time.Sleep(timeout)

	switch got := asked.count(); {
	case got < 1:
		t.Error("the sweep reached the jump host not at all, so it is holding nothing down")
	case got > probe.JumpWorkers:
		t.Errorf("%d probes at one jump host at once, want at most %d", got, probe.JumpWorkers)
	}
	<-done
}

// Up is the far side having spoken, because that is all a hop can be asked:
// it opens the connection, and omassh never hears whether it opened. For an
// ssh port that is the same answer, since sshd greets whoever connects — but a
// port answering with something that waits to be addressed first cannot be
// told from a port that is not answering, and it says so rather than guessing.
//
// It is also what keeps the greeting honest: ssh says a good deal of its own
// on stderr, "Permanently added ... to the list of known hosts" included, and
// a single byte of it on stdout would make every probe here report up.
func TestAPortThatAnswersWithoutSpeakingCannotBeToldFromSilence(t *testing.T) {
	needSSH(t)
	hop, _ := bastion(t, true)
	h := behind(&hop, mute(t))

	if got := probe.Check(t.Context(), h, 300*time.Millisecond); got != probe.Skipped {
		t.Errorf("a host answering in silence = %v, want not probed", got)
	}
}

// The hop that gets asked is the one inheritance worked out, which is the
// whole reason the probe is handed a resolved host the way Build is: this
// host names no jump host of its own, its group does, and an unresolved host
// carries neither the hop nor a way to reach it.
func TestTheHopAskedIsTheOneInheritanceWorkedOut(t *testing.T) {
	needSSH(t)
	hop, asked := bastion(t, true)
	group := store.Group{ID: "g", Name: "behind the bastion", ProxyJump: hop.Name}
	h := store.Host{ID: "h", Name: "private", Addr: "127.0.0.1", Port: greeting(t), GroupID: group.ID}

	r := store.NewResolver([]store.Group{group}, []store.Host{hop, h}, nil)
	if got := probe.Check(t.Context(), r.Resolve(h).Host, 3*time.Second); got != probe.Up {
		t.Errorf("a host whose group names the jump host = %v, want up", got)
	}
	if asked.count() == 0 {
		t.Error("the group's jump host was never asked")
	}
}

// A hop omassh knows only as a destination is asked as well. It carries no key
// of its own — nothing was written down about it beyond where it is — so the
// key comes from ssh's own config, which is what the -o settings omassh is
// given stand for here.
func TestAHopNamedAsFreeTextIsAskedToo(t *testing.T) {
	hop, asked := bastionFor(t)
	h := store.Host{ID: "h", Name: "private", Addr: "127.0.0.1", Port: greeting(t),
		ProxyJump: hop.JumpTarget()}

	if got := probe.Check(t.Context(), h, 3*time.Second); got != probe.Up {
		t.Errorf("a host behind a hop written as a destination = %v, want up", got)
	}
	if asked.count() == 0 {
		t.Error("the hop was never asked, so the address was dialled from here")
	}
}

// And so is a ProxyJump in ssh's own config, which until now was passed over
// along with the ProxyCommands it is written beside — the file being the only
// thing that can tell the two apart.
func TestAJumpHostYourSSHConfigNamesIsAskedToo(t *testing.T) {
	hop, asked := bastionFor(t)
	h := store.Host{ID: "h", Name: "private", Addr: "127.0.0.1", Port: greeting(t),
		ConfigProxy: store.ConfigRoute{Via: hop.JumpTarget(), Jump: true}}

	if got := probe.Check(t.Context(), h, 3*time.Second); got != probe.Up {
		t.Errorf("a host your ssh config sends through a bastion = %v, want up", got)
	}
	if asked.count() == 0 {
		t.Error("the hop was never asked, so the address was dialled from here")
	}
}

// bastionFor is a bastion reached as a destination rather than as one of your
// own hosts. Nothing is written down about such a hop beyond where it is, so
// the key it wants arrives the way ssh's own config would bring it.
func bastionFor(t *testing.T) (store.Host, *visits) {
	t.Helper()
	needSSH(t)
	hop, asked := bastion(t, true)
	sshx.SetGlobalOptions(append(offTheRunnersOwnKeys, "IdentityFile="+hop.Identity))
	return hop, asked
}
