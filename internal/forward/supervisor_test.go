package forward

import (
	"fmt"
	"net"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/cuonggt/omassh/internal/store"
)

func newTestSupervisor(build Builder) *Supervisor {
	s := New(build)
	s.backoffBase = 10 * time.Millisecond
	s.maxRestarts = 2
	return s
}

// waitFor polls until the rule reaches want, or fails the test.
func waitFor(t *testing.T, s *Supervisor, id string, want State) Status {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var last Status
	for time.Now().Before(deadline) {
		last = s.Status(id)
		if last.State == want {
			return last
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("rule %s is %s (err %q), want %s", id, last.State, last.Err, want)
	return last
}

func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }

var localRule = store.Forward{
	ID: "f1", Kind: store.ForwardLocal,
	ListenPort: 55432, TargetHost: "localhost", TargetPort: 5432,
}

func TestStartAndStop(t *testing.T) {
	s := newTestSupervisor(func(store.Forward) (*exec.Cmd, error) {
		return exec.Command("sleep", "60"), nil
	})

	if err := s.Start(localRule); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitFor(t, s, localRule.ID, Running)
	if s.Count() != 1 {
		t.Errorf("Count = %d, want 1", s.Count())
	}

	s.mu.Lock()
	pid := s.procs[localRule.ID].cmd.Process.Pid
	s.mu.Unlock()

	s.Stop(localRule.ID)
	waitFor(t, s, localRule.ID, Stopped)

	// The child must actually be gone, not merely marked stopped.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && alive(pid) {
		time.Sleep(10 * time.Millisecond)
	}
	if alive(pid) {
		t.Errorf("process %d survived Stop", pid)
	}
	if s.Count() != 0 {
		t.Errorf("Count = %d after stop, want 0", s.Count())
	}
}

// A tunnel that drops should come back, and give up after the restart budget.
func TestRetriesThenFails(t *testing.T) {
	s := newTestSupervisor(func(store.Forward) (*exec.Cmd, error) {
		return exec.Command("sh", "-c", "echo 'bind: Address already in use' >&2; exit 255"), nil
	})

	if err := s.Start(localRule); err != nil {
		t.Fatalf("Start: %v", err)
	}
	got := waitFor(t, s, localRule.ID, Failed)

	if got.Restarts != s.maxRestarts {
		t.Errorf("Restarts = %d, want %d", got.Restarts, s.maxRestarts)
	}
	// The reason shown must be ssh's own words, not just "exit status 255".
	if got.Err != "bind: Address already in use" {
		t.Errorf("Err = %q, want the child's stderr", got.Err)
	}
}

func TestStopDuringRetryDoesNotRestart(t *testing.T) {
	s := New(func(store.Forward) (*exec.Cmd, error) {
		return exec.Command("sh", "-c", "exit 1"), nil
	})
	s.backoffBase = 500 * time.Millisecond
	s.maxRestarts = 10

	if err := s.Start(localRule); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitFor(t, s, localRule.ID, Retrying)
	s.Stop(localRule.ID)
	waitFor(t, s, localRule.ID, Stopped)

	// Give the loop a chance to wrongly wake up and restart.
	time.Sleep(700 * time.Millisecond)
	if st := s.Status(localRule.ID); st.State != Stopped {
		t.Errorf("state = %s after Stop during retry, want stopped", st.State)
	}
}

// The pre-flight check should name the port, without spawning anything.
func TestPortInUseIsCaughtBeforeSpawning(t *testing.T) {
	l, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	port := l.Addr().(*net.TCPAddr).Port

	spawned := false
	s := newTestSupervisor(func(store.Forward) (*exec.Cmd, error) {
		spawned = true
		return exec.Command("sleep", "60"), nil
	})

	rule := store.Forward{ID: "busy", Kind: store.ForwardLocal,
		ListenPort: port, TargetHost: "localhost", TargetPort: 1}
	err = s.Start(rule)

	if err == nil {
		t.Fatal("Start succeeded on a port already in use")
	}
	if want := fmt.Sprintf("port %d is already in use", port); !strings.HasPrefix(err.Error(), want) {
		t.Errorf("err = %q, want it to start with %q", err, want)
	}
	if spawned {
		t.Error("a process was spawned despite the port being taken")
	}
	if st := s.Status(rule.ID); st.State != Failed {
		t.Errorf("state = %s, want failed", st.State)
	}
}

// A remote forward binds on the server, so a busy local port is irrelevant.
func TestRemoteForwardSkipsLocalPortCheck(t *testing.T) {
	l, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	port := l.Addr().(*net.TCPAddr).Port

	s := newTestSupervisor(func(store.Forward) (*exec.Cmd, error) {
		return exec.Command("sleep", "60"), nil
	})
	rule := store.Forward{ID: "r1", Kind: store.ForwardRemote,
		ListenPort: port, TargetHost: "localhost", TargetPort: 1}

	if err := s.Start(rule); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitFor(t, s, rule.ID, Running)
	s.StopAll()
	waitFor(t, s, rule.ID, Stopped)
}

func TestStartRejectsInvalidRule(t *testing.T) {
	s := newTestSupervisor(func(store.Forward) (*exec.Cmd, error) {
		t.Fatal("should not build a command for an invalid rule")
		return nil, nil
	})
	if err := s.Start(store.Forward{ID: "bad", Kind: store.ForwardLocal}); err == nil {
		t.Error("Start accepted an invalid rule")
	}
}

func TestEventsAreEmitted(t *testing.T) {
	s := newTestSupervisor(func(store.Forward) (*exec.Cmd, error) {
		return exec.Command("sleep", "60"), nil
	})
	if err := s.Start(localRule); err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-s.Events():
		if ev.ID != localRule.ID {
			t.Errorf("event for %q, want %q", ev.ID, localRule.ID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no event emitted on start")
	}
	s.StopAll()
}

func TestTailKeepsLastLine(t *testing.T) {
	tl := newTail(2)
	fmt.Fprintf(tl, "first\nsecond\n")
	fmt.Fprintf(tl, "third\n")
	if got := tl.lastLine(); got != "third" {
		t.Errorf("lastLine = %q, want third", got)
	}
	// A partial line with no newline is still reportable.
	tl2 := newTail(2)
	fmt.Fprint(tl2, "no newline yet")
	if got := tl2.lastLine(); got != "no newline yet" {
		t.Errorf("lastLine = %q", got)
	}
}

// A dual-stack machine resolves "localhost" to both ::1 and 127.0.0.1.
// Listening on one succeeds while the other is held, so the check has to try
// every address or it waves through a rule that ssh cannot bind.
func TestPortCheckSeesConflictOnEitherFamily(t *testing.T) {
	for _, held := range []string{"127.0.0.1", "::1"} {
		t.Run(held, func(t *testing.T) {
			l, err := net.Listen("tcp", net.JoinHostPort(held, "0"))
			if err != nil {
				t.Skipf("cannot bind %s: %v", held, err)
			}
			defer l.Close()
			port := l.Addr().(*net.TCPAddr).Port

			spawned := false
			s := newTestSupervisor(func(store.Forward) (*exec.Cmd, error) {
				spawned = true
				return exec.Command("sleep", "60"), nil
			})
			rule := store.Forward{ID: "c-" + held, Kind: store.ForwardLocal,
				ListenPort: port, TargetHost: "localhost", TargetPort: 1}

			if err := s.Start(rule); err == nil {
				s.StopAll()
				t.Fatalf("Start succeeded while %s:%d was held", held, port)
			}
			if spawned {
				t.Error("spawned ssh despite the port being taken")
			}
		})
	}
}
