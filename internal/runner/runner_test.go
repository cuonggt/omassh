package runner_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	gssh "github.com/gliderlabs/ssh"

	"github.com/cuonggt/omassh/internal/keys"
	"github.com/cuonggt/omassh/internal/runner"
	"github.com/cuonggt/omassh/internal/store"
)

var testOpts = []string{
	"-o", "StrictHostKeyChecking=no",
	"-o", "UserKnownHostsFile=/dev/null",
	"-o", "IdentitiesOnly=yes",
}

// concurrent tracks how many commands the server is running at once, so the
// fan-out limit can be observed rather than assumed.
type concurrent struct{ now, peak atomic.Int64 }

func (c *concurrent) enter() {
	n := c.now.Add(1)
	for {
		p := c.peak.Load()
		if n <= p || c.peak.CompareAndSwap(p, n) {
			return
		}
	}
}
func (c *concurrent) leave() { c.now.Add(-1) }

// startExecServer runs a real SSH server that actually executes commands.
func startExecServer(t *testing.T, hostKey string, c *concurrent) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &gssh.Server{
		PublicKeyHandler: func(gssh.Context, gssh.PublicKey) bool { return true },
		Handler: func(s gssh.Session) {
			if c != nil {
				c.enter()
				defer c.leave()
			}
			cmd := exec.Command("sh", "-c", s.RawCommand())
			cmd.Stdout = s
			cmd.Stderr = s.Stderr()
			err := cmd.Run()
			code := 0
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				code = ee.ExitCode()
			} else if err != nil {
				code = 127
			}
			s.Exit(code)
		},
	}
	if err := gssh.HostKeyFile(hostKey)(srv); err != nil {
		t.Fatal(err)
	}
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close(); l.Close() })
	return l.Addr().(*net.TCPAddr).Port
}

func testHost(t *testing.T, c *concurrent) store.Host {
	t.Helper()
	dir := t.TempDir()
	hk, err := keys.Generate(filepath.Join(dir, "host"), "host", "")
	if err != nil {
		t.Fatal(err)
	}
	ck, err := keys.Generate(filepath.Join(dir, "client"), "client", "")
	if err != nil {
		t.Fatal(err)
	}
	port := startExecServer(t, hk.Path, c)
	return store.Host{ID: "h1", Name: "testsrv", Addr: "127.0.0.1", Port: port,
		User: "tester", Identity: ck.Path}
}

func TestRun(t *testing.T) {
	h := testHost(t, nil)
	ctx := context.Background()

	t.Run("captures stdout", func(t *testing.T) {
		r := runner.Run(ctx, h, "echo hello world", testOpts...)
		if !r.OK() {
			t.Fatalf("not OK: %+v", r)
		}
		if strings.TrimSpace(r.Stdout) != "hello world" {
			t.Errorf("Stdout = %q", r.Stdout)
		}
		if r.HostName != "testsrv" || r.HostKey != "h1" {
			t.Errorf("host identification = %q / %q", r.HostName, r.HostKey)
		}
		if r.Duration == 0 {
			t.Error("Duration not recorded")
		}
	})

	t.Run("keeps stderr separate", func(t *testing.T) {
		r := runner.Run(ctx, h, "echo out; echo err >&2", testOpts...)
		if strings.TrimSpace(r.Stdout) != "out" {
			t.Errorf("Stdout = %q, want out", r.Stdout)
		}
		if strings.TrimSpace(r.Stderr) != "err" {
			t.Errorf("Stderr = %q, want err", r.Stderr)
		}
	})

	// A non-zero exit is the remote command answering, not a transport failure.
	t.Run("non-zero exit is data, not an error", func(t *testing.T) {
		r := runner.Run(ctx, h, "echo nope >&2; exit 3", testOpts...)
		if r.Err != nil {
			t.Errorf("Err = %v, want nil", r.Err)
		}
		if r.ExitCode != 3 {
			t.Errorf("ExitCode = %d, want 3", r.ExitCode)
		}
		if r.OK() {
			t.Error("OK() true for a non-zero exit")
		}
		if got := r.Summary(); got != "nope" {
			t.Errorf("Summary = %q, want the stderr line", got)
		}
	})

	t.Run("quoting survives the trip", func(t *testing.T) {
		r := runner.Run(ctx, h, `echo "a  b   c" | tr -s ' '`, testOpts...)
		if strings.TrimSpace(r.Stdout) != "a b c" {
			t.Errorf("Stdout = %q", r.Stdout)
		}
	})

	t.Run("unreachable host reports an error", func(t *testing.T) {
		dead := store.Host{ID: "x", Name: "dead", Addr: "127.0.0.1", Port: 1, User: "u"}
		r := runner.Run(ctx, dead, "true", "-o", "ConnectTimeout=3")
		if r.Err == nil && r.ExitCode == 0 {
			t.Error("a dead host produced a clean result")
		}
	})
}

func TestRunAllFansOut(t *testing.T) {
	var c concurrent
	base := testHost(t, &c)

	const n = 12
	hosts := make([]store.Host, n)
	for i := range hosts {
		hosts[i] = base
		hosts[i].ID = fmt.Sprintf("h%d", i)
		hosts[i].Name = fmt.Sprintf("host-%02d", i)
	}

	var reported atomic.Int64
	const limit = 3
	results := runner.RunAll(context.Background(), hosts, "sleep 0.2; echo done", limit,
		func(runner.Result) { reported.Add(1) }, testOpts...)

	if len(results) != n {
		t.Fatalf("got %d results, want %d", len(results), n)
	}
	if int(reported.Load()) != n {
		t.Errorf("reported %d results, want %d", reported.Load(), n)
	}
	for i, r := range results {
		if !r.OK() {
			t.Errorf("result %d not OK: %+v", i, r)
		}
		// Results must land in the same order as the hosts given.
		if r.HostName != hosts[i].Name {
			t.Errorf("result %d is for %q, want %q", i, r.HostName, hosts[i].Name)
		}
	}
	if peak := c.peak.Load(); peak > limit {
		t.Errorf("peak concurrency %d exceeded the limit of %d", peak, limit)
	}
	if peak := c.peak.Load(); peak < 2 {
		t.Errorf("peak concurrency %d — the fan-out did not run in parallel", peak)
	}
}

// One unreachable machine must not cancel the rest of the run.
func TestRunAllContinuesPastFailures(t *testing.T) {
	good := testHost(t, nil)
	bad := store.Host{ID: "bad", Name: "bad", Addr: "127.0.0.1", Port: 1, User: "u"}

	hosts := []store.Host{good, bad, good}
	hosts[2].ID, hosts[2].Name = "h3", "third"

	results := runner.RunAll(context.Background(), hosts, "echo alive", 2, nil,
		append(testOpts, "-o", "ConnectTimeout=3")...)

	if len(results) != 3 {
		t.Fatalf("got %d results", len(results))
	}
	if !results[0].OK() || !results[2].OK() {
		t.Errorf("reachable hosts failed: %+v / %+v", results[0], results[2])
	}
	if results[1].OK() {
		t.Error("the dead host reported success")
	}
}

func TestRunAllRespectsContextCancellation(t *testing.T) {
	h := testHost(t, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	start := time.Now()
	results := runner.RunAll(ctx, []store.Host{h}, "sleep 30", 1, nil, testOpts...)

	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("took %s — cancellation did not take effect", elapsed)
	}
	if results[0].Err == nil {
		t.Error("cancelled run reported no error")
	}
}

func TestDangerous(t *testing.T) {
	risky := []string{
		"rm -rf /var/tmp/x",
		"sudo rm -fr /",
		"RM -RF /data",
		"dd of=/dev/disk2 if=/dev/zero",
		"mkfs.ext4 /dev/sdb1",
		"systemctl reboot",
		"psql -c 'DROP DATABASE prod'",
		"git push --force origin main",
		"  rm   -rf   /opt  ",
	}
	for _, c := range risky {
		if p, ok := runner.Dangerous(c); !ok {
			t.Errorf("Dangerous(%q) = false, want true", c)
		} else if p == "" {
			t.Errorf("Dangerous(%q) matched but named no pattern", c)
		}
	}

	safe := []string{
		"uptime", "df -h", "systemctl status nginx",
		"tail -n 100 /var/log/syslog", "ls -la /tmp",
		"git push origin main",
	}
	for _, c := range safe {
		if p, ok := runner.Dangerous(c); ok {
			t.Errorf("Dangerous(%q) = true (matched %q), want false", c, p)
		}
	}
}

func TestOutputIsCapped(t *testing.T) {
	h := testHost(t, nil)
	// Far more than the cap, so the guard has to engage.
	r := runner.Run(context.Background(), h, "yes abcdefghij | head -c 2000000", testOpts...)

	if len(r.Stdout) > (256<<10)+64 {
		t.Errorf("captured %d bytes, want it capped near 256KiB", len(r.Stdout))
	}
	if !strings.Contains(r.Stdout, "truncated") {
		t.Error("truncation was not disclosed in the output")
	}
}
