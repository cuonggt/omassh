// Package forward supervises the ssh child processes that hold port-forwarding
// tunnels open.
package forward

import (
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cuonggt/omassh/internal/store"
)

// State is what the UI renders for a rule.
type State uint8

const (
	Stopped State = iota
	Starting
	Running
	Retrying
	Failed
)

func (s State) String() string {
	switch s {
	case Starting:
		return "starting"
	case Running:
		return "running"
	case Retrying:
		return "retrying"
	case Failed:
		return "failed"
	default:
		return "stopped"
	}
}

// Status is a snapshot of one rule's supervision.
type Status struct {
	State    State
	Since    time.Time
	Err      string
	Restarts int
}

// Event announces that a rule's status changed.
type Event struct{ ID string }

// Builder turns a rule into the command that carries it. It is injectable so
// the supervisor can be tested without an SSH server.
type Builder func(store.Forward) (*exec.Cmd, error)

const (
	defaultMaxRestarts = 5
	// healthyFor is how long a tunnel must survive before a later exit is
	// treated as a fresh problem rather than a continuing one.
	healthyFor = 60 * time.Second
)

// Supervisor starts, watches and restarts forwarding processes.
//
// Every tunnel is a child of this process and dies with it. That is a
// deliberate limit rather than an oversight: a forward that outlived the UI
// would be invisible and unkillable from here.
type Supervisor struct {
	mu    sync.Mutex
	procs map[string]*proc
	build Builder

	events chan Event

	// tunables, overridden in tests
	backoffBase time.Duration
	maxRestarts int
}

type proc struct {
	status Status
	cmd    *exec.Cmd
	stop   chan struct{}
	once   sync.Once
	stderr *tail
}

func New(build Builder) *Supervisor {
	return &Supervisor{
		procs:       map[string]*proc{},
		build:       build,
		events:      make(chan Event, 64),
		backoffBase: time.Second,
		maxRestarts: defaultMaxRestarts,
	}
}

// Events reports status changes. It is buffered and lossy by design: the UI
// reads current status on each event, so a dropped duplicate changes nothing.
func (s *Supervisor) Events() <-chan Event { return s.events }

// SetBuilder installs the command factory. It exists so main can own the
// supervisor's lifetime while the UI, which knows how to resolve a rule's host,
// supplies the command.
func (s *Supervisor) SetBuilder(b Builder) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.build = b
}

func (s *Supervisor) emit(id string) {
	select {
	case s.events <- Event{ID: id}:
	default:
	}
}

func (s *Supervisor) Status(id string) Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.procs[id]; ok {
		return p.status
	}
	return Status{}
}

func (s *Supervisor) Active(id string) bool {
	st := s.Status(id)
	return st.State == Running || st.State == Starting || st.State == Retrying
}

// Count returns how many rules are currently held open.
func (s *Supervisor) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, p := range s.procs {
		if p.status.State == Running {
			n++
		}
	}
	return n
}

// Start brings a rule up. It reports an error only for problems detectable
// before spawning anything; later failures arrive as status changes.
func (s *Supervisor) Start(f store.Forward) error {
	if err := f.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	if p, ok := s.procs[f.ID]; ok && p.status.State != Stopped && p.status.State != Failed {
		s.mu.Unlock()
		return fmt.Errorf("%s is already %s", f.Label(), p.status.State)
	}
	s.mu.Unlock()

	// The entry has to exist before any status is recorded against it, or a
	// pre-flight failure would be dropped and the rule would read as merely
	// stopped rather than failed.
	p := &proc{stop: make(chan struct{}), stderr: newTail(12)}
	s.mu.Lock()
	s.procs[f.ID] = p
	s.mu.Unlock()

	// Checking the port ourselves turns ssh's generic bind failure into a
	// message that names the port, before any process exists.
	if err := checkPort(f); err != nil {
		s.setStatus(f.ID, Status{State: Failed, Since: time.Now(), Err: err.Error()})
		return err
	}

	s.setStatus(f.ID, Status{State: Starting, Since: time.Now()})
	go s.supervise(f, p)
	return nil
}

// supervise runs the child, and restarts it with backoff if it exits on its
// own. A deliberate Stop ends the loop without a retry.
func (s *Supervisor) supervise(f store.Forward, p *proc) {
	backoff := s.backoffBase
	restarts := 0

	for {
		s.mu.Lock()
		build := s.build
		s.mu.Unlock()
		if build == nil {
			s.setStatus(f.ID, Status{State: Failed, Since: time.Now(), Err: "no command builder configured"})
			return
		}
		cmd, err := build(f)
		if err != nil {
			s.setStatus(f.ID, Status{State: Failed, Since: time.Now(), Err: err.Error(), Restarts: restarts})
			return
		}
		isolate(cmd)
		cmd.Stdout = nil
		cmd.Stderr = p.stderr
		cmd.Stdin = nil

		if err := cmd.Start(); err != nil {
			s.setStatus(f.ID, Status{State: Failed, Since: time.Now(), Err: err.Error(), Restarts: restarts})
			return
		}

		s.mu.Lock()
		p.cmd = cmd
		s.mu.Unlock()
		started := time.Now()
		s.setStatus(f.ID, Status{State: Running, Since: started, Restarts: restarts})

		waitErr := cmd.Wait()

		select {
		case <-p.stop:
			s.setStatus(f.ID, Status{State: Stopped, Since: time.Now(), Restarts: restarts})
			return
		default:
		}

		// A tunnel that stayed up is considered healthy: its next failure gets
		// a fresh restart budget rather than inheriting an old one.
		if time.Since(started) > healthyFor {
			restarts, backoff = 0, s.backoffBase
		}

		reason := p.stderr.lastLine()
		if reason == "" && waitErr != nil {
			reason = waitErr.Error()
		}
		if reason == "" {
			reason = "connection closed"
		}

		restarts++
		if restarts > s.maxRestarts {
			s.setStatus(f.ID, Status{State: Failed, Since: time.Now(), Err: reason, Restarts: restarts - 1})
			return
		}

		s.setStatus(f.ID, Status{State: Retrying, Since: time.Now(), Err: reason, Restarts: restarts})
		select {
		case <-p.stop:
			s.setStatus(f.ID, Status{State: Stopped, Since: time.Now(), Restarts: restarts})
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

// Stop takes a rule down and prevents any further restart.
func (s *Supervisor) Stop(id string) {
	s.mu.Lock()
	p, ok := s.procs[id]
	s.mu.Unlock()
	if !ok {
		return
	}
	p.once.Do(func() { close(p.stop) })

	s.mu.Lock()
	cmd := p.cmd
	s.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		s.setStatus(id, Status{State: Stopped, Since: time.Now()})
		return
	}

	// Ask politely, then insist: ssh closes its channels on SIGTERM, but a
	// wedged ProxyCommand will not.
	terminate(cmd.Process.Pid)
	go func(pid int) {
		time.Sleep(2 * time.Second)
		kill(pid)
	}(cmd.Process.Pid)
}

// StopAll takes every rule down. main calls it as the UI exits, so tunnels do
// not outlive the process that owns them.
func (s *Supervisor) StopAll() {
	s.mu.Lock()
	ids := make([]string, 0, len(s.procs))
	for id := range s.procs {
		ids = append(ids, id)
	}
	s.mu.Unlock()
	for _, id := range ids {
		s.Stop(id)
	}
}

// LastError returns the most recent stderr from a rule's child process.
func (s *Supervisor) LastError(id string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.procs[id]; ok {
		return p.stderr.lastLine()
	}
	return ""
}

func (s *Supervisor) setStatus(id string, st Status) {
	s.mu.Lock()
	if p, ok := s.procs[id]; ok {
		p.status = st
	}
	s.mu.Unlock()
	s.emit(id)
}

// checkPort reports whether a locally-bound rule can take its port.
//
// Every address the bind name resolves to is tried, not just the first: on a
// dual-stack machine "localhost" is both ::1 and 127.0.0.1, and listening on
// one of them succeeds while the other is occupied — which would let a doomed
// rule through and surface later as an opaque bind failure from ssh.
//
// Only EADDRINUSE counts as a conflict; an address family that is simply not
// configured is not a reason to refuse.
func checkPort(f store.Forward) error {
	if !f.BindsLocally() {
		return nil
	}
	host := f.BindAddr
	if host == "" {
		host = "localhost"
	}
	addrs, err := net.LookupHost(host)
	if err != nil || len(addrs) == 0 {
		addrs = []string{host}
	}

	port := strconv.Itoa(f.ListenPort)
	for _, a := range addrs {
		l, err := net.Listen("tcp", net.JoinHostPort(a, port))
		if err == nil {
			// Released immediately so ssh can claim it; this races in
			// principle, but the common case is a long-running process.
			l.Close()
			continue
		}
		if inUse(err) {
			return fmt.Errorf("port %s is already in use on %s", port, a)
		}
	}
	return nil
}

// tail keeps the last few lines written by a child process.
type tail struct {
	mu    sync.Mutex
	lines []string
	max   int
	part  string
}

func newTail(max int) *tail { return &tail{max: max} }

func (t *tail) Write(b []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.part += string(b)
	for {
		i := strings.IndexByte(t.part, '\n')
		if i < 0 {
			break
		}
		if line := strings.TrimSpace(t.part[:i]); line != "" {
			t.lines = append(t.lines, line)
			if len(t.lines) > t.max {
				t.lines = t.lines[len(t.lines)-t.max:]
			}
		}
		t.part = t.part[i+1:]
	}
	return len(b), nil
}

func (t *tail) lastLine() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.lines) > 0 {
		return t.lines[len(t.lines)-1]
	}
	return strings.TrimSpace(t.part)
}
