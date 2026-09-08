package ui

import (
	"testing"
)

// A deleted host's session would otherwise keep running with nothing left in
// the interface that can reach it.
func TestDeletingAHostWarnsAboutItsSession(t *testing.T) {
	h := newHarness(t)
	host := h.addHost("doomed", "10.0.0.1")

	// Without a live session the prompt says only that history is kept.
	h.press("2", "d")
	h.mustContain("session history is kept")
	h.mustNotContain("running session is ended")
	h.press("n")

	// With one, the prompt says the session goes too.
	h.m.d.live = map[string]bool{sessionNameFor(host): true}
	h.press("d")
	h.mustContain("running session is ended")
	h.press("n")
}
