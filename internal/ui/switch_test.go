package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/cuonggt/omassh/internal/store"
	"github.com/cuonggt/omassh/internal/term"
)

// runningOn opens a session on a new host in the pane, to a server that holds
// the connection open, and waits until tmux has it — so that leaving it
// leaves something running to come back to.
func (h *harness) runningOn(name string) store.Host {
	h.t.Helper()
	host, err := h.store.PutHost(store.Host{Name: name, Addr: "127.0.0.1", Port: h.silentServer()})
	if err != nil {
		h.t.Fatalf("put host: %v", err)
	}
	h.t.Cleanup(func() { term.KillSession(term.SessionName(host)) })
	h.reload()
	h.selectHost(name)
	h.press("t")
	if h.m.attached == nil || h.m.attached.Host.Name != name {
		h.t.Fatalf("t on %s did not attach it", name)
	}
	h.waitRunning(host)
	return host
}

// waitRunning waits for tmux to have a host's session. The client that makes
// it is still starting when t returns, and a session left before it exists
// is not left running at all.
func (h *harness) waitRunning(host store.Host) {
	h.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !term.HasLiveSession(host) {
		if time.Now().After(deadline) {
			h.t.Fatalf("tmux never had a session for %s", host.Name)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// The prefix's n and p step to the next and previous host with a session
// running, in the list's order and round again at the end, and the session
// left behind goes on running. Reaching one used to be the long way round:
// back to the list, along it to the yellow mark, and t.
func TestSwitchingStepsBetweenTheRunningSessions(t *testing.T) {
	h := newHarness(t)
	hosts := []store.Host{h.runningOn("alpha"), h.runningOn("bravo"), h.runningOn("charlie")}

	for _, step := range []struct {
		key, host, status string
	}{
		{"n", "alpha", "session 1 of 3"}, // round from the end of the list
		{"n", "bravo", "session 2 of 3"},
		{"p", "alpha", "session 1 of 3"},
		{"p", "charlie", "session 3 of 3"}, // and back round from the start
	} {
		h.press("prefix", step.key)
		if got := h.m.attached.Host.Name; got != step.host {
			t.Fatalf("prefix %s went to %s, want %s", step.key, got, step.host)
		}
		if !strings.HasPrefix(h.m.status, step.status) {
			t.Errorf("after prefix %s to %s, status = %q, want it to start %q",
				step.key, step.host, h.m.status, step.status)
		}
	}
	for _, host := range hosts {
		if !term.HasLiveSession(host) {
			t.Errorf("the session on %s stopped when the pane switched away from it", host.Name)
		}
	}
}

// A session that has ended is not one of the running ones: switching away
// from it counts only those still there.
func TestSwitchingFromAnEndedSessionCountsOnlyTheRunningOnes(t *testing.T) {
	h := newHarness(t)
	h.runningOn("alpha")
	h.runningOn("bravo")
	h.addHost("charlie", "127.0.0.1:1") // nothing answers, so it ends at once
	h.selectHost("charlie")
	h.press("t")
	h.waitForEnd()

	h.press("prefix", "n")
	if got := h.m.attached.Host.Name; got != "alpha" {
		t.Fatalf("prefix n from the ended session went to %s, want alpha", got)
	}
	if !strings.HasPrefix(h.m.status, "session 1 of 2") {
		t.Errorf("status = %q, want it to count the two still running", h.m.status)
	}
}

// With nothing else running there is nowhere to go, and it says so rather
// than reconnecting to the session already on screen.
func TestSwitchingWithNoOtherSessionSaysSo(t *testing.T) {
	h := newHarness(t)
	h.runningOn("alpha")
	pane := h.m.attached

	h.press("prefix", "n")
	if h.m.attached != pane {
		t.Error("the pane was replaced with nowhere else to go")
	}
	if want := "no other session running"; h.m.status != want {
		t.Errorf("status = %q, want %q", h.m.status, want)
	}
}

// Without tmux a session ends when the pane leaves it, so there is never
// another one to switch to — which is worth saying, since the key does
// nothing otherwise.
func TestWithoutTmuxThereIsNoSessionToSwitchTo(t *testing.T) {
	h := newHarness(t, func(o *Options) { o.TmuxAvailable = func() bool { return false } })
	h.openLiveSession("alpha")

	h.press("prefix", "n")
	if !strings.Contains(h.m.status, "without tmux") {
		t.Errorf("status = %q, want it to say why there is nothing to switch to", h.m.status)
	}
}

// The keys are on the prefix's own list, which is where anyone who has pressed
// it looks for what comes next.
func TestThePrefixOffersSwitching(t *testing.T) {
	h := newHarness(t)
	h.openLiveSession("alpha")

	h.press("prefix")
	h.mustContain("n/p switch")
}
