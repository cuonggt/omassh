package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/cuonggt/omassh/internal/keymap"
	"github.com/cuonggt/omassh/internal/store"
)

// Enter opens an SSH connection, so anything the terminal delivers before the
// first frame — a newline left in the buffer by the launching shell — must not
// be acted on.
func TestKeysBeforeFirstFrameAreIgnored(t *testing.T) {
	m := Model{}
	if m.ready {
		t.Fatal("a fresh model should not be ready")
	}
	got, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil {
		t.Errorf("enter before the first frame produced a command: %T", cmd)
	}
	if got.(Model).mode != modeBrowse {
		t.Error("mode changed before the first frame")
	}
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if !sized.(Model).ready {
		t.Error("a window size message should make the model ready")
	}
}

func TestPanelFocusCycles(t *testing.T) {
	h := newHarness(t)
	h.addHost("web", "10.0.0.1")

	// The session panel is skipped while nothing is attached, or tab would
	// land on an empty panel that swallows every key.
	seen := map[panel]bool{}
	for range 6 {
		h.press("tab")
		seen[h.m.focus] = true
	}
	if seen[panelSession] {
		t.Error("tab reached the session panel with no session attached")
	}
	for _, p := range []panel{panelGroups, panelHosts, panelForwards} {
		if !seen[p] {
			t.Errorf("tab never reached panel %v", p)
		}
	}
}

func TestNumberKeysSelectPanels(t *testing.T) {
	h := newHarness(t)
	for _, tc := range []struct {
		key  string
		want panel
	}{{"1", panelGroups}, {"2", panelHosts}, {"3", panelForwards}} {
		h.press(tc.key)
		if h.m.focus != tc.want {
			t.Errorf("%q focused %v, want %v", tc.key, h.m.focus, tc.want)
		}
	}
}

func TestFuzzySearchSpansGroups(t *testing.T) {
	h := newHarness(t)
	prod, _ := h.store.PutGroup(store.Group{Name: "Production"})
	stg, _ := h.store.PutGroup(store.Group{Name: "Staging"})
	h.store.PutHost(store.Host{Name: "prod-web", Addr: "10.0.1.1", GroupID: prod.ID})
	h.store.PutHost(store.Host{Name: "stg-web", Addr: "10.0.2.1", GroupID: stg.ID})
	h.store.PutHost(store.Host{Name: "prod-db", Addr: "10.0.1.2", GroupID: prod.ID})
	h.reload()

	h.press("/")
	if h.m.mode != modeFilter {
		t.Fatalf("/ did not enter filter mode, got %v", h.m.mode)
	}
	h.type_("web")

	// Both matches, though they live in different groups.
	h.mustContain("prod-web")
	h.mustContain("stg-web")
	h.mustNotContain("prod-db")

	h.press("esc")
	if h.m.filtering() {
		t.Error("esc did not clear the filter")
	}
}

// Creating a host must leave it selected, or the next keystroke acts on
// whatever was selected before.
func TestNewHostFormCreatesAndSelects(t *testing.T) {
	h := newHarness(t)
	h.addHost("existing", "10.0.0.9")

	h.press("2", "n")
	if h.m.mode != modeForm {
		t.Fatalf("n did not open a form, got %v", h.m.mode)
	}
	h.type_("newbox")
	h.press("tab")
	h.type_("10.9.9.9")
	h.press("enter")

	if h.m.mode != modeBrowse {
		t.Fatalf("saving did not return to the browser, got %v", h.m.mode)
	}
	got, ok := h.m.selectedHost()
	if !ok || got.Name != "newbox" {
		t.Errorf("selected host = %+v, want the newly created one", got)
	}
	h.mustContain("newbox")
}

func TestFormValidationKeepsTheFormOpen(t *testing.T) {
	h := newHarness(t)
	h.press("2", "n")
	h.press("enter") // no name, no address

	if h.m.mode != modeForm {
		t.Fatal("an invalid form was accepted")
	}
	h.mustContain("needs a name")
}

// Deleting must ask first; the confirmation is the only thing between a
// keystroke and losing a record.
func TestDeleteAsksBeforeRemoving(t *testing.T) {
	h := newHarness(t)
	h.addHost("doomed", "10.0.0.1")
	h.press("2", "d")

	if h.m.mode != modeConfirm {
		t.Fatalf("d did not ask for confirmation, got %v", h.m.mode)
	}
	h.mustContain("Delete host doomed?")

	h.press("n") // decline
	hosts, _ := h.store.Hosts()
	if len(hosts) != 1 {
		t.Fatalf("declining still deleted the host")
	}

	h.press("d", "y")
	hosts, _ = h.store.Hosts()
	if len(hosts) != 0 {
		t.Errorf("confirming did not delete: %+v", hosts)
	}
}

// The help is generated from the live keymap; a hardcoded list silently lies
// after a rebind.
func TestHelpShowsConfiguredKeys(t *testing.T) {
	km, err := keymap.New(map[string]string{"connect": "c", "search": "f"})
	if err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, func(o *Options) { o.Keys = km })

	h.press("?")
	if h.m.mode != modeHelp {
		t.Fatalf("? did not open help, got %v", h.m.mode)
	}
	screen := h.screen()
	for _, want := range []string{
		"c             connect",
		"f             fuzzy search",
	} {
		if !strings.Contains(strings.Join(strings.Fields(screen), " "), strings.Join(strings.Fields(want), " ")) {
			t.Errorf("help does not show the rebound key %q:\n%s", want, screen)
		}
	}
}

// The status is the thing that just changed; when the terminal is too narrow
// for both, the fixed hint list is what gives way.
func TestStatusSurvivesANarrowTerminal(t *testing.T) {
	h := newHarness(t)
	h.m.setStatus("a distinctive status message")
	h.send(tea.WindowSizeMsg{Width: 60, Height: 20})

	if !strings.Contains(h.screen(), "a distinctive status message") {
		t.Errorf("status was dropped at 60 columns:\n%s", h.screen())
	}
}

// Every rendered frame must be exactly the terminal's width and height, or the
// layout tears.
func TestFrameGeometry(t *testing.T) {
	h := newHarness(t)
	h.addHost("web", "10.0.0.1")

	for _, size := range [][2]int{{100, 30}, {60, 20}, {180, 50}, {41, 13}} {
		h.send(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		lines := strings.Split(h.screen(), "\n")
		if len(lines) != size[1] {
			t.Errorf("%dx%d rendered %d lines, want %d", size[0], size[1], len(lines), size[1])
		}
		for i, l := range lines {
			if w := len([]rune(l)); w > size[0] {
				t.Errorf("%dx%d line %d is %d wide, want at most %d", size[0], size[1], i, w, size[0])
			}
		}
	}
}

func TestQuitKey(t *testing.T) {
	h := newHarness(t)
	if cmd := h.send(tea.KeyPressMsg{Code: 'q', Text: "q"}); cmd == nil {
		t.Error("q produced no command, expected quit")
	}
}
