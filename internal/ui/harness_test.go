package ui

import (
	"github.com/charmbracelet/x/ansi"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/cuonggt/omassh/internal/keymap"
	"github.com/cuonggt/omassh/internal/store"
	"github.com/cuonggt/omassh/internal/term"
)

// The Bubble Tea model is an ordinary value with pure Update and View, so it
// can be driven directly. That is cheaper and more deterministic than a fake
// terminal, and covers everything short of real rendering.

const testW, testH = 100, 30

type harness struct {
	t     *testing.T
	m     Model
	store *store.Store
}

func newHarness(t *testing.T, opts ...func(*Options)) *harness {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	o := Options{
		Keys:         keymap.Default(),
		ProbeTimeout: time.Second,
	}
	for _, fn := range opts {
		fn(&o)
	}

	h := &harness{t: t, store: st, m: New(st, o)}
	t.Cleanup(func() { h.m.Close() })
	h.send(tea.WindowSizeMsg{Width: testW, Height: testH})
	return h
}

// selectHost moves the cursor onto a host by name. addHost does not change
// the selection, so a test that adds two hosts and then presses e would edit
// the first one both times.
func (h *harness) selectHost(name string) {
	h.t.Helper()
	h.m.focus = panelHosts
	for i, x := range h.m.visibleHosts() {
		if x.Name == name {
			h.m.hostIdx = i
			return
		}
	}
	h.t.Fatalf("no host named %q", name)
}

// pickUntil walks the open picker to a choice. It gives up rather than
// looping forever: a choice that is not in the list is a failure to report,
// not a test that hangs until the timeout kills the whole package.
func (h *harness) pickUntil(want string) {
	h.t.Helper()
	if !h.m.form.picking {
		h.t.Fatal("the picker is not open")
	}
	for range len(h.m.form.fields[h.m.form.idx].choices) {
		if h.m.form.currentChoice() == want {
			return
		}
		h.press("down")
	}
	h.t.Fatalf("no choice %q in %v", want, h.m.form.fields[h.m.form.idx].choices)
}

// send delivers a message and keeps the resulting model.
func (h *harness) send(msg tea.Msg) tea.Cmd {
	h.t.Helper()
	m, cmd := h.m.Update(msg)
	h.m = m.(Model)
	return cmd
}

// press sends key presses, one per argument. Multi-character arguments that
// are not named keys are typed a rune at a time.
func (h *harness) press(keys ...string) {
	h.t.Helper()
	for _, k := range keys {
		switch k {
		case "enter":
			h.send(tea.KeyPressMsg{Code: tea.KeyEnter})
		case "esc":
			h.send(tea.KeyPressMsg{Code: tea.KeyEscape})
		case "tab":
			h.send(tea.KeyPressMsg{Code: tea.KeyTab})
		case "space":
			h.send(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
		case "shift+tab":
			h.send(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
		case "backspace":
			h.send(tea.KeyPressMsg{Code: tea.KeyBackspace})
		case "up":
			h.send(tea.KeyPressMsg{Code: tea.KeyUp})
		case "down":
			h.send(tea.KeyPressMsg{Code: tea.KeyDown})
		case "left":
			h.send(tea.KeyPressMsg{Code: tea.KeyLeft})
		case "right":
			h.send(tea.KeyPressMsg{Code: tea.KeyRight})
		default:
			for _, r := range k {
				h.send(tea.KeyPressMsg{Code: r, Text: string(r)})
			}
		}
	}
}

// type_ enters literal text, so a word is not mistaken for a named key.
func (h *harness) type_(s string) {
	h.t.Helper()
	for _, r := range s {
		h.send(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

// reload picks up records written to the store behind the model's back.
func (h *harness) reload() {
	h.t.Helper()
	h.m.reload()
}

var ansiRE = regexp.MustCompile(`\x1b\][^\x07\x1b]*(\x07|\x1b\\)|\x1b[\[\]][0-9;?]*[a-zA-Z]|\x1b[()][B0]|\x1b[=>]`)

// screen is the rendered view with styling stripped, for assertions that care
// about what the user reads rather than how it is coloured.
func (h *harness) screen() string {
	h.t.Helper()
	return ansiRE.ReplaceAllString(h.m.View().Content, "")
}

func (h *harness) contains(want string) bool {
	return strings.Contains(h.screen(), want)
}

func (h *harness) mustContain(want string) {
	h.t.Helper()
	if !h.contains(want) {
		h.t.Errorf("screen does not contain %q:\n%s", want, h.screen())
	}
}

func (h *harness) mustNotContain(bad string) {
	h.t.Helper()
	if h.contains(bad) {
		h.t.Errorf("screen unexpectedly contains %q:\n%s", bad, h.screen())
	}
}

// addHost writes a host directly, for tests that need one without driving the
// form.
func (h *harness) addHost(name, addr string) store.Host {
	h.t.Helper()
	host, err := h.store.PutHost(store.Host{Name: name, Addr: addr})
	if err != nil {
		h.t.Fatalf("put host: %v", err)
	}
	h.reload()
	return host
}

// sessionNameFor exposes the tmux session name a host maps to, so a test can
// pretend one is running without starting tmux.
func sessionNameFor(h store.Host) string { return term.SessionName(h) }

// keymapHas reports whether an action is bound, for tests that only care that
// a way in exists.
func keymapHas(h *harness, action string) (string, bool) {
	k := h.m.keys.Key(keymap.Action(action))
	return k, k != "?"
}

// ansiWidth measures a rendered line in terminal cells, ignoring escapes.
func ansiWidth(s string) int { return ansi.StringWidth(s) }
