package sshx

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/cuonggt/omassh/internal/store"
)

// maxOutput is how much of a script's output is kept.
//
// A snippet that says more than this is not one whose output is being read,
// and keeping all of it would mean a stray cat on a large file filling memory
// on this machine rather than on the far one.
const maxOutput = 256 << 10

// RunArgs is the ssh invocation that runs script on h.
//
// The script goes after the destination and as a single argument. ssh joins
// everything after the destination with spaces before handing it to the remote
// shell, so a script split across argv comes back together with its newlines
// turned into spaces — which changes what a trailing comment comments out, and
// changes it silently.
//
// RequestTTY=no is fixed rather than left to the default, for the reason
// BatchMode is fixed elsewhere: ssh keeps the first value it sees, and a
// RequestTTY in anyone's ssh_config would otherwise hand the remote command a
// terminal it never asked for. What comes back would then be a full-screen
// program's escape sequences in the middle of the output, or a prompt waiting
// on a keyboard that is not there.
//
// opts are preferences and follow the global settings, the way an sftp
// session's do.
func RunArgs(h store.Host, script string, opts ...string) []string {
	fixed := append(Unattended(h), "-o", "RequestTTY=no")
	return append(BuildWith(fixed, h, opts...), script)
}

// Result is what running a script on one host came to.
type Result struct {
	// Output is stdout and stderr together, in the order a terminal would
	// have shown them.
	Output string
	// Truncated is whether Output is only the beginning of what was said.
	Truncated bool
	ExitCode  int
	// Err is omassh's own failure: ssh could not be started, or the run was
	// stopped from here. A script that exited non-zero is not one of these —
	// that is an ordinary result with a number in it.
	Err      error
	Duration time.Duration
}

// Failed reports whether the run is one to look at rather than tick off.
func (r Result) Failed() bool { return r.Err != nil || r.ExitCode != 0 }

// Summary is the one line worth showing beside a host: the last thing said,
// which for most scripts is the thing they were run to find out. Fitting it to
// a row is the caller's business, since only it knows the width.
func (r Result) Summary() string {
	lines := strings.Split(r.Output, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if s := strings.TrimSpace(lines[i]); s != "" {
			return s
		}
	}
	return ""
}

// Run runs script on h and collects what it said.
//
// stdout and stderr are collected together because that is the order they make
// sense in, and because ssh writes its own complaints to stderr — which is
// where the reason for not connecting at all appears. Nothing is on the
// child's stdin, so a script that reads from it gets end of file rather than
// waiting on a keyboard that is not there.
//
// Cancelling kills the ssh on this side, which drops the connection; whether
// the far end stops depends on sshd hanging up the command it started, and it
// usually does. What is certain is that nothing further arrives here.
func Run(ctx context.Context, h store.Host, script string, opts ...string) Result {
	cmd := exec.CommandContext(ctx, "ssh", RunArgs(h, script, opts...)...)
	// A password credential answers through omassh itself; Env is empty for
	// every other kind, so this is the same command it always was.
	if env := Env(h); len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	out := &capped{}
	cmd.Stdout, cmd.Stderr = out, out

	start := time.Now()
	err := cmd.Run()
	r := Result{
		Output:    out.buf.String(),
		Truncated: out.over,
		Duration:  time.Since(start),
	}

	switch {
	case err == nil:
	case ctx.Err() != nil:
		// Stopped from here, so whatever status the child ended with is not a
		// verdict on the script. Reporting it as one is the mistake a
		// cancelled directory copy made once, blaming a file for the stop.
		r.Err = ctx.Err()
	default:
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			r.ExitCode = ee.ExitCode()
			return r
		}
		r.Err = err
	}
	return r
}

// capped keeps the beginning of what is written through it.
//
// The beginning rather than the end, because output is read from the top. It
// always reports the whole write as taken: telling the child it wrote short
// would break the pipe and end the script early, which is a different thing
// from having produced more output than is being kept.
type capped struct {
	buf  bytes.Buffer
	over bool
}

func (c *capped) Write(b []byte) (int, error) {
	switch room := maxOutput - c.buf.Len(); {
	case room >= len(b):
		c.buf.Write(b)
	case room > 0:
		c.buf.Write(b[:room])
		c.over = true
	case len(b) > 0:
		c.over = true
	}
	return len(b), nil
}

// Workers is how many hosts a run touches at once.
//
// Far fewer than a probe's, deliberately. A probe is one TCP connection and
// nothing else; this is a whole ssh session plus whatever the script does at
// the other end, and thirty simultaneous package upgrades is not something to
// start by accident. Four keeps a sweep of a dozen hosts brisk while leaving
// what is happening small enough to follow.
const Workers = 4

// RunEvents is how a sweep reports itself.
//
// Began exists because of the limit: with four workers and forty hosts, most
// of them have not started, and a screen that shows every one as running is
// describing forty ssh sessions that are not open. Either may be nil.
type RunEvents struct {
	Began func(store.Host)
	Done  func(store.Host, Result)
}

// RunAll runs script on every host, reporting each as it starts and finishes.
//
// The callbacks run on each worker, so they have to be safe to call from
// several goroutines at once — the interface sends down a channel, and
// anything writing to a map of its own needs a lock. Results arrive in the
// order they finish, which is not the order they were started in; putting them
// back in the caller's order is the caller's business.
//
// Cancelling stops the ones running and turns the ones still queued into
// results of their own rather than silence: every host gets a word, because a
// host that vanishes from a list of results is indistinguishable from one the
// list never had.
func RunAll(ctx context.Context, hosts []store.Host, script string, limit int, on RunEvents, opts ...string) {
	if limit < 1 {
		limit = Workers
	}
	sem := make(chan struct{}, limit)
	var wg sync.WaitGroup

	for _, h := range hosts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if on.Began != nil {
				on.Began(h)
			}
			r := Run(ctx, h, script, opts...)
			if on.Done != nil {
				on.Done(h, r)
			}
		}()
	}
	wg.Wait()
}
