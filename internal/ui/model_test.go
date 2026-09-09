package ui

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/cuonggt/omassh/internal/keymap"
	"github.com/cuonggt/omassh/internal/probe"
	"github.com/cuonggt/omassh/internal/sftpx"
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
	for _, p := range []panel{panelGroups, panelHosts} {
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
	}{{"1", panelGroups}, {"2", panelHosts}} {
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

	// The small sizes matter: the sidebar splits a fixed budget between
	// Groups and Hosts, and a terminal short enough to starve one of them
	// must still produce a frame of exactly the right shape.
	for _, size := range [][2]int{{100, 30}, {60, 20}, {180, 50}, {41, 13}, {60, 10}, {40, 8}, {30, 6}, {24, 5}, {20, 3}} {
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
	h := newHarness(t)
	host := h.addHost("waiting", "10.0.0.1")

	// A detached session is waiting, so the row must carry its badge.
	h.m.d.live = map[string]bool{sessionNameFor(host): true}
	h.press("2")
	if got, ok := h.m.selectedHost(); !ok || got.Name != "waiting" {
		t.Fatalf("selected %+v, want the host named waiting", got)
	}
	h.mustContain("●")
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

// openSession connects the selected host for real. The connection itself is
// expected to fail — port 1 answers nothing — but a pane is still created,
// which is all the rendering path needs. Sessions are killed rather than
// closed so no tmux session outlives the test.
func (h *harness) openSession(name string) {
	h.t.Helper()
	h.addHost(name, "127.0.0.1:1")
	h.press("t") // enter hands the terminal over; t is the embedded pane
	h.t.Cleanup(func() {
		if h.m.attached != nil {
			_ = h.m.attached.Kill()
		}
	})
	if h.m.attached == nil {
		h.t.Fatal("connecting did not attach a session")
	}
}

// Connecting shows the session in the main pane and gives it the keyboard,
// with the host list still beside it. render() has silently kept drawing the
// host detail here before, which looks like nothing happened at all.
func TestConnectShowsTheSessionInTheMainPane(t *testing.T) {
	h := newHarness(t)
	h.openSession("alpha")

	if h.m.focus != panelSession {
		t.Errorf("focus = %v, want panelSession", h.m.focus)
	}
	// The sidebar stays: that is the whole point of the main pane.
	h.mustContain("Groups")
	h.mustContain("Hosts")
	// Only the session title says this, so it proves the main pane is the
	// session and not the host detail that would otherwise be there.
	h.mustContain(prefixKey + " w for the host list")
	// "history" belongs to the host detail, which the session has displaced.
	h.mustNotContain("history")

	lines := strings.Split(h.screen(), "\n")
	if len(lines) != testH {
		t.Errorf("frame is %d lines, want %d", len(lines), testH)
	}
}

// The prefix hands the keyboard back without ending the session.
func TestPrefixWReturnsToTheListKeepingTheSession(t *testing.T) {
	h := newHarness(t)
	h.openSession("alpha")

	h.send(tea.KeyPressMsg{Code: '\\', Mod: tea.ModCtrl})
	if !h.m.prefixArmed {
		t.Fatal("the prefix did not arm from a focused session")
	}
	h.press("w")

	if h.m.focus != panelHosts {
		t.Errorf("focus = %v, want panelHosts", h.m.focus)
	}
	if h.m.attached == nil {
		t.Fatal("prefix w ended the session instead of just moving focus")
	}
	// Still connected, so the list marks it and the pane still shows it.
	if !h.m.attachedTo(h.m.d.hosts[0]) {
		t.Error("the connected host is not marked in the list")
	}
	// The session keeps the main pane even unfocused, so the host detail
	// stays displaced.
	h.mustNotContain("history")
}

// Only one session exists at a time, so connecting elsewhere replaces it
// rather than leaving a connection the interface cannot reach.
func TestConnectingElsewhereReplacesTheSession(t *testing.T) {
	h := newHarness(t)
	h.addHost("bravo", "127.0.0.1:1")
	h.openSession("alpha")
	first := h.m.attached

	h.send(tea.KeyPressMsg{Code: '\\', Mod: tea.ModCtrl})
	h.press("w")
	h.press("j")
	h.press("t")
	t.Cleanup(func() {
		if h.m.attached != nil {
			_ = h.m.attached.Kill()
		}
	})

	if h.m.attached == first {
		t.Fatal("connecting to a second host did not replace the session")
	}
	if got := h.m.attached.Host.Name; got != "bravo" {
		t.Errorf("attached to %q, want bravo", got)
	}
}

// tab must not land on an empty main pane when nothing is connected.
func TestPanelCycleSkipsTheSessionUntilConnected(t *testing.T) {
	h := newHarness(t)
	for range int(numPanels) + 1 {
		h.press("tab")
		if h.m.focus == panelSession {
			t.Fatal("tab reached the session panel with nothing connected")
		}
	}

	h.openSession("alpha")
	h.send(tea.KeyPressMsg{Code: '\\', Mod: tea.ModCtrl})
	h.press("w")

	seen := false
	for range int(numPanels) {
		h.press("tab")
		if h.m.focus == panelSession {
			seen = true
		}
	}
	if !seen {
		t.Error("tab never reached the session panel while connected")
	}
}

// The group split is gone. T was its key in v0.2 and now opens the theme
// picker, so what this guards is the action rather than the letter: a feature
// removed on purpose should not reappear because a binding was left behind.
func TestGroupSplitIsUnbound(t *testing.T) {
	for _, name := range keymap.Names() {
		if strings.Contains(name, "split") || strings.Contains(name, "broadcast") {
			t.Errorf("%q is bound again — the group split was meant to go", name)
		}
	}
}

// enter is the full-screen handoff, not the embedded pane. The two were the
// other way round for a while, and the difference is invisible in a unit test
// unless it is asserted: the handoff opens no pane at all.
func TestEnterHandsTheTerminalOverRatherThanOpeningAPane(t *testing.T) {
	h := newHarness(t)
	h.addHost("web", "10.0.0.1")

	h.press("2")
	cmd := h.send(tea.KeyPressMsg{Code: '\r'})

	if h.m.attached != nil {
		_ = h.m.attached.Kill()
		t.Fatal("enter opened an embedded pane; it should hand the terminal to ssh")
	}
	if cmd == nil {
		t.Fatal("enter produced no command, so nothing was executed")
	}
	h.mustContain("connecting to web")
}

// A dialog is composited over the list rather than replacing it, so every row
// is rebuilt as left + popup + right. That splice is where a width bug would
// hide: the frame must still be exactly the terminal's shape at every size,
// and the list must still be visible around the dialog.
func TestDialogGeometry(t *testing.T) {
	h := newHarness(t)
	h.addHost("web", "10.0.0.1")

	for _, size := range [][2]int{{100, 30}, {60, 20}, {46, 14}, {180, 50}, {31, 6}} {
		h.send(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		h.press("n") // the new-host form
		if h.m.mode != modeForm {
			t.Fatalf("%dx%d: n did not open a form", size[0], size[1])
		}

		lines := strings.Split(h.screen(), "\n")
		if len(lines) != size[1] {
			t.Errorf("%dx%d rendered %d lines, want %d", size[0], size[1], len(lines), size[1])
		}
		for i, l := range lines {
			if w := ansiWidth(l); w > size[0] {
				t.Errorf("%dx%d line %d is %d wide, want at most %d", size[0], size[1], i, w, size[0])
			}
		}
		// The dialog is an overlay, not a replacement. Only assert that where
		// there is room for context: on a narrow terminal the dialog covers
		// almost everything, and legitimately so.
		if size[0] >= 60 && !h.contains("Groups") {
			t.Errorf("%dx%d: the list vanished behind the dialog", size[0], size[1])
		}
		h.press("esc")
	}
}

// A terminal delivers a paste as one bracketed-paste message, not as key
// presses. Nothing routed that message anywhere, so pasting into a form did
// nothing at all and looked like the clipboard was empty.
func TestPasteFillsTheFocusedFormField(t *testing.T) {
	h := newHarness(t)
	h.press("n") // the new-host form
	if h.m.mode != modeForm {
		t.Fatal("n did not open a form")
	}

	h.send(tea.PasteMsg{Content: "prod-web-01"})
	if got := h.m.form.value("Name"); got != "prod-web-01" {
		t.Fatalf("Name = %q, want the pasted text", got)
	}

	// The next field takes its own paste, so the target follows focus.
	h.press("tab")
	h.send(tea.PasteMsg{Content: "10.0.1.14"})
	if got := h.m.form.value("Address"); got != "10.0.1.14" {
		t.Errorf("Address = %q, want the pasted text", got)
	}
	if got := h.m.form.value("Name"); got != "prod-web-01" {
		t.Errorf("Name changed to %q; the paste went to the wrong field", got)
	}
}

// Copying a host out of a config file or a wiki table brings newlines and tabs
// with it. A single-line field must absorb that rather than break its layout.
func TestPasteOfMultipleLinesStaysOnOneLine(t *testing.T) {
	h := newHarness(t)
	h.press("n")

	h.send(tea.PasteMsg{Content: "one\ntwo\tthree\r\nfour"})

	got := h.m.form.value("Name")
	if strings.ContainsAny(got, "\n\t\r") {
		t.Errorf("field holds control characters: %q", got)
	}
	if got == "" {
		t.Fatal("a multi-line paste was dropped entirely")
	}
	// The frame must still be exactly the terminal's shape.
	lines := strings.Split(h.screen(), "\n")
	if len(lines) != testH {
		t.Errorf("frame is %d lines, want %d", len(lines), testH)
	}
}

// Search is a text input too, and its results must follow a paste.
func TestPasteIntoSearchFiltersImmediately(t *testing.T) {
	h := newHarness(t)
	h.addHost("alpha", "10.0.0.1")
	h.addHost("bravo", "10.0.0.2")

	h.press("/")
	h.send(tea.PasteMsg{Content: "bravo"})

	if got, ok := h.m.selectedHost(); !ok || got.Name != "bravo" {
		t.Errorf("selected %+v after pasting a search, want bravo", got)
	}
}

// The jump host is chosen from the hosts you already have, rather than
// remembered and retyped.
func TestJumpHostIsPickedFromAList(t *testing.T) {
	h := newHarness(t)
	h.addHost("bastion", "10.0.0.1")
	h.addHost("web", "10.0.0.2")

	h.press("2")
	h.press("e") // edit the selected host
	if h.m.mode != modeForm {
		t.Fatal("e did not open a form")
	}
	for h.m.form.fields[h.m.form.idx].label != "Jump host" {
		h.press("tab")
	}

	h.press("down") // opens the picker rather than leaving the field
	if !h.m.form.picking {
		t.Fatal("down on the jump host field did not open the picker")
	}
	if h.m.form.fields[h.m.form.idx].label != "Jump host" {
		t.Fatal("down moved to the next field instead of opening the picker")
	}

	// Walk to a real host and take it.
	h.press("down")
	want := h.m.form.currentChoice()
	if want == noChoice {
		h.press("down")
		want = h.m.form.currentChoice()
	}
	h.press("enter")

	if h.m.form.picking {
		t.Error("enter left the picker open")
	}
	if got := h.m.form.value("Jump host"); got != want {
		t.Errorf("Jump host = %q, want %q", got, want)
	}
}

// A host cannot jump through itself; offering it would only build a
// connection that hangs.
func TestJumpHostPickerExcludesTheHostBeingEdited(t *testing.T) {
	h := newHarness(t)
	h.addHost("only", "10.0.0.1")

	h.press("2", "e")
	for h.m.form.fields[h.m.form.idx].label != "Jump host" {
		h.press("tab")
	}
	for _, c := range h.m.form.fields[h.m.form.idx].choices {
		if c == "only" {
			t.Fatal("the host being edited is offered as its own jump host")
		}
	}
}

// The list must never trap the keyboard: esc dismisses it without changing
// the value, and an ordinary keystroke dismisses it and is typed.
func TestPickerDoesNotTrapTheKeyboard(t *testing.T) {
	h := newHarness(t)
	h.addHost("bastion", "10.0.0.1")
	h.addHost("web", "10.0.0.2")

	openPicker := func() {
		h.press("2", "e")
		for h.m.form.fields[h.m.form.idx].label != "Jump host" {
			h.press("tab")
		}
		h.press("down")
		if !h.m.form.picking {
			h.t.Fatal("the picker did not open")
		}
	}

	openPicker()
	h.press("esc")
	if h.m.form.picking {
		t.Error("esc left the picker open")
	}
	if got := h.m.form.value("Jump host"); got != "" {
		t.Errorf("esc changed the value to %q", got)
	}
	if h.m.mode != modeForm {
		t.Error("esc closed the whole form rather than just the list")
	}
	h.press("esc") // now leave the form

	openPicker()
	h.type_("z")
	if h.m.form.picking {
		t.Error("typing left the picker open")
	}
	if got := h.m.form.value("Jump host"); got != "z" {
		t.Errorf("Jump host = %q, want the typed character", got)
	}
}

// The Group field arrives pre-filled with the group you are standing in.
// Typing used to append to that, so one stray keystroke over "Fleet" produced
// "FleetFleet" — and since unknown group names are created on save, that made
// a whole group out of a typo.
func TestTypingReplacesTheSuggestedGroup(t *testing.T) {
	h := newHarness(t)
	h.addHost("first", "10.0.0.1")

	// Put the host in a group, then stand in it so the suggestion appears.
	h.press("e")
	for h.m.form.fields[h.m.form.idx].label != "Group" {
		h.press("tab")
	}
	h.type_("Fleet")
	h.press("enter")

	h.press("n") // new host, standing in Fleet
	for h.m.form.fields[h.m.form.idx].label != "Group" {
		h.press("tab")
	}
	if got := h.m.form.value("Group"); got != "Fleet" {
		t.Fatalf("Group = %q, want the current group suggested", got)
	}

	h.type_("Staging")
	if got := h.m.form.value("Group"); got != "Staging" {
		t.Errorf("Group = %q, want the suggestion replaced", got)
	}
}

// Arriving with an arrow key means you meant to edit what is there, not
// discard it.
func TestNavigatingKeepsTheSuggestedGroup(t *testing.T) {
	h := newHarness(t)
	h.addHost("first", "10.0.0.1")

	h.press("e")
	for h.m.form.fields[h.m.form.idx].label != "Group" {
		h.press("tab")
	}
	h.type_("Fleet")
	h.press("enter")

	h.press("n")
	for h.m.form.fields[h.m.form.idx].label != "Group" {
		h.press("tab")
	}
	h.press("left") // move within the value rather than typing over it
	h.type_("X")
	if got := h.m.form.value("Group"); got != "FleeXt" {
		t.Errorf("Group = %q, want the value edited in place", got)
	}
}

// A paste is text too, so it replaces rather than appending.
func TestPasteReplacesTheSuggestedGroup(t *testing.T) {
	h := newHarness(t)
	h.addHost("first", "10.0.0.1")

	h.press("e")
	for h.m.form.fields[h.m.form.idx].label != "Group" {
		h.press("tab")
	}
	h.type_("Fleet")
	h.press("enter")

	h.press("n")
	for h.m.form.fields[h.m.form.idx].label != "Group" {
		h.press("tab")
	}
	h.send(tea.PasteMsg{Content: "Production"})
	if got := h.m.form.value("Group"); got != "Production" {
		t.Errorf("Group = %q, want the pasted text alone", got)
	}
}

// Jump host is pre-filled when editing and has a picker, so it carries the
// same trap as Group: typing over "bastion" would otherwise append to it.
func TestTypingReplacesTheExistingJumpHost(t *testing.T) {
	h := newHarness(t)
	h.addHost("bastion", "10.0.0.1")
	h.addHost("edge", "10.0.0.2")
	h.addHost("web", "10.0.0.3")

	// Give web a jump host, then reopen it for editing.
	h.press("2")
	for i := range h.m.d.hosts {
		if h.m.d.hosts[i].Name == "web" {
			h.m.hostIdx = i
		}
	}
	h.press("e")
	for h.m.form.fields[h.m.form.idx].label != "Jump host" {
		h.press("tab")
	}
	h.type_("bastion")
	h.press("enter")

	h.press("e")
	for h.m.form.fields[h.m.form.idx].label != "Jump host" {
		h.press("tab")
	}
	if got := h.m.form.value("Jump host"); got != "bastion" {
		t.Fatalf("Jump host = %q, want the saved value", got)
	}

	h.type_("edge")
	if got := h.m.form.value("Jump host"); got != "edge" {
		t.Errorf("Jump host = %q, want the existing value replaced", got)
	}
}

// A new host has no jump host yet, so there is nothing to replace and the
// field must simply accept what is typed.
func TestNewHostJumpFieldHasNothingToReplace(t *testing.T) {
	h := newHarness(t)
	h.addHost("bastion", "10.0.0.1")

	h.press("n")
	for h.m.form.fields[h.m.form.idx].label != "Jump host" {
		h.press("tab")
	}
	h.type_("edge")
	if got := h.m.form.value("Jump host"); got != "edge" {
		t.Errorf("Jump host = %q, want the typed text", got)
	}
}

// A sweep that dialled nothing used to read "0 up, 0 down", which looks like
// the probe failed rather than like it declined on purpose.
func TestProbeSummaryNamesSkippedHosts(t *testing.T) {
	cases := []struct {
		name   string
		counts map[probe.State]int
		want   string
	}{
		{"all skipped", map[probe.State]int{probe.Skipped: 2},
			"2 skipped — a host behind a jump host is not dialled directly"},
		{"mixed keeps the counts uncluttered", map[probe.State]int{probe.Up: 3, probe.Skipped: 1},
			"3 up · 1 skipped"},
		{"every bucket", map[probe.State]int{probe.Up: 1, probe.Down: 2, probe.Skipped: 3, probe.Unknown: 4},
			"1 up · 2 down · 3 skipped · 4 no address"},
		{"nothing at all", map[probe.State]int{}, "nothing probed"},
	}
	for _, c := range cases {
		if got := probeSummary(c.counts); got != c.want {
			t.Errorf("%s: probeSummary = %q, want %q", c.name, got, c.want)
		}
	}
}

// The tally is per sweep. Summing every result ever seen would report on
// hosts in groups this sweep never touched.
func TestProbeCountsResetBetweenSweeps(t *testing.T) {
	h := newHarness(t)
	h.addHost("one", "10.0.0.1")

	h.press("2")
	h.press("p")
	h.m.probeCounts[probe.Up] = 7 // as if a previous group had been probed
	h.press("p")

	if got := h.m.probeCounts[probe.Up]; got != 0 {
		t.Errorf("probeCounts[Up] = %d at the start of a sweep, want 0", got)
	}
}

// Groups are picked from the ones you already have, the same as jump hosts —
// otherwise every host means retyping a name exactly, and a typo silently
// creates a group.
func TestGroupIsPickedFromAList(t *testing.T) {
	h := newHarness(t)
	h.addHost("first", "10.0.0.1")

	// Create two groups by naming them on a host.
	h.press("e")
	for h.m.form.fields[h.m.form.idx].label != "Group" {
		h.press("tab")
	}
	h.type_("Fleet")
	h.press("enter")
	h.press("e")
	for h.m.form.fields[h.m.form.idx].label != "Group" {
		h.press("tab")
	}
	h.type_("Staging")
	h.press("enter")

	h.press("e")
	for h.m.form.fields[h.m.form.idx].label != "Group" {
		h.press("tab")
	}
	h.press("down")
	if !h.m.form.picking {
		t.Fatal("down on the group field did not open a picker")
	}

	got := h.m.form.fields[h.m.form.idx].choices
	for _, want := range []string{noChoice, "Fleet", "Staging"} {
		if !slices.Contains(got, want) {
			t.Errorf("choices = %v, want it to contain %q", got, want)
		}
	}
}

// The empty choice is how a host is left ungrouped.
func TestPickingNoGroupClearsTheField(t *testing.T) {
	h := newHarness(t)
	h.addHost("first", "10.0.0.1")

	h.press("e")
	for h.m.form.fields[h.m.form.idx].label != "Group" {
		h.press("tab")
	}
	h.type_("Fleet")
	h.press("enter")

	h.press("e")
	for h.m.form.fields[h.m.form.idx].label != "Group" {
		h.press("tab")
	}
	h.press("down") // opens on the current value
	h.pickUntil(noChoice)
	h.press("enter")

	if got := h.m.form.value("Group"); got != "" {
		t.Errorf("Group = %q, want empty so the host is ungrouped", got)
	}
}

// Typing still creates a group; the picker is a shortcut, not a restriction.
func TestGroupStaysFreeText(t *testing.T) {
	h := newHarness(t)
	h.addHost("first", "10.0.0.1")

	h.press("e")
	for h.m.form.fields[h.m.form.idx].label != "Group" {
		h.press("tab")
	}
	h.type_("BrandNew")
	h.press("enter")

	h.mustContain("BrandNew")
}

// Tags hold a set, so picking adds to the list rather than replacing it —
// opening the picker again is how a second tag is added.
func TestPickingATagAppends(t *testing.T) {
	h := newHarness(t)
	h.addHost("first", "10.0.0.1")

	// Give the vocabulary something to offer.
	h.press("e")
	for h.m.form.fields[h.m.form.idx].label != "Tags" {
		h.press("tab")
	}
	h.type_("prod, web")
	h.press("enter")

	h.press("e")
	for h.m.form.fields[h.m.form.idx].label != "Tags" {
		h.press("tab")
	}
	h.m.form.fields[h.m.form.idx].input.SetValue("prod")

	// Pick "web" out of the list and expect it added, not substituted.
	h.press("down")
	h.pickUntil("web")
	h.press("space")

	if got := h.m.form.value("Tags"); got != "prod, web" {
		t.Errorf("Tags = %q, want prod, web", got)
	}
}

// One key both selects and deselects, so a tag added by mistake comes off the
// same way it went on.
func TestTogglingATagAddsThenRemovesIt(t *testing.T) {
	if got := toggleInList("", "prod"); got != "prod" {
		t.Errorf("toggle onto an empty field = %q, want prod", got)
	}
	if got := toggleInList("prod,web", "api"); got != "prod, web, api" {
		t.Errorf("toggle = %q, want the list normalised and extended", got)
	}
	if got := toggleInList("prod, web", "web"); got != "prod" {
		t.Errorf("toggle of a present entry = %q, want it removed", got)
	}
	if got := toggleInList("prod", "prod"); got != "" {
		t.Errorf("toggle of the only entry = %q, want empty", got)
	}
}

// A set has no "none" entry: toggling every tag off is what emptying it
// means, and an entry you toggle to mean "nothing" reads as a tag of its own.
func TestTagPickerHasNoEmptyChoice(t *testing.T) {
	h := newHarness(t)
	h.addHost("a", "10.0.0.1")

	h.press("e")
	for h.m.form.fields[h.m.form.idx].label != "Tags" {
		h.press("tab")
	}
	h.type_("prod")
	h.press("enter")

	h.press("e")
	for h.m.form.fields[h.m.form.idx].label != "Tags" {
		h.press("tab")
	}
	if got := h.m.form.fields[h.m.form.idx].choices; slices.Contains(got, noChoice) {
		t.Errorf("tag choices = %v, want no empty entry", got)
	}
	// A single-value picker keeps it, since that is how it is cleared.
	for h.m.form.fields[h.m.form.idx].label != "Jump host" {
		h.press("shift+tab")
	}
	if got := h.m.form.fields[h.m.form.idx].choices; !slices.Contains(got, noChoice) {
		t.Errorf("jump host choices = %v, want the empty entry kept", got)
	}
}

// The vocabulary is what is already in use, deduplicated across hosts.
func TestTagChoicesAreTheTagsInUse(t *testing.T) {
	h := newHarness(t)
	h.addHost("a", "10.0.0.1")
	h.press("e")
	for h.m.form.fields[h.m.form.idx].label != "Tags" {
		h.press("tab")
	}
	h.type_("prod, web")
	h.press("enter")

	h.addHost("b", "10.0.0.2")
	h.selectHost("b")
	h.press("e")
	for h.m.form.fields[h.m.form.idx].label != "Tags" {
		h.press("tab")
	}
	h.type_("prod, api")
	h.press("enter")

	got := h.m.hostChoices("").tags
	want := []string{"api", "prod", "web"}
	if !slices.Equal(got, want) {
		t.Errorf("tags = %v, want %v", got, want)
	}
}

// click puts a mouse click at a screen cell.
func (h *harness) click(x, y int) {
	h.t.Helper()
	h.send(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
}

// Clicking a row selects it, and moves focus to the panel it belongs to, so
// the keys that follow act on what was clicked.
func TestClickSelectsAHost(t *testing.T) {
	h := newHarness(t)
	h.addHost("alpha", "10.0.0.1")
	h.addHost("bravo", "10.0.0.2")
	h.addHost("charlie", "10.0.0.3")

	l := h.m.layout()
	h.click(2, l.groupsH+1+2) // third host row

	if h.m.focus != panelHosts {
		t.Errorf("focus = %v, want panelHosts", h.m.focus)
	}
	got, ok := h.m.selectedHost()
	if !ok || got.Name != "charlie" {
		t.Errorf("selected %+v, want charlie", got)
	}
}

func TestClickSelectsAGroup(t *testing.T) {
	h := newHarness(t)
	h.addHost("first", "10.0.0.1")
	h.press("e")
	for h.m.form.fields[h.m.form.idx].label != "Group" {
		h.press("tab")
	}
	h.type_("Fleet")
	h.press("enter")

	h.click(2, 1) // first group row

	if h.m.focus != panelGroups {
		t.Errorf("focus = %v, want panelGroups", h.m.focus)
	}
	if g, ok := h.m.currentGroup(); !ok || g.Name != "Fleet" {
		t.Errorf("selected group %+v, want Fleet", g)
	}
}

// Borders and the empty space past the end of a list are not rows. Clicking
// them must change nothing rather than snapping to the nearest one.
func TestClickOnNothingSelectsNothing(t *testing.T) {
	h := newHarness(t)
	h.addHost("alpha", "10.0.0.1")
	h.addHost("bravo", "10.0.0.2")
	h.click(2, h.m.layout().groupsH+1+1) // bravo
	before, _ := h.m.selectedHost()

	l := h.m.layout()
	for _, y := range []int{
		l.groupsH,         // the Hosts box's top border
		l.groupsH + 1 + 5, // past the last host
		l.content - 1,     // the bottom border
		l.content,         // the status bar
	} {
		h.click(2, y)
		got, _ := h.m.selectedHost()
		if got.Name != before.Name {
			t.Errorf("clicking row %d moved the selection to %q", y, got.Name)
		}
	}
}

// A dialog covers the list, so a click must not act on what is underneath it.
func TestClickIsIgnoredWhileADialogIsOpen(t *testing.T) {
	h := newHarness(t)
	h.addHost("alpha", "10.0.0.1")
	h.addHost("bravo", "10.0.0.2")

	h.press("n") // a form over the list
	l := h.m.layout()
	h.click(2, l.groupsH+1+1)

	if h.m.mode != modeForm {
		t.Errorf("mode = %v, want the form still open", h.m.mode)
	}
	if h.m.focus == panelHosts && h.m.hostIdx == 1 {
		t.Error("the click reached the list behind the dialog")
	}
}

// Clicking the main pane focuses the session in it, which is the other half
// of what the keyboard's prefix w does.
func TestClickFocusesTheSessionPane(t *testing.T) {
	h := newHarness(t)
	h.openSession("alpha")

	// Hand the keyboard back, then take it again with the mouse.
	h.send(tea.KeyPressMsg{Code: '\\', Mod: tea.ModCtrl})
	h.press("w")
	if h.m.focus == panelSession {
		t.Fatal("prefix w did not leave the session")
	}

	h.click(h.m.layout().side+5, 3)
	if h.m.focus != panelSession {
		t.Errorf("focus = %v, want panelSession", h.m.focus)
	}
}

// With no session the main pane is host detail, and there is nothing there to
// focus — a click must not strand the keyboard on an unfocusable panel.
func TestClickOnAnEmptyMainPaneDoesNothing(t *testing.T) {
	h := newHarness(t)
	h.addHost("alpha", "10.0.0.1")

	h.click(h.m.layout().side+5, 3)
	if h.m.focus == panelSession {
		t.Error("focus moved to the session panel with no session open")
	}
}

// A whole set is chosen in one pass: the list stays open so several tags can
// be toggled without reopening it once per tag.
func TestTagPickerStaysOpenForSeveralTags(t *testing.T) {
	h := newHarness(t)
	h.addHost("a", "10.0.0.1")

	// Establish a vocabulary.
	h.press("e")
	for h.m.form.fields[h.m.form.idx].label != "Tags" {
		h.press("tab")
	}
	h.type_("prod, web, api")
	h.press("enter")

	h.addHost("b", "10.0.0.2")
	h.selectHost("b")
	h.press("e")
	for h.m.form.fields[h.m.form.idx].label != "Tags" {
		h.press("tab")
	}

	h.press("down")
	h.pickUntil("api")
	h.press("space")
	if !h.m.form.picking {
		t.Fatal("the picker closed after one tag; a set needs it to stay open")
	}
	h.pickUntil("web")
	h.press("space")

	if got := h.m.form.value("Tags"); got != "api, web" {
		t.Errorf("Tags = %q, want api, web from one pass", got)
	}

	// Space toggles, so the same entry comes back off.
	h.pickUntil("api")
	h.press("space")
	if got := h.m.form.value("Tags"); got != "web" {
		t.Errorf("Tags = %q, want api toggled back off", got)
	}

	h.press("enter")
	if h.m.form.picking {
		t.Error("enter did not finish the picker")
	}
	if h.m.mode != modeForm {
		t.Error("enter closed the whole form rather than the list")
	}
}

// The list shows what is already chosen, so it reads as a set.
func TestTagPickerMarksChosenTags(t *testing.T) {
	h := newHarness(t)
	h.addHost("a", "10.0.0.1")

	h.press("e")
	for h.m.form.fields[h.m.form.idx].label != "Tags" {
		h.press("tab")
	}
	h.type_("prod, web")
	h.press("enter")

	h.press("e")
	for h.m.form.fields[h.m.form.idx].label != "Tags" {
		h.press("tab")
	}
	h.press("down")

	h.mustContain("✓ prod")
	h.mustContain("✓ web")
}

// A single-value picker still commits and closes: there is nothing more to
// choose once a jump host is set.
func TestSingleValuePickerStillClosesOnChoice(t *testing.T) {
	h := newHarness(t)
	h.addHost("bastion", "10.0.0.1")
	h.addHost("web", "10.0.0.2")
	h.selectHost("web")

	h.press("e")
	for h.m.form.fields[h.m.form.idx].label != "Jump host" {
		h.press("tab")
	}
	h.press("down")
	h.pickUntil("bastion")
	h.press("enter")

	if h.m.form.picking {
		t.Error("the jump host picker stayed open after a choice")
	}
	if got := h.m.form.value("Jump host"); got != "bastion" {
		t.Errorf("Jump host = %q, want bastion", got)
	}
}

// The running build should be identifiable without quitting to ask, so the
// help screen carries the version -version reports.
func TestHelpShowsTheVersion(t *testing.T) {
	h := newHarness(t, func(o *Options) { o.Version = "1.2.3" })
	h.press("?")

	h.mustContain("omassh 1.2.3")
}

// With no version stamped in, the title is the plain name — not the name
// followed by a stray separator where the version would have gone.
func TestHelpWithoutAVersionJustNamesTheApp(t *testing.T) {
	h := newHarness(t)
	h.press("?")

	h.mustContain("omassh  keyboard-driven SSH client")
	h.mustNotContain("omassh   keyboard") // the extra space a bare join leaves
}

// The window follows the cursor rather than centring on it, so moving within
// the visible part does not scroll the whole pane on every keystroke.
func TestListWindowFollowsTheCursor(t *testing.T) {
	cases := []struct {
		name               string
		idx, n, rows       int
		wantStart, wantEnd int
	}{
		{"everything fits", 3, 5, 10, 0, 5},
		{"cursor inside the first window", 2, 100, 10, 0, 10},
		{"cursor at the last visible row", 9, 100, 10, 0, 10},
		{"one past it scrolls by one", 10, 100, 10, 1, 11},
		{"cursor at the end", 99, 100, 10, 90, 100},
		{"cursor at the start", 0, 100, 10, 0, 10},
		{"single row", 42, 100, 1, 42, 43},
		{"empty list", 0, 0, 10, 0, 0},
	}
	for _, c := range cases {
		start, end := listWindow(c.idx, c.n, c.rows)
		if start != c.wantStart || end != c.wantEnd {
			t.Errorf("%s: listWindow(%d,%d,%d) = %d,%d want %d,%d",
				c.name, c.idx, c.n, c.rows, start, end, c.wantStart, c.wantEnd)
		}
		// Whatever it returns, the cursor must be inside it and the slice valid.
		if c.n > 0 && (c.idx < start || c.idx >= end) {
			t.Errorf("%s: cursor %d outside window %d..%d", c.name, c.idx, start, end)
		}
		if start < 0 || end > c.n || start > end {
			t.Errorf("%s: window %d..%d is not a valid slice of %d", c.name, start, end, c.n)
		}
	}
}

// A window must never be larger than the rows available, or the box would cut
// it off again and the cursor could still be the row that got cut.
func TestListWindowNeverExceedsTheRows(t *testing.T) {
	for n := 0; n < 40; n++ {
		for rows := 1; rows < 12; rows++ {
			for idx := 0; idx < max(n, 1); idx++ {
				start, end := listWindow(idx, n, rows)
				if end-start > rows {
					t.Fatalf("listWindow(%d,%d,%d) spans %d rows", idx, n, rows, end-start)
				}
				if n > 0 && (idx < start || idx >= end) {
					t.Fatalf("listWindow(%d,%d,%d) hides the cursor", idx, n, rows)
				}
			}
		}
	}
}

// A host list longer than its panel must scroll, or the selection walks out
// of sight and you cannot see what enter would connect to.
func TestHostListScrollsToKeepTheSelectionVisible(t *testing.T) {
	h := newHarness(t)
	for i := range 40 {
		h.addHost(fmt.Sprintf("host-%02d", i), "10.0.0.1")
	}

	// Walk to the end of the list.
	for range 40 {
		h.press("j")
	}
	if _, ok := h.m.selectedHost(); !ok {
		t.Fatal("nothing selected")
	}
	// The top of the list must have scrolled away. Asserting the selected
	// host is present would not prove anything: its name is also the detail
	// pane's title, so that passes whether the list scrolled or not.
	h.mustNotContain("host-00")
	h.mustContain("host-39")
	h.mustContain("40/40")
}

// Clicking must select what is under the pointer, which is an offset into the
// visible window once the list has scrolled — not into the list itself.
func TestClickSelectsTheRightHostAfterScrolling(t *testing.T) {
	h := newHarness(t)
	for i := range 40 {
		h.addHost(fmt.Sprintf("host-%02d", i), "10.0.0.1")
	}
	for range 40 {
		h.press("j") // scroll to the bottom
	}

	l := h.m.layout()
	h.click(2, l.groupsH+1) // the first visible row

	got, _ := h.m.selectedHost()
	if got.Name == "host-00" {
		t.Fatal("the click mapped to the first host in the list, not the first visible row")
	}
	h.mustContain(got.Name)
}

// sftpHarness puts the model into the file browser with two populated panes,
// without needing a server: only the entries and the cursor drive selection.
func sftpHarness(t *testing.T, left, right int) *harness {
	t.Helper()
	h := newHarness(t)
	h.m.mode = modeSFTP
	h.m.paneFocus = 1 // where sftpConnected leaves it
	for i, n := range []int{left, right} {
		// A rendered pane asks its filesystem for a label, so it needs one
		// even when the test only cares about the cursor.
		h.m.panes[i].fs = fakeFS{}
		h.m.panes[i].path = "/"
		h.m.panes[i].entries = make([]sftpx.Entry, n)
		for j := range h.m.panes[i].entries {
			h.m.panes[i].entries[j].Name = fmt.Sprintf("p%d-file-%02d", i, j)
		}
	}
	return h
}

// tab and shift+tab both move between the panes. There are only two, so the
// direction does not matter — what matters is that the key people reach for
// to go back does not do nothing.
func TestSFTPShiftTabSwitchesPanes(t *testing.T) {
	h := sftpHarness(t, 5, 5)

	h.press("shift+tab")
	if h.m.paneFocus != 0 {
		t.Errorf("paneFocus = %d after shift+tab, want 0", h.m.paneFocus)
	}
	h.press("shift+tab")
	if h.m.paneFocus != 1 {
		t.Errorf("paneFocus = %d after a second shift+tab, want 1", h.m.paneFocus)
	}
}

// Clicking a file selects it, and focuses the pane it is in.
func TestClickSelectsAFileAndItsPane(t *testing.T) {
	h := sftpHarness(t, 20, 20)

	h.click(2, 3) // the left pane, third row
	if h.m.paneFocus != 0 {
		t.Errorf("paneFocus = %d, want the clicked pane", h.m.paneFocus)
	}
	if h.m.panes[0].idx != 2 {
		t.Errorf("left idx = %d, want 2", h.m.panes[0].idx)
	}

	h.click(h.m.w-4, 1) // the right pane, first row
	if h.m.paneFocus != 1 {
		t.Errorf("paneFocus = %d, want the right pane", h.m.paneFocus)
	}
	if h.m.panes[1].idx != 0 {
		t.Errorf("right idx = %d, want 0", h.m.panes[1].idx)
	}
}

// A scrolled pane must select the row that is showing, not the nth entry.
func TestClickInAScrolledFilePane(t *testing.T) {
	h := sftpHarness(t, 200, 5)
	h.m.paneFocus = 0
	h.m.panes[0].idx = 150 // scrolled far down

	h.click(2, 1) // the first visible row
	if got := h.m.panes[0].idx; got == 0 {
		t.Fatal("the click mapped to the first entry rather than the first visible row")
	}
	// It must land inside the window that was on screen.
	rows := max(h.m.h-statusHeight-1-2, 1)
	start, end := listWindow(150, 200, rows)
	if h.m.panes[0].idx < start || h.m.panes[0].idx >= end {
		t.Errorf("idx = %d, outside the visible window %d..%d", h.m.panes[0].idx, start, end)
	}
}

// The borders and the transfer strip are not rows.
func TestClickOnFilePaneChromeDoesNothing(t *testing.T) {
	h := sftpHarness(t, 20, 20)
	h.m.paneFocus = 0
	h.m.panes[0].idx = 4
	before := h.m.panes[0].idx

	content := h.m.h - statusHeight
	for _, y := range []int{0, content - 1, content} { // top border, strip, status
		h.click(2, y)
		if h.m.panes[0].idx != before {
			t.Errorf("clicking row %d moved the cursor to %d", y, h.m.panes[0].idx)
		}
	}
}

// A second click on the same row opens a directory, the way a file browser
// does. Terminals report each press separately with no click count, so this
// is inferred from position and timing.
func TestDoubleClickEntersADirectory(t *testing.T) {
	h := sftpHarness(t, 5, 5)
	h.m.paneFocus = 0
	h.m.panes[0].fs = fakeFS{}
	h.m.panes[0].path = "/start"
	h.m.panes[0].entries[1] = sftpx.Entry{Name: "sub", IsDir: true}

	h.click(2, 2) // selects
	if h.m.panes[0].path != "/start" {
		t.Fatal("one click entered the directory")
	}
	h.click(2, 2) // opens
	if h.m.panes[0].path != "/start/sub" {
		t.Errorf("path = %q, want the directory entered", h.m.panes[0].path)
	}
}

// Two clicks far enough apart in time are two selections, not an open.
func TestSlowSecondClickDoesNotEnter(t *testing.T) {
	h := sftpHarness(t, 5, 5)
	h.m.paneFocus = 0
	h.m.panes[0].fs = fakeFS{}
	h.m.panes[0].path = "/start"
	h.m.panes[0].entries[1] = sftpx.Entry{Name: "sub", IsDir: true}

	h.click(2, 2)
	h.m.lastClick.at = time.Now().Add(-2 * doubleClickWithin) // as if long ago
	h.click(2, 2)

	if h.m.panes[0].path != "/start" {
		t.Errorf("path = %q, want no entry from two slow clicks", h.m.panes[0].path)
	}
}

// Two clicks on different rows are two selections, however fast.
func TestDoubleClickOnADifferentRowDoesNotEnter(t *testing.T) {
	h := sftpHarness(t, 5, 5)
	h.m.paneFocus = 0
	h.m.panes[0].fs = fakeFS{}
	h.m.panes[0].path = "/start"
	h.m.panes[0].entries[1] = sftpx.Entry{Name: "sub", IsDir: true}

	h.click(2, 1)
	h.click(2, 2)
	if h.m.panes[0].path != "/start" {
		t.Errorf("path = %q, want no entry when the rows differ", h.m.panes[0].path)
	}
}

// Double-clicking a plain file selects it and does nothing else.
func TestDoubleClickOnAFileDoesNothing(t *testing.T) {
	h := sftpHarness(t, 5, 5)
	h.m.paneFocus = 0
	h.m.panes[0].fs = fakeFS{}
	h.m.panes[0].path = "/start"

	h.click(2, 2)
	h.click(2, 2)
	if h.m.panes[0].path != "/start" {
		t.Errorf("path = %q, want a file not to open", h.m.panes[0].path)
	}
	if h.m.panes[0].idx != 1 {
		t.Errorf("idx = %d, want the file still selected", h.m.panes[0].idx)
	}
}

// The file browser had two hint lines saying nearly the same thing in
// different words — the transfer strip's and the status bar's — and neither
// listed everything. Only the strip carries them now.
func TestSFTPHasOneHintLine(t *testing.T) {
	h := sftpHarness(t, 5, 5)

	lines := strings.Split(h.screen(), "\n")
	hintRows := 0
	for _, l := range lines {
		if strings.Contains(l, "pane") && strings.Contains(l, "copy") {
			hintRows++
		}
	}
	if hintRows != 1 {
		t.Errorf("%d hint lines, want exactly 1:\n%s", hintRows,
			strings.Join(lines[len(lines)-2:], "\n"))
	}

	// The one that remains is the complete list, not the abbreviated one.
	h.mustContain("mkdir")
	h.mustContain("chmod")
	h.mustContain("⇧tab")
}

// A transfer replaces the keys with its progress; the keys come back after.
func TestTransferReplacesTheHintLine(t *testing.T) {
	h := sftpHarness(t, 5, 5)
	h.mustContain("mkdir")

	h.send(transferMsg{name: "big.iso", done: 512, total: 1024})
	h.mustContain("big.iso")
	h.mustNotContain("mkdir")
}

// deadSession attaches a session and waits for it to end. Port 1 answers
// nothing, so the connection fails on its own.
func (h *harness) deadSession(name string) {
	h.t.Helper()
	h.openSession(name)
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if !h.m.attached.Alive() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	h.t.Skip("the session stayed alive; nothing to test")
}

// Typing into a session that has ended used to detach on the first key and
// send the rest to the host list, where e opens an edit form and the remainder
// of a half-typed command was typed into a host's name — then saved by the
// enter meant for the shell.
func TestTypingIntoAnEndedSessionDoesNotReachTheList(t *testing.T) {
	h := newHarness(t)
	h.deadSession("alpha")

	// The exact keys that renamed a host: "echo hello".
	h.type_("echo hello")
	h.press("enter")

	if h.m.mode == modeForm {
		t.Fatal("a form opened from keys typed at an ended session")
	}
	hosts := h.m.d.hosts
	if len(hosts) != 1 || hosts[0].Name != "alpha" {
		t.Errorf("host is now %+v — the typing reached the list", hosts)
	}
}

// esc is how you leave, and it says so.
func TestEscDismissesAnEndedSession(t *testing.T) {
	h := newHarness(t)
	h.deadSession("alpha")

	h.type_("x") // anything else just explains
	h.mustContain("esc to return to the list")
	if h.m.attached == nil {
		t.Fatal("a stray key dismissed the ended session")
	}

	h.press("esc")
	if h.m.attached != nil {
		t.Error("esc did not dismiss the ended session")
	}
	if h.m.focus == panelSession {
		t.Error("focus stayed on a session that is gone")
	}
}

// The prefix still works on an ended session, so its commands are not lost.
func TestPrefixStillWorksOnAnEndedSession(t *testing.T) {
	h := newHarness(t)
	h.deadSession("alpha")

	h.send(tea.KeyPressMsg{Code: '\\', Mod: tea.ModCtrl})
	if !h.m.prefixArmed {
		t.Fatal("the prefix did not arm on an ended session")
	}
	h.press("w")
	if h.m.focus == panelSession {
		t.Error("prefix w did not return to the host list")
	}
}

func TestTheEndOfHelpIsReachable(t *testing.T) {
	h := newHarness(t)
	h.press("?")
	// The cheatsheet is taller than the terminal, so the last section is not
	// on the first screen of it.
	h.mustContain("Navigate")
	h.mustNotContain("Moving to another machine")

	h.press("G")
	h.mustContain("Moving to another machine")
	h.mustContain("omassh import-ssh-config")
	if h.m.mode != modeHelp {
		t.Fatal("scrolling closed the help screen")
	}

	// Anything that is not a movement key still means done, so help stays a
	// glance rather than a mode to escape from.
	h.press("z")
	if h.m.mode != modeBrowse {
		t.Errorf("mode = %v, want browse after an unrelated key", h.m.mode)
	}
	h.press("?")
	if h.m.helpScroll != 0 {
		t.Errorf("help reopened scrolled to %d, want the top", h.m.helpScroll)
	}
}

func TestHelpSaysWhenThereIsMore(t *testing.T) {
	h := newHarness(t)
	h.press("?")
	// The word appears in the body too — the session section is about
	// scrolling output — so this has to look at the status bar itself, which
	// is the last line of the frame.
	lines := strings.Split(strings.TrimRight(h.screen(), "\n"), "\n")
	if status := lines[len(lines)-1]; !strings.Contains(status, "scroll") {
		t.Errorf("status bar does not offer to scroll: %q", status)
	}
}

func TestTheStatusBarNeverRunsIntoTheStatus(t *testing.T) {
	// The hint line is a fixed width, so at one particular terminal width it
	// ends exactly where the status begins and the two read as one word —
	// "q quit27 hosts". Padding to fill the row is not the same as leaving a
	// gap, so the gap has to be reserved before the hints are measured.
	h := newHarness(t)
	h.addHost("srv-a", "10.0.0.1")
	h.send(tea.WindowSizeMsg{Width: testW, Height: testH})

	for w := minWidth; w <= 200; w++ {
		h.send(tea.WindowSizeMsg{Width: w, Height: testH})
		lines := strings.Split(strings.TrimRight(h.screen(), "\n"), "\n")
		bar := lines[len(lines)-1]

		i := strings.Index(bar, h.m.status)
		if i <= 0 {
			continue // the status itself was truncated, or fills the row
		}
		if bar[i-1] != ' ' {
			t.Fatalf("width %d: hints run into the status: %q", w, strings.TrimSpace(bar))
		}
	}
}

func TestTheSidebarGrowsWithTheFrame(t *testing.T) {
	h := newHarness(t)
	// aaa sorts first and stays selected, so the long name appears only in
	// the host list — the detail pane's title would otherwise show it too and
	// the assertion would pass without the sidebar fitting anything.
	h.addHost("aaa", "10.0.0.1")
	h.addHost("prod-be-capichi-chat-server", "10.0.0.2")

	const long = "prod-be-capichi-chat-server"
	h.send(tea.WindowSizeMsg{Width: 80, Height: testH})
	h.mustNotContain(long) // no room at this width; truncating is right

	h.send(tea.WindowSizeMsg{Width: 120, Height: testH})
	h.mustContain(long) // room to spare, and the list is where it belongs
}

func TestTheSidebarLeavesRoomForThePaneBesideIt(t *testing.T) {
	h := newHarness(t)
	for w := minWidth; w <= 300; w++ {
		h.send(tea.WindowSizeMsg{Width: w, Height: testH})
		side := h.m.sidebar()
		switch {
		case side > sidebarMax:
			t.Fatalf("width %d: sidebar %d is wider than %d", w, side, sidebarMax)
		case side > max(w/2, 1):
			t.Fatalf("width %d: sidebar %d takes more than half the frame", w, side)
		case w-side < 1:
			t.Fatalf("width %d: sidebar %d leaves nothing beside it", w, side)
		}
		// The hit-test has to land on what was drawn, so it cannot work the
		// width out for itself.
		if got := h.m.layout().side; got != side {
			t.Fatalf("width %d: the mouse layout uses %d, the view uses %d", w, got, side)
		}
	}
}

// The prefix was routed only while the session pane had the keyboard, so on
// the host list it was swallowed without a word — and the d that usually
// follows it opened the delete confirmation for the highlighted host instead.
func TestThePrefixReachesTheSessionFromTheHostList(t *testing.T) {
	h := newHarness(t)
	h.openSession("alpha")
	h.press("prefix", "w") // hand the keyboard back, session still running
	if h.m.focus == panelSession {
		t.Fatal("prefix w did not leave the pane")
	}

	h.press("prefix", "d")
	if h.m.mode == modeConfirm {
		t.Fatal("prefix d opened the delete confirmation")
	}
	if h.m.attached != nil {
		t.Error("prefix d on the host list did not detach")
	}
	if len(h.m.d.hosts) != 1 {
		t.Errorf("the host list is now %+v", h.m.d.hosts)
	}
}

func TestEndingTheSessionFromTheHostList(t *testing.T) {
	h := newHarness(t)
	h.openSession("alpha")
	h.press("prefix", "w")

	h.press("prefix", "X")
	if h.m.attached != nil {
		t.Error("prefix X on the host list did not end the session")
	}
	if len(h.m.d.hosts) != 1 {
		t.Errorf("it removed the host too: %+v", h.m.d.hosts)
	}
}

// With nothing to command the prefix says so, rather than doing nothing at
// all and leaving the next key to land somewhere surprising.
func TestThePrefixSaysWhenThereIsNoSession(t *testing.T) {
	h := newHarness(t)
	h.addHost("alpha", "10.0.0.1")
	h.send(tea.WindowSizeMsg{Width: testW, Height: testH})

	h.press("prefix")
	h.mustContain("no session")

	// And it does not arm: the list's own keys still mean what they say.
	h.press("d")
	if h.m.mode != modeConfirm {
		t.Error("d after a lone prefix did not reach the host list")
	}
}

// t on the host already attached is someone coming back to it. Reconnecting
// closes the pane first, and for a plain ssh child that ends the shell.
func TestReturningToTheAttachedSessionDoesNotRestartIt(t *testing.T) {
	h := newHarness(t)
	h.openSession("alpha")
	first := h.m.attached
	h.press("prefix", "w")
	if !h.m.attached.Alive() {
		t.Skip("the session ended before it could be returned to")
	}

	h.press("t")
	if h.m.attached != first {
		t.Error("t built a new session instead of going back to the one running")
	}
	if h.m.focus != panelSession {
		t.Error("t did not give the pane the keyboard")
	}
}

// Renaming means typing the name you want. The field is filled in with the
// current one, and appending to it produced "notes.txtreport.txt" — the same
// failure the Group and Jump host fields were fixed for.
func TestRenamingReplacesTheNameItStartsWith(t *testing.T) {
	h := sftpHarness(t, 3, 3)
	h.m.panes[1].entries = []sftpx.Entry{{Name: "notes.txt", Mode: 0o644}}
	h.m.panes[1].idx = 0

	h.press("r")
	if got := h.m.form.value("Name"); got != "notes.txt" {
		t.Fatalf("the form opened with %q, want the current name", got)
	}
	h.type_("report.txt")
	if got := h.m.form.value("Name"); got != "report.txt" {
		t.Errorf("name = %q, want report.txt", got)
	}
}

func TestChmodReplacesTheModeItStartsWith(t *testing.T) {
	h := sftpHarness(t, 3, 3)
	h.m.panes[1].entries = []sftpx.Entry{{Name: "notes.txt", Mode: 0o644}}
	h.m.panes[1].idx = 0

	h.press("M")
	h.type_("640")
	// "644640" parses as octal too, so appending here does not fail loudly —
	// it quietly asks for a mode nobody meant.
	if got := h.m.form.value("Mode"); got != "640" {
		t.Errorf("mode = %q, want 640", got)
	}
}

// A placeholder shows through the moment a field is emptied, so one repeating
// the value makes a cleared field look like a full one.
func TestAPrefilledFieldsHintIsNotItsValue(t *testing.T) {
	h := sftpHarness(t, 3, 3)
	h.m.panes[1].entries = []sftpx.Entry{{Name: "notes.txt", Mode: 0o644}}
	h.m.panes[1].idx = 0

	for _, key := range []string{"r", "M"} {
		h.press(key)
		f := h.m.form.fields[0]
		if f.hint == f.input.Value() {
			t.Errorf("%s: the hint %q is the value, so an emptied field reads as a full one", key, f.hint)
		}
		h.press("esc")
	}
}
