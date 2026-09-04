package ui

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/cuonggt/omassh/internal/forward"
	"github.com/cuonggt/omassh/internal/keymap"
	"github.com/cuonggt/omassh/internal/secrets"
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

	sup := forward.New(nil)
	t.Cleanup(sup.StopAll)

	o := Options{
		Keys:   keymap.Default(),
		Fanout: 4, ProbeTimeout: time.Second,
		// An empty file, so tests never depend on the developer's own config.
		SSHConfigPath: filepath.Join(dir, "no-such-ssh-config"),
	}
	for _, fn := range opts {
		fn(&o)
	}

	h := &harness{t: t, store: st, m: New(st, secrets.NewMemory(), sup, o)}
	t.Cleanup(func() { h.m.Close() })
	h.send(tea.WindowSizeMsg{Width: testW, Height: testH})
	return h
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
		case "backspace":
			h.send(tea.KeyPressMsg{Code: tea.KeyBackspace})
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

func writeFile(path, body string) error {
	return os.WriteFile(path, []byte(body), 0o600)
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
