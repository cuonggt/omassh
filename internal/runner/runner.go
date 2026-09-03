// Package runner executes snippets on hosts and collects their output.
package runner

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/cuonggt/omassh/internal/sshx"
	"github.com/cuonggt/omassh/internal/store"
)

// maxOutput caps what is kept from one host. A snippet that cats a log would
// otherwise pull an unbounded amount of text into a terminal UI.
const maxOutput = 256 << 10

// DefaultLimit is how many hosts a fan-out talks to at once. Enough to be
// quick, low enough not to look like a connection storm from the far side.
const DefaultLimit = 8

// Result is one host's answer.
type Result struct {
	HostKey  string
	HostName string
	ExitCode int
	Stdout   string
	Stderr   string
	Err      error
	Duration time.Duration
}

// OK reports a clean run: connected, executed, exited zero.
func (r Result) OK() bool { return r.Err == nil && r.ExitCode == 0 }

// Summary is a one-line description for a results list.
func (r Result) Summary() string {
	switch {
	case r.Err != nil:
		return r.Err.Error()
	case r.ExitCode != 0:
		if line := firstLine(r.Stderr); line != "" {
			return line
		}
		return "exit " + itoa(r.ExitCode)
	default:
		if line := firstLine(r.Stdout); line != "" {
			return line
		}
		return "no output"
	}
}

// Run executes command on one host and captures its output.
func Run(ctx context.Context, h store.Host, command string, opts ...string) Result {
	res := Result{HostKey: h.StatKey(), HostName: h.Name}

	cmd := exec.CommandContext(ctx, "ssh", sshx.ExecArgs(h, command, opts...)...)
	var out, errb cappedBuffer
	out.max, errb.max = maxOutput, maxOutput
	cmd.Stdout, cmd.Stderr = &out, &errb
	cmd.Stdin = nil

	start := time.Now()
	err := cmd.Run()
	res.Duration = time.Since(start)
	res.Stdout, res.Stderr = out.String(), errb.String()

	var ee *exec.ExitError
	switch {
	case errors.As(err, &ee):
		// A non-zero exit is the remote command's answer, not a failure of
		// ours; only transport problems go in Err.
		res.ExitCode = ee.ExitCode()
	case err != nil:
		res.Err = err
	}
	if ctx.Err() != nil {
		res.Err = ctx.Err()
	}
	return res
}

// RunAll fans a command out across hosts with bounded concurrency, reporting
// each result as it lands so the UI can fill in progressively.
//
// Every host is attempted regardless of what the others do: one unreachable
// machine must not cancel the rest of the run.
func RunAll(ctx context.Context, hosts []store.Host, command string, limit int, report func(Result), opts ...string) []Result {
	if limit < 1 {
		limit = DefaultLimit
	}
	results := make([]Result, len(hosts))

	g := new(errgroup.Group)
	g.SetLimit(limit)
	for i, h := range hosts {
		g.Go(func() error {
			r := Run(ctx, h, command, opts...)
			results[i] = r
			if report != nil {
				report(r)
			}
			return nil // never cancel siblings
		})
	}
	g.Wait()
	return results
}

// dangerousPatterns are substrings that turn a confirmation into a typed one.
// This is a speed bump for the obvious mistakes, not a security control: it
// cannot understand shell, and a determined command will always get through.
var dangerousPatterns = []string{
	"rm -rf", "rm -fr", "rm -r -f", "rm -f -r",
	"mkfs", "dd of=", "of=/dev/", "> /dev/sd", "> /dev/nvme",
	"shutdown", "reboot", "poweroff", "halt -f",
	"chmod -r 777", "chown -r", "userdel", "groupdel",
	"drop database", "drop table", "truncate table",
	"git push --force", "git push -f",
	":(){:|:&};:", "killall -9",
	"iptables -f", "ufw --force reset",
}

// Dangerous reports whether a command looks destructive, and which pattern
// triggered it.
func Dangerous(command string) (string, bool) {
	norm := strings.Join(strings.Fields(strings.ToLower(command)), " ")
	for _, p := range dangerousPatterns {
		if strings.Contains(norm, p) {
			return p, true
		}
	}
	return "", false
}

// cappedBuffer keeps at most max bytes and records that it stopped.
type cappedBuffer struct {
	mu       sync.Mutex
	buf      bytes.Buffer
	max      int
	overflow bool
}

func (c *cappedBuffer) Write(b []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if room := c.max - c.buf.Len(); room > 0 {
		if len(b) > room {
			c.buf.Write(b[:room])
			c.overflow = true
		} else {
			c.buf.Write(b)
		}
	} else if len(b) > 0 {
		c.overflow = true
	}
	// Report the full length: the writer is not at fault for our cap, and
	// short writes would abort the copy.
	return len(b), nil
}

func (c *cappedBuffer) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.buf.String()
	if c.overflow {
		s += "\n… output truncated"
	}
	return s
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [8]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
