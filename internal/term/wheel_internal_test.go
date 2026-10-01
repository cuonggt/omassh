package term

import (
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/vt"
)

// running is a pane without tmux, as Open makes one, whose program has written
// output. It returns what the pane has sent back to the program, read off as a
// pty would read it — the emulator's writes wait for a reader.
func running(t *testing.T, w, h int, output string) (*Pane, func() string) {
	t.Helper()
	p := &Pane{em: vt.NewSafeEmulator(w, h), w: w, h: h,
		done: make(chan struct{}), changed: make(chan struct{}, 1)}
	p.em.SetScrollbackSize(scrollback)
	p.followProgramMouse()

	// The reader is left waiting when the test ends. Closing the emulator to
	// end it races inside the emulator, whose Close sets a flag its Read
	// checks unguarded — and omassh never closes one, so there is nothing of
	// its own to test there. A goroutine waiting on a pipe costs nothing.
	var mu sync.Mutex
	var sent strings.Builder
	go func() {
		buf := make([]byte, 256)
		for {
			n, err := p.em.Read(buf)
			mu.Lock()
			sent.Write(buf[:n])
			mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	p.em.Write([]byte(output))

	// The bytes have been read once the write returns, but the goroutine may
	// not have kept them yet; a moment settles it.
	return p, func() string {
		time.Sleep(20 * time.Millisecond)
		mu.Lock()
		defer mu.Unlock()
		s := sent.String()
		sent.Reset()
		return s
	}
}

// A program on the alternate screen — less, man, vim — gets the wheel as the
// arrow keys a terminal sends in its place, a notch's worth of lines at a time
// and in the program's own cursor-key mode. The history above it is the
// shell's from before it started, which is not what anyone scrolling over it
// meant to see.
func TestAProgramOnTheAlternateScreenGetsTheWheelAsArrowKeys(t *testing.T) {
	p, sent := running(t, 30, 5, "\x1b[?1049h")

	if !p.Wheel(0, 0, false, 3) {
		t.Fatal("the program did not take the wheel")
	}
	if got, want := sent(), strings.Repeat("\x1b[B", 3); got != want {
		t.Errorf("sent %q for a notch down, want %q", got, want)
	}

	// less and vim turn on application cursor keys, and the arrows follow.
	p.em.Write([]byte("\x1b[?1h"))
	p.Wheel(0, 0, true, 3)
	if got, want := sent(), strings.Repeat("\x1bOA", 3); got != want {
		t.Errorf("sent %q for a notch up in application mode, want %q", got, want)
	}
}

// A program that asked for the mouse gets the wheel itself, a notch as one
// event, at the cell under the pointer — and scrolls by however much it
// scrolls for one.
func TestAProgramThatAskedForTheMouseGetsTheWheelItself(t *testing.T) {
	p, sent := running(t, 30, 5, "\x1b[?1049h\x1b[?1000h\x1b[?1006h")

	p.Wheel(4, 2, true, 3)
	p.Wheel(4, 2, false, 3)
	if got, want := sent(), "\x1b[<64;5;3M\x1b[<65;5;3M"; got != want {
		t.Errorf("sent %q, want a wheel up and a wheel down at the cell, %q", got, want)
	}
}

// A shell's wheel is its history, which the caller scrolls; nothing is sent.
// Arrow keys there are the commands you last ran.
func TestAShellsWheelIsItsHistory(t *testing.T) {
	p, sent := running(t, 30, 5, "$ ls\r\nnotes.txt\r\n$ ")

	if p.Wheel(0, 0, true, 3) {
		t.Error("a shell took the wheel")
	}
	if got := sent(); got != "" {
		t.Errorf("sent %q to a shell", got)
	}
}

// When the program lets go of the mouse it gets arrow keys again, and when it
// leaves the screen the wheel is the history's. A reset, which puts every mode
// back without saying so one by one, lets go of the mouse too — or the wheel
// would go on being sent to a shell as mouse reports it never asked for.
func TestWhenAProgramLetsGoTheWheelGoesBack(t *testing.T) {
	p, sent := running(t, 30, 5, "\x1b[?1049h\x1b[?1000h\x1b[?1002h")

	for _, step := range []struct {
		output string
		want   wheelGoes
	}{
		{"", asWheel},
		{"\x1b[?1002l", asWheel}, // still asking, by the other mode
		{"\x1b[?1000l", asArrows},
		{"\x1b[?1049l", toHistory},
		{"\x1b[?1049h\x1b[?1000h", asWheel},
		// A full reset, as `reset` sends. The alternate screen going too
		// says the emulator's own reset still ran after this one's.
		{"\x1bc", toHistory},
	} {
		p.em.Write([]byte(step.output))
		if got := p.wheelGoes(); got != step.want {
			t.Errorf("after %q the wheel goes to %v, want %v", step.output, got, step.want)
		}
	}
	sent()
}

// Scrolled back, the wheel goes on through the history whatever has the
// screen: the view is history, and turning the wheel there is reading it.
func TestScrolledBackTheWheelStaysInTheHistory(t *testing.T) {
	var lines []string
	for i := range 20 {
		lines = append(lines, "line-"+strconv.Itoa(i))
	}
	p, sent := running(t, 30, 5, strings.Join(lines, "\r\n")+"\x1b[?1000h")
	p.ScrollUp(3)

	if p.Wheel(0, 0, true, 3) {
		t.Error("the program took the wheel from a view scrolled back into the history")
	}
	if got := sent(); got != "" {
		t.Errorf("sent %q", got)
	}
}

// A session that has ended has nothing running to give the wheel to.
func TestAnEndedSessionsWheelIsItsHistory(t *testing.T) {
	p, _ := running(t, 30, 5, "\x1b[?1049h\x1b[?1000h")
	p.mu.Lock()
	p.exited = true
	p.mu.Unlock()

	if p.Wheel(0, 0, true, 3) {
		t.Error("a session that has ended took the wheel")
	}
}
