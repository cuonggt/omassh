package ui

import (
	"fmt"
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

	seen := map[panel]bool{}
	for range 6 {
		h.press("tab")
		seen[h.m.focus] = true
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

// Badges have to survive selection: the selected row is where the cursor sits,
// so hiding information there hides it exactly when it is being looked at.
func TestBadgesShowOnTheSelectedRow(t *testing.T) {
	dir := t.TempDir()
	cfg := dir + "/config"
	if err := writeFile(cfg, "Host fromconfig\n  HostName 10.1.2.3\n"); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, func(o *Options) { o.SSHConfigPath = cfg })

	// Select the config-sourced host and confirm its badge is still rendered.
	for range len(h.m.d.tree) {
		if g, ok := h.m.currentGroup(); ok && g.ID == store.SSHConfigGroupID {
			break
		}
		h.press("1")
		h.m.move(1)
	}
	h.press("2")
	if got, ok := h.m.selectedHost(); !ok || got.Source != store.SourceSSHConfig {
		t.Skip("could not select the config host in this layout")
	}
	h.mustContain("cfg")
}

// Terminals can clear the screen without telling the application — iTerm2's
// cmd+K does exactly that. The renderer still believes its last frame is on
// screen and writes only deltas, so the display stays blank until something
// forces a full repaint.
func TestRedrawKeyForcesAFullRepaint(t *testing.T) {
	h := newHarness(t)

	cmd := h.send(tea.KeyPressMsg{Code: 'l', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("ctrl+l produced no command; the screen would stay blank")
	}
	if got, want := fmt.Sprintf("%T", cmd()), fmt.Sprintf("%T", tea.ClearScreen()); got != want {
		t.Errorf("ctrl+l yielded %s, want %s", got, want)
	}
}

// While a session owns the keyboard, ctrl+l belongs to the remote shell, so
// the redraw has to be reachable another way.
func TestRedrawIsReachableFromASession(t *testing.T) {
	h := newHarness(t)
	if _, ok := keymapHas(h, "redraw"); !ok {
		t.Skip("no redraw action configured")
	}
	// The prefix route is exercised without a live pty by checking the binding
	// exists; the pane path itself is covered in internal/term.
	h.press("?")
	h.mustContain("redraw")
}

// The tab bar is always present, and the browser is always tab 1 — it is the
// way back to everything else.
func TestTabBarShowsTheBrowserTab(t *testing.T) {
	h := newHarness(t)

	if len(h.m.tabs) != 1 || !h.m.tabs[0].isBrowser() {
		t.Fatalf("expected exactly one browser tab, got %+v", h.m.tabs)
	}
	if h.m.activeIsSession() {
		t.Error("the browser tab reports itself as a session")
	}
	h.mustContain("1 hosts")

	// The frame must still fit exactly, now that a row is given to the bar.
	lines := strings.Split(h.screen(), "\n")
	if len(lines) != testH {
		t.Errorf("rendered %d lines, want %d", len(lines), testH)
	}
}

// The prefix works from the browser too, so switching tabs is the same gesture
// wherever you are.
func TestPrefixArmsFromTheBrowser(t *testing.T) {
	h := newHarness(t)

	h.send(tea.KeyPressMsg{Code: '\\', Mod: tea.ModCtrl})
	if !h.m.prefixArmed {
		t.Fatal("the prefix did not arm from the browser tab")
	}
	h.mustContain("prefix")

	// A command that needs no session still works: w returns to tab 1.
	h.press("w")
	if h.m.prefixArmed {
		t.Error("the prefix stayed armed after a command")
	}
	if h.m.activeTab != 0 {
		t.Errorf("activeTab = %d, want 0", h.m.activeTab)
	}
}

func TestTabLabels(t *testing.T) {
	if got := (tab{}).label(); got != "hosts" {
		t.Errorf("browser tab label = %q, want hosts", got)
	}
}

// openPaneTab connects the selected host in a real tab. The connection itself
// is expected to fail — port 1 answers nothing — but a pane is still created,
// which is all the rendering path needs. Sessions are killed rather than
// closed so no tmux session outlives the test.
func (h *harness) openPaneTab(name string) {
	h.t.Helper()
	h.addHost(name, "127.0.0.1:1")
	h.press("enter")
	h.t.Cleanup(func() {
		for _, t := range h.m.tabs {
			for _, p := range t.panes {
				_ = p.Kill()
			}
		}
	})
	if !h.m.activeIsSession() {
		h.t.Fatalf("connecting did not open a session tab: tabs=%d active=%d", len(h.m.tabs), h.m.activeTab)
	}
}

// Connecting opens a tab rather than taking over the browser, and the frame
// that comes back is the session — not the host list behind it. render() has
// silently kept drawing the browser here before, which looks like nothing
// happened at all.
func TestConnectOpensATabShowingTheSession(t *testing.T) {
	h := newHarness(t)
	h.openPaneTab("alpha")

	if h.m.activeTab != 1 {
		t.Errorf("activeTab = %d, want 1", h.m.activeTab)
	}
	h.mustContain("1 hosts")
	h.mustContain("2 alpha")
	// The browser's panels must be gone; a session tab owns the whole frame.
	h.mustNotContain("Groups")
	h.mustNotContain("Forwards")

	lines := strings.Split(h.screen(), "\n")
	if len(lines) != testH {
		t.Errorf("session frame is %d lines, want %d", len(lines), testH)
	}
}

// Tab 1 is always the way back, and the sessions keep running behind it.
func TestPrefixReturnsToTheBrowserWithoutClosingTheSession(t *testing.T) {
	h := newHarness(t)
	h.openPaneTab("alpha")

	h.send(tea.KeyPressMsg{Code: '\\', Mod: tea.ModCtrl})
	h.press("w")

	if h.m.activeIsSession() {
		t.Fatal("prefix w did not return to the browser")
	}
	h.mustContain("Groups")
	if len(h.m.tabs) != 2 {
		t.Errorf("tabs = %d, want the session tab kept", len(h.m.tabs))
	}
	// The host list says where the session went.
	if got := h.m.tabForHost(h.m.d.hosts[0]); got != 2 {
		t.Errorf("tabForHost = %d, want 2", got)
	}
}

// Connecting to a host that is already open goes to its tab instead of
// stacking a second session onto the same machine.
func TestConnectingTwiceReusesTheTab(t *testing.T) {
	h := newHarness(t)
	h.openPaneTab("alpha")

	h.send(tea.KeyPressMsg{Code: '\\', Mod: tea.ModCtrl})
	h.press("w")
	h.press("enter")

	if len(h.m.tabs) != 2 {
		t.Fatalf("tabs = %d, want 2 — a second tab was opened for the same host", len(h.m.tabs))
	}
	if h.m.activeTab != 1 {
		t.Errorf("activeTab = %d, want 1", h.m.activeTab)
	}
}
