package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/cuonggt/omassh/internal/term"
)

// A session is drawn when it has output, which for the echo of a key is at
// once. A flood of it is held to a frame apart, or printing a large file would
// draw the whole interface once per read.
func TestAFloodOfOutputIsDrawnAtMostOnceAFrame(t *testing.T) {
	changed, done := make(chan struct{}, 1), make(chan struct{})
	changed <- struct{}{}

	last := time.Now()
	if over := awaitOutput(changed, done, false, last); over {
		t.Fatal("output was taken for the end of it")
	}
	if took := time.Since(last); took < paneFrame {
		t.Errorf("drawn again %v after the last time, want at least a frame (%v)", took, paneFrame)
	}
}

// When the output stops for good, the end is reported only once the session
// has ended too, so that what is read then can already say how it ended.
func TestTheEndOfTheOutputWaitsForTheSessionToEnd(t *testing.T) {
	changed, done := make(chan struct{}, 1), make(chan struct{})
	close(changed)

	result := make(chan bool, 1)
	go func() { result <- awaitOutput(changed, done, false, time.Time{}) }()
	select {
	case <-result:
		t.Fatal("the end was reported while the session was still running")
	case <-time.After(50 * time.Millisecond):
	}
	close(done)
	select {
	case over := <-result:
		if !over {
			t.Error("the end of the output was not reported as the end")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the end was never reported")
	}
}

// A session that ends while its output is still open is noticed at once, so
// the status can say how to leave it.
func TestASessionThatEndsIsNoticedBeforeItsOutputCloses(t *testing.T) {
	changed, done := make(chan struct{}, 1), make(chan struct{})
	close(done)

	result := make(chan bool, 1)
	go func() { result <- awaitOutput(changed, done, false, time.Time{}) }()
	select {
	case over := <-result:
		if over {
			t.Error("output that is still open was reported as over")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the session ended and nothing noticed")
	}
}

// Once the end has been reported, only output wakes the watcher. The session
// being over answers at once every time it is asked, so asking again would go
// round and round, drawing the interface as fast as it could with nothing new
// to show.
func TestAnEndedSessionIsWatchedOnlyForOutput(t *testing.T) {
	changed, done := make(chan struct{}, 1), make(chan struct{})
	close(done)

	result := make(chan bool, 1)
	go func() { result <- awaitOutput(changed, done, true, time.Time{}) }()
	select {
	case <-result:
		t.Fatal("woke with nothing new to show, and would keep waking")
	case <-time.After(50 * time.Millisecond):
	}
	changed <- struct{}{}
	select {
	case over := <-result:
		if over {
			t.Error("output after the end was taken for the end of the output")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("output after the end was never drawn")
	}
}

// A pane that has been closed or replaced stops being watched, and so does
// one whose output is over; the attached one is watched for as long as it
// has any.
func TestOnlyTheAttachedPaneIsWatched(t *testing.T) {
	h := newHarness(t)
	h.openSession("alpha")

	if cmd := h.send(paneOutputMsg{pane: &term.Pane{}}); cmd != nil {
		t.Error("a pane that is no longer attached is still being watched")
	}
	if cmd := h.send(paneOutputMsg{pane: h.m.attached}); cmd == nil {
		t.Error("the attached pane stopped being watched after drawing its output")
	}
	if cmd := h.send(paneOutputMsg{pane: h.m.attached, over: true}); cmd != nil {
		t.Error("the attached pane is still watched after its output is over")
	}
}

// The session follows the window. A tick used to resize it on its way past,
// twenty times a second; with nothing ticking, the resize is what does it.
func TestResizingTheWindowResizesTheSession(t *testing.T) {
	h := newHarness(t)
	h.openSession("alpha")

	h.send(tea.WindowSizeMsg{Width: testW + 40, Height: testH + 10})
	wantW, wantH := h.m.sessionArea()
	if w, ht := h.m.attached.Size(); w != wantW || ht != wantH {
		t.Errorf("the session is %dx%d in a pane that is now %dx%d", w, ht, wantW, wantH)
	}
}

// The wheel over a session goes back through what it has said. It reached
// nothing before, and a wheel that reaches nothing is not harmless: a terminal
// on the alternate screen sends arrow keys for it unless the application is
// asking for the mouse, so scrolling a focused session typed up and down into
// it, and the shell answered with the commands last run.
func TestTheWheelOverASessionScrollsItRatherThanTypingIntoIt(t *testing.T) {
	h := newHarness(t)
	h.openSession("alpha")
	h.m.focus = panelSession
	h.m.setStatus("something else")

	l := h.m.layout()
	h.wheel(l.side+2, 1, true)

	// A session this short has nothing to go back to, so it says where it is.
	if !strings.Contains(h.m.status, "live view") {
		t.Errorf("status = %q after a wheel over the session, so it never reached it", h.m.status)
	}
}

// And the terminal has to be asked for the mouse while the session has the
// keyboard, or the wheel never arrives as a wheel at all.
func TestTheMouseIsAskedForWhileASessionIsFocused(t *testing.T) {
	h := newHarness(t)
	h.openSession("alpha")
	h.m.focus = panelSession

	if got := h.m.View().MouseMode; got != tea.MouseModeCellMotion {
		t.Errorf("MouseMode = %v with a session focused; the terminal would send arrow keys for the wheel", got)
	}
}
