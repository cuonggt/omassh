package ui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/cuonggt/omassh/internal/keymap"
	"github.com/cuonggt/omassh/internal/sshx"
	"github.com/cuonggt/omassh/internal/store"
)

// addSnippet writes one straight into the store, for tests that need one
// without driving the form for each.
func (h *harness) addSnippet(s store.Snippet) store.Snippet {
	h.t.Helper()
	saved, err := h.store.PutSnippet(s)
	if err != nil {
		h.t.Fatalf("put snippet: %v", err)
	}
	h.reload()
	return saved
}

// ctrlE is the key that opens $EDITOR, which press has no name for.
func (h *harness) ctrlE() {
	h.t.Helper()
	h.send(tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl})
}

func TestTheSnippetListOpensAndCloses(t *testing.T) {
	h := newHarness(t)
	h.addSnippet(store.Snippet{Name: "disk free", Script: "df -h /"})

	h.press("S")
	if h.m.mode != modeSnippets {
		t.Fatalf("mode = %v, want the snippet list", h.m.mode)
	}
	h.mustContain("disk free")
	// The command itself, since it is one line — the row is where you
	// recognise a snippet, and its name alone often will not do it.
	h.mustContain("df -h /")

	h.press("esc")
	if h.m.mode != modeBrowse {
		t.Errorf("mode = %v, want the list closed", h.m.mode)
	}
}

func TestAnEmptySnippetListExplainsItself(t *testing.T) {
	h := newHarness(t)

	h.press("S")
	h.mustContain("no snippets yet")
	h.mustContain("n new")
}

// A script too long for its row is shown by size, so one enormous snippet
// cannot push every other name off the screen.
func TestALongScriptIsListedBySizeRatherThanByItsFirstLine(t *testing.T) {
	h := newHarness(t)
	h.addSnippet(store.Snippet{Name: "rotate certs",
		Script: "set -e\ncertbot renew\nsystemctl reload nginx\n"})

	h.press("S")
	h.mustContain("rotate certs")
	h.mustContain("3 lines")
	h.mustNotContain("certbot renew")
}

func TestANewSnippetIsTypedAndSaved(t *testing.T) {
	h := newHarness(t)
	h.press("S", "n")
	if h.m.form == nil || h.m.form.kind != formSnippet {
		t.Fatal("n did not open a snippet form")
	}

	h.type_("uptime everywhere")
	h.press("tab")
	h.type_("uptime")
	h.press("enter")

	if h.m.mode != modeSnippets {
		t.Fatalf("mode = %v, want back on the list", h.m.mode)
	}
	got, err := h.store.Snippets()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("%d snippets, want 1", len(got))
	}
	if got[0].Name != "uptime everywhere" || got[0].Script != "uptime" {
		t.Errorf("saved %+v, want the name and script typed", got[0])
	}
}

// A snippet needs both halves, and the complaint stays on the form rather
// than closing it and losing what was typed.
func TestASnippetNeedsANameAndAScript(t *testing.T) {
	h := newHarness(t)

	h.press("S", "n")
	h.press("tab")
	h.type_("uptime")
	h.press("enter")
	if h.m.form == nil {
		t.Fatal("a snippet with no name was accepted")
	}
	if !strings.Contains(h.m.form.problem, "name") {
		t.Errorf("problem = %q, want it to ask for a name", h.m.form.problem)
	}

	h.press("shift+tab")
	h.type_("named")
	h.press("tab")
	// Clear the script back out.
	for range len("uptime") {
		h.press("backspace")
	}
	h.press("enter")
	if h.m.form == nil {
		t.Fatal("a snippet with no script was accepted")
	}
	if !strings.Contains(h.m.form.problem, "would do nothing") {
		t.Errorf("problem = %q, want it to say the script is empty", h.m.form.problem)
	}
}

// Editing keeps the record rather than making a second one.
func TestEditingASnippetChangesItInPlace(t *testing.T) {
	h := newHarness(t)
	was := h.addSnippet(store.Snippet{Name: "disk free", Script: "df -h /"})

	h.press("S", "e")
	if h.m.form == nil || h.m.form.editID != was.ID {
		t.Fatal("e did not open the snippet under the cursor")
	}
	h.press("tab")
	h.type_(" | tail -1")
	h.press("enter")

	got, _ := h.store.Snippets()
	if len(got) != 1 {
		t.Fatalf("%d snippets, want the one edited in place", len(got))
	}
	if got[0].ID != was.ID {
		t.Errorf("id = %q, want %q", got[0].ID, was.ID)
	}
	if got[0].Script != "df -h / | tail -1" {
		t.Errorf("script = %q, want the edit", got[0].Script)
	}
}

// Deleting asks first, as deleting anything else here does — and says the
// thing that is true of a snippet and of nothing else in this program.
func TestDeletingASnippetAsksAndSaysNothingElseChanges(t *testing.T) {
	h := newHarness(t)
	h.addSnippet(store.Snippet{Name: "disk free", Script: "df -h /"})

	h.press("S", "d")
	if h.m.mode != modeConfirm {
		t.Fatalf("mode = %v, want a confirmation", h.m.mode)
	}
	h.mustContain("Delete snippet disk free?")
	h.mustContain("nothing else refers to a snippet")

	h.press("y")
	got, _ := h.store.Snippets()
	if len(got) != 0 {
		t.Errorf("%d snippets left after deleting the only one", len(got))
	}
}

// A script that runs to more than a line cannot be held by a single-line
// input, so the field describes it instead — and refuses to be typed into,
// because saving the description as the script is exactly the bug.
func TestAMultiLineScriptIsShownRatherThanTyped(t *testing.T) {
	h := newHarness(t)
	h.addSnippet(store.Snippet{Name: "rotate certs", Script: "set -e\ncertbot renew\n"})

	h.press("S", "e")
	h.press("tab")
	h.mustContain("2 lines — ctrl+e to edit")

	before := h.m.form.script
	h.type_("rm -rf /")
	if h.m.form.script != before {
		t.Errorf("typing changed the script to %q", h.m.form.script)
	}
	h.press("enter")
	got, _ := h.store.Snippets()
	if got[0].Script != "set -e\ncertbot renew\n" {
		t.Errorf("script = %q, want what the editor had put there", got[0].Script)
	}
}

// What $EDITOR wrote becomes the script, exactly — and a script that came
// back down to one line is typeable again.
func TestWhatTheEditorWroteBecomesTheScript(t *testing.T) {
	h := newHarness(t)
	h.press("S", "n")
	h.type_("rotate certs")
	h.press("tab")
	h.ctrlE()

	h.send(scriptEditedMsg{script: "set -e\ncertbot renew\nsystemctl reload nginx\n"})
	if h.m.form.script != "set -e\ncertbot renew\nsystemctl reload nginx\n" {
		t.Fatalf("script = %q, want what the editor wrote", h.m.form.script)
	}
	h.mustContain("3 lines — ctrl+e to edit")

	// Back down to one line, and the field takes typing again.
	h.send(scriptEditedMsg{script: "certbot renew\n"})
	h.mustContain("certbot renew")
	h.type_(" --dry-run")
	if want := "certbot renew --dry-run"; h.m.form.script != want {
		t.Errorf("script = %q, want %q", h.m.form.script, want)
	}
}

// An editor that exited without saving has said nothing about what the script
// should be, so what is on the form stands.
func TestAnEditorThatFailedLeavesTheScriptAlone(t *testing.T) {
	h := newHarness(t)
	h.press("S", "n")
	h.type_("uptime everywhere")
	h.press("tab")
	h.type_("uptime")
	h.ctrlE()

	h.send(scriptEditedMsg{err: errors.New("exit status 1")})
	if h.m.form.script != "uptime" {
		t.Errorf("script = %q, want the one already typed", h.m.form.script)
	}
	if strings.Contains(h.m.form.problem, "exit status") {
		t.Errorf("problem = %q, which is a shell's words rather than ours", h.m.form.problem)
	}
	if !strings.Contains(h.m.form.problem, "as it was") {
		t.Errorf("problem = %q, want it to say the script is unchanged", h.m.form.problem)
	}
}

// The editor is the one already configured on the machine, in the order every
// other program looks for it.
func TestTheEditorComesFromVisualThenEditor(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "echo -n")
	name, args, err := editorCommand()
	if err != nil {
		t.Fatal(err)
	}
	if name != "echo" || len(args) != 1 || args[0] != "-n" {
		// $EDITOR very often carries a flag: "code -w", "emacsclient -nw".
		t.Errorf("editorCommand() = %q %v, want the flag kept", name, args)
	}

	t.Setenv("VISUAL", "echo")
	if name, _, _ := editorCommand(); name != "echo" {
		t.Errorf("name = %q, want VISUAL to win", name)
	}

	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "no-such-editor-anywhere")
	if _, _, err := editorCommand(); err == nil {
		t.Error("an editor that is not installed was accepted")
	} else if !strings.Contains(err.Error(), "EDITOR") {
		t.Errorf("err = %v, want it to say what to set", err)
	}
}

// A one-line script sits in a field that looks perfectly ordinary, so the
// form has to say that a longer one is possible at all. The readonly field
// says it once the script is already too long — which is too late to be how
// anyone finds out.
func TestTheSnippetFormSaysHowToWriteALongerScript(t *testing.T) {
	h := newHarness(t)

	h.press("S", "n")
	h.mustContain("ctrl+e opens $EDITOR")

	// Still said with a one-liner typed in, which is exactly the case where
	// nothing else on the form would mention it.
	h.type_("uptime everywhere")
	h.press("tab")
	h.type_("uptime")
	h.mustContain("ctrl+e opens $EDITOR")

	// And not on the forms that have no script to open.
	h.press("esc", "esc")
	h.press("C", "n")
	h.mustNotContain("ctrl+e")
}

// Help names the key, so the list is findable without reading the README.
func TestHelpNamesTheSnippetKey(t *testing.T) {
	h := newHarness(t)
	h.press("?")
	h.mustContain("snippets: a script to run on a host or a whole group")
}

// Running shows the script and the host first and waits. A long snippet is
// listed by its size, so enter alone would run something the person pressing
// it cannot see, on a machine they have to be right about.
func TestRunningASnippetShowsWhatAndWhereFirst(t *testing.T) {
	h := newHarness(t)
	h.addHost("web-01", "10.0.1.1")
	h.addSnippet(store.Snippet{Name: "restart nginx",
		Script: "set -e\nsystemctl restart nginx\n"})
	h.selectHost("web-01")

	h.press("S", "enter")
	if h.m.mode != modeSnippetRun {
		t.Fatalf("mode = %v, want the run screen", h.m.mode)
	}
	h.mustContain(`Run "restart nginx"`)
	// The whole script, not its size: this is where it is read before it runs.
	h.mustContain("systemctl restart nginx")
	h.mustContain("this host")
	h.mustContain("web-01")
	h.mustContain("no terminal there")
	h.mustContain("y run")

	if h.m.running.started {
		t.Error("the run started before it was confirmed")
	}
}

// esc there runs nothing and goes back to the list.
func TestEscapingTheRunScreenRunsNothing(t *testing.T) {
	h := newHarness(t)
	h.addHost("web-01", "10.0.1.1")
	h.addSnippet(store.Snippet{Name: "uptime", Script: "uptime"})
	h.selectHost("web-01")

	h.press("S", "enter", "esc")
	if h.m.mode != modeSnippets {
		t.Errorf("mode = %v, want back on the list", h.m.mode)
	}
	if h.m.running != nil {
		t.Error("a run was left behind by cancelling")
	}
}

// startRun puts the run in flight without actually spawning ssh: pressing y
// returns the command that would, and the harness does not run commands.
func (h *harness) startRun() {
	h.t.Helper()
	h.press("y")
	if !h.m.running.started {
		h.t.Fatal("y did not start the run")
	}
}

// begin says a host's ssh has been started, which a worker does as the limit
// lets each one through.
func (h *harness) begin(hosts ...store.Host) {
	h.t.Helper()
	for _, host := range hosts {
		h.send(snippetEvent{token: h.m.runToken, host: host, began: true})
	}
}

// report delivers one host's result. It begins the host first, because a host
// cannot finish without having started.
func (h *harness) report(host store.Host, res sshx.Result) {
	h.t.Helper()
	h.begin(host)
	h.send(snippetEvent{token: h.m.runToken, host: host, result: res})
}

// finishRun delivers one host's result and the end of the run.
func (h *harness) finishRun(host store.Host, res sshx.Result) {
	h.t.Helper()
	h.report(host, res)
	h.send(snippetEvent{token: h.m.runToken, done: true})
}

func TestWhatAScriptSaidIsShownWhenItFinishes(t *testing.T) {
	h := newHarness(t)
	web := h.addHost("web-01", "10.0.1.1")
	h.addSnippet(store.Snippet{Name: "disk free", Script: "df -h /"})
	h.selectHost("web-01")

	h.press("S", "enter")
	h.startRun()
	h.begin(web)
	h.mustContain("web-01")
	h.mustContain("running…")
	h.mustContain("esc stop")

	h.finishRun(web, sshx.Result{
		Output:   "Filesystem  Size  Used\n/dev/sda1   40G   12G\n",
		Duration: 2 * time.Second,
	})
	h.mustContain("disk free on web-01")
	h.mustContain("ok in 2s")
	h.mustNotContain("exit ")
	h.mustContain("/dev/sda1   40G   12G")
	// One host needs no table behind it, so esc leaves rather than going back
	// to a list of one.
	h.mustContain("esc close")
}

// A script that failed says so with its own status, and what it wrote is
// still what is on screen — that is where the reason is.
func TestAFailedScriptShowsItsStatusAndWhatItSaid(t *testing.T) {
	h := newHarness(t)
	db := h.addHost("db-01", "10.0.2.1")
	h.addSnippet(store.Snippet{Name: "restart nginx", Script: "systemctl restart nginx"})
	h.selectHost("db-01")

	h.press("S", "enter")
	h.startRun()
	h.finishRun(db, sshx.Result{
		Output:   "sudo: a password is required\n",
		ExitCode: 1,
	})
	h.mustContain("exit 1")
	h.mustContain("sudo: a password is required")
}

// A run stopped from here is not the script failing, and the screen says
// stopped rather than putting a status on it.
func TestAStoppedRunSaysStoppedRatherThanFailed(t *testing.T) {
	h := newHarness(t)
	web := h.addHost("web-01", "10.0.1.1")
	h.addSnippet(store.Snippet{Name: "slow", Script: "sleep 300"})
	h.selectHost("web-01")

	h.press("S", "enter")
	h.startRun()
	h.press("esc")
	if h.m.mode != modeSnippetRun {
		t.Fatalf("esc left the screen, taking what it had already said with it")
	}
	h.mustContain("esc again to leave without waiting")
	h.finishRun(web, sshx.Result{
		Output: "still going\n",
		Err:    context.Canceled,
	})
	h.mustContain("stopped")
	h.mustContain("still going")
	h.mustNotContain("exit ")
}

// A result belonging to a run already closed is ignored. Its ssh was still on
// its way back when the screen was shut, and nothing on screen is its.
func TestAResultFromAClosedRunIsIgnored(t *testing.T) {
	h := newHarness(t)
	web := h.addHost("web-01", "10.0.1.1")
	h.addSnippet(store.Snippet{Name: "uptime", Script: "uptime"})
	h.selectHost("web-01")

	h.press("S", "enter")
	h.startRun()
	stale := h.m.runToken
	// The first esc stops it; the second leaves without waiting for a result
	// that may never come.
	h.press("esc", "esc")
	if h.m.mode != modeSnippets {
		t.Fatalf("mode = %v, want to have left the run screen", h.m.mode)
	}
	h.press("enter")

	h.send(snippetEvent{token: stale, host: web, result: sshx.Result{Output: "from the run that was closed"}})
	h.mustNotContain("from the run that was closed")
	if len(h.m.running.results) != 0 {
		t.Error("a stale result was taken as this run's")
	}
}

// Output longer than the screen scrolls, and what is not on it is accounted
// for rather than simply absent.
func TestLongOutputScrollsAndSaysHowMuchIsLeft(t *testing.T) {
	h := newHarness(t)
	web := h.addHost("web-01", "10.0.1.1")
	h.addSnippet(store.Snippet{Name: "lots", Script: "seq 100"})
	h.selectHost("web-01")

	var sb strings.Builder
	for i := 1; i <= 100; i++ {
		fmt.Fprintf(&sb, "line %d\n", i)
	}
	h.press("S", "enter")
	h.startRun()
	h.finishRun(web, sshx.Result{Output: sb.String()})

	h.mustContain("line 1")
	h.mustContain("more line")
	top := h.screen()
	h.press("j", "j", "j")
	if h.screen() == top {
		t.Error("j did not scroll the output")
	}
	h.press("g")
	if h.m.running.scroll != 0 {
		t.Errorf("scroll = %d, want g to go back to the top", h.m.running.scroll)
	}
}

// Output past what omassh keeps is said to be missing rather than quietly
// ending mid-line.
func TestTruncatedOutputSaysSo(t *testing.T) {
	h := newHarness(t)
	web := h.addHost("web-01", "10.0.1.1")
	h.addSnippet(store.Snippet{Name: "lots", Script: "cat /dev/urandom"})
	h.selectHost("web-01")

	h.press("S", "enter")
	h.startRun()
	h.finishRun(web, sshx.Result{Output: "the beginning\n", Truncated: true})
	h.mustContain("more than omassh keeps")
}

// A script that said nothing at all says that, rather than leaving an empty
// box that looks like something went wrong on the way back.
func TestAScriptThatSaidNothingSaysSo(t *testing.T) {
	h := newHarness(t)
	web := h.addHost("web-01", "10.0.1.1")
	h.addSnippet(store.Snippet{Name: "quiet", Script: "true"})
	h.selectHost("web-01")

	h.press("S", "enter")
	h.startRun()
	h.finishRun(web, sshx.Result{})
	h.mustContain("it said nothing")
}

// A run whose result never comes back must not hold the screen. Cancelling
// kills ssh, but Wait does not return until the output pipes close, and a
// background process the script started keeps them open after ssh is gone.
func TestASecondEscLeavesARunThatWillNotComeBack(t *testing.T) {
	h := newHarness(t)
	h.addHost("web-01", "10.0.1.1")
	h.addSnippet(store.Snippet{Name: "daemonises", Script: "(sleep 300 &)"})
	h.selectHost("web-01")

	h.press("S", "enter")
	h.startRun()
	h.press("esc")
	if h.m.mode != modeSnippetRun {
		t.Fatal("the first esc left rather than stopping")
	}
	h.press("esc")
	if h.m.mode != modeSnippets {
		t.Errorf("mode = %v, want to be back on the list with no result", h.m.mode)
	}
	if h.m.running != nil {
		t.Error("the run was left behind")
	}
}

// A script that finished in under a second says "ok", not "ok in 0s" — which
// reads as a measurement that failed rather than as a script that was quick.
func TestAQuickScriptDoesNotReportZeroSeconds(t *testing.T) {
	h := newHarness(t)
	web := h.addHost("web-01", "10.0.1.1")
	h.addSnippet(store.Snippet{Name: "uptime", Script: "uptime"})
	h.selectHost("web-01")

	h.press("S", "enter")
	h.startRun()
	h.finishRun(web, sshx.Result{Output: "up 3 days\n", Duration: 120 * time.Millisecond})
	h.mustContain("uptime on web-01  ·  ok")
	h.mustNotContain("0s")
}

// --- running on more than one host -------------------------------------

// threeHosts is a group with hosts in it, for the fan-out tests.
func threeHosts(t *testing.T, h *harness) []store.Host {
	t.Helper()
	g := h.addGroup("Production", "")
	out := []store.Host{
		h.addGroupedHost("web-01", g.ID),
		h.addGroupedHost("web-02", g.ID),
		h.addGroupedHost("db-01", g.ID),
	}
	h.selectGroup("Production")
	return out
}

// The group in front of you is offered by name and by size, so choosing it is
// not a guess about what "this group" currently holds.
func TestAGroupIsOfferedAsAPlaceToRun(t *testing.T) {
	h := newHarness(t)
	threeHosts(t, h)
	h.addSnippet(store.Snippet{Name: "uptime", Script: "uptime"})

	h.press("S", "enter")
	h.mustContain("this host")
	h.mustContain("this group")
	h.mustContain("Production — 3 hosts")
	h.mustContain("pick hosts…")
}

// Under a search the list is the matches and not a group at all, and calling
// that "this group" would invite running a script on a set other than the one
// on screen.
func TestASearchIsNotCalledAGroup(t *testing.T) {
	h := newHarness(t)
	threeHosts(t, h)
	h.addSnippet(store.Snippet{Name: "uptime", Script: "uptime"})

	h.press("/")
	h.type_("web")
	// enter leaves the search box with the search still in force, which is
	// the state this is about.
	h.press("enter")
	h.press("S", "enter")
	h.mustContain("these hosts")
	h.mustNotContain("this group")
}

// The table fills in as hosts finish, and says what is still to come rather
// than showing a list that looks complete.
func TestTheTableFillsInAsHostsFinish(t *testing.T) {
	h := newHarness(t)
	hosts := threeHosts(t, h)
	h.addSnippet(store.Snippet{Name: "uptime", Script: "uptime"})

	h.press("S", "enter")
	h.press("j") // this group
	h.startRun()
	h.begin(hosts...)
	h.mustContain("3 running")

	h.report(hosts[0], sshx.Result{Output: "up 3 days\n"})
	h.mustContain("1 ok")
	h.mustContain("2 running")
	h.mustContain("up 3 days")

	h.report(hosts[1], sshx.Result{Output: "boom\n", ExitCode: 2})
	h.mustContain("1 failed")
	h.mustContain("boom")

	h.report(hosts[2], sshx.Result{Output: "up 1 day\n"})
	h.send(snippetEvent{token: h.m.runToken, done: true})
	h.mustContain("2 ok, 1 failed")
	h.mustContain("esc close")
}

// A host's own output is one keypress away, and esc comes back to the table
// rather than throwing the whole run away.
func TestEnterOpensOneHostsOutputAndEscReturnsToTheTable(t *testing.T) {
	h := newHarness(t)
	hosts := threeHosts(t, h)
	h.addSnippet(store.Snippet{Name: "uptime", Script: "uptime"})

	h.press("S", "enter", "j")
	h.startRun()
	for _, host := range hosts {
		h.report(host, sshx.Result{
			Output: fmt.Sprintf("first line from %s\nsummary from %s\n", host.Name, host.Name),
		})
	}
	h.send(snippetEvent{token: h.m.runToken, done: true})

	// The table is in the order the list is, which is not the order they were
	// added in — so the assertion follows the cursor rather than assuming it.
	under, ok := h.m.running.hostAt(h.m.running.idx)
	if !ok {
		t.Fatal("the table has no host under the cursor")
	}
	h.mustContain("summary from " + under.Name)
	h.press("enter")
	h.mustContain("first line from " + under.Name)
	h.mustContain("esc back")

	h.press("esc")
	if h.m.running == nil {
		t.Fatal("esc from one host's output threw the whole run away")
	}
	if h.m.running.showing != "" {
		t.Error("esc did not go back to the table")
	}
	h.mustContain("summary from " + under.Name)
}

// A set that is none of the ready-made ones is chosen by hand, and what runs
// is what was ticked.
func TestPickingHostsRunsOnTheOnesChosen(t *testing.T) {
	h := newHarness(t)
	threeHosts(t, h)
	h.addSnippet(store.Snippet{Name: "uptime", Script: "uptime"})

	h.press("S", "enter")
	h.press("j", "j") // pick hosts…
	h.press("enter")
	h.mustContain("space toggle")

	h.press("space")      // the first host
	h.press("j", "space") // and the second
	h.mustContain("2 of 3 chosen")
	h.press("enter")
	h.mustContain("chosen")
	h.mustContain("2 hosts")

	h.startRun()
	if n := len(h.m.running.hosts); n != 2 {
		t.Errorf("running on %d hosts, want the 2 that were ticked", n)
	}
}

// Ticking eleven of twelve is otherwise eleven presses, and unticking them
// again is another eleven.
func TestAllOrNoneInThePicker(t *testing.T) {
	h := newHarness(t)
	threeHosts(t, h)
	h.addSnippet(store.Snippet{Name: "uptime", Script: "uptime"})

	h.press("S", "enter", "j", "j", "enter")
	h.press("a")
	h.mustContain("3 of 3 chosen")
	h.press("a")
	h.mustContain("0 of 3 chosen")
}

// Stopping mid-run keeps what the hosts that did finish said. Closing the
// screen instead would take it away at the same moment.
func TestStoppingMidRunKeepsWhatFinished(t *testing.T) {
	h := newHarness(t)
	hosts := threeHosts(t, h)
	h.addSnippet(store.Snippet{Name: "slow", Script: "sleep 300"})

	h.press("S", "enter", "j")
	h.startRun()
	h.report(hosts[0], sshx.Result{Output: "web-01 finished\n"})

	h.press("esc")
	if h.m.mode != modeSnippetRun {
		t.Fatal("the first esc left, taking what had finished with it")
	}
	h.mustContain("web-01 finished")
	h.mustContain("esc again to leave without waiting")

	// The ones caught still running are reported stopped, not failed.
	h.report(hosts[1], sshx.Result{Err: context.Canceled})
	h.report(hosts[2], sshx.Result{Err: context.Canceled})
	h.send(snippetEvent{token: h.m.runToken, done: true})
	h.mustContain("stopped")
	h.mustNotContain("exit ")

	h.press("esc")
	if h.m.mode != modeSnippets {
		t.Errorf("mode = %v, want back on the list once it has finished", h.m.mode)
	}
}

// With no hosts at all there is nowhere to run it, and the list says so
// rather than opening a screen with nothing on it.
func TestRunningWithNoHostsAtAllSaysSo(t *testing.T) {
	h := newHarness(t)
	h.addSnippet(store.Snippet{Name: "uptime", Script: "uptime"})

	h.press("S", "enter")
	if h.m.mode != modeSnippets {
		t.Fatalf("mode = %v, want to have stayed on the list", h.m.mode)
	}
	h.mustContain("no host to run it on")
}

// A sweep runs a few at a time, so most of a large one has not started. A
// table calling every host running would be claiming ssh sessions that are
// not open.
func TestHostsTheSweepHasNotReachedSayQueued(t *testing.T) {
	h := newHarness(t)
	hosts := threeHosts(t, h)
	h.addSnippet(store.Snippet{Name: "uptime", Script: "uptime"})

	h.press("S", "enter", "j")
	h.startRun()
	h.mustContain("3 queued")
	h.mustNotContain("running")

	h.begin(hosts[0])
	h.mustContain("1 running, 2 queued")

	h.report(hosts[0], sshx.Result{Output: "up 3 days\n"})
	h.mustContain("1 ok, 2 queued")
}

// A sweep someone stopped is not a sweep that failed. The rows said "stopped"
// while the title above them counted the same hosts as failures.
func TestAStoppedSweepIsCountedAsStoppedNotFailed(t *testing.T) {
	h := newHarness(t)
	hosts := threeHosts(t, h)
	h.addSnippet(store.Snippet{Name: "slow", Script: "sleep 300"})

	h.press("S", "enter", "j")
	h.startRun()
	h.report(hosts[0], sshx.Result{Output: "done\n"})
	h.press("esc")
	h.report(hosts[1], sshx.Result{Err: context.Canceled})
	h.report(hosts[2], sshx.Result{Err: context.Canceled})
	h.send(snippetEvent{token: h.m.runToken, done: true})

	h.mustContain("1 ok, 2 stopped")
	h.mustNotContain("failed")
}

// --- pasting into a session --------------------------------------------

// The script reaches the shell's edit buffer without the trailing newline an
// editor leaves behind. With it, a remote that has not turned bracketed paste
// on runs the last line the moment it arrives — which is the one surprise
// this half of the feature exists to avoid.
func TestWhatIsPastedDoesNotEndInANewline(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"uptime", "uptime"},
		{"uptime\n", "uptime"},
		{"set -e\nsystemctl restart nginx\n", "set -e\nsystemctl restart nginx"},
		{"echo one\n\n\n", "echo one"},
		// Leading and inner whitespace is the script's own business.
		{"  indented\n", "  indented"},
	} {
		if got := pasteText(tc.in); got != tc.want {
			t.Errorf("pasteText(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// With no session open there is nowhere to paste it, and the list says which
// key opens one rather than doing nothing.
func TestPastingWithNoSessionSaysWhichKeyOpensOne(t *testing.T) {
	h := newHarness(t)
	h.addHost("web-01", "10.0.1.1")
	h.addSnippet(store.Snippet{Name: "uptime", Script: "uptime"})

	h.press("S", "p")
	if h.m.mode != modeSnippets {
		t.Fatalf("mode = %v, want to have stayed on the list", h.m.mode)
	}
	h.mustContain("no session in the pane")
	h.mustContain(h.m.keys.Key(keymap.Pane))
}

// stillConnected skips the rest of a test whose session has ended underneath
// it. openSession dials a port that answers nothing, so the pane is alive for
// a moment and then is not; anything asserting on a live one has to say so
// next to the assertion rather than at the top of the test.
func (h *harness) stillConnected() {
	h.t.Helper()
	if h.m.attached == nil || !h.m.attached.Alive() {
		h.t.Skip("the session ended before this could reach it")
	}
}

// Pasting leaves you looking at the session, because reading what arrived is
// the point of pasting rather than running.
func TestPastingLeavesYouLookingAtTheSession(t *testing.T) {
	h := newHarness(t)
	h.openSession("alpha")
	// Back to the list first: a focused session takes every key, so S there
	// is typed at the remote rather than opening anything. That is the route
	// a person takes too.
	h.send(tea.KeyPressMsg{Code: '\\', Mod: tea.ModCtrl})
	h.press("w")
	h.addSnippet(store.Snippet{Name: "uptime", Script: "uptime\n"})

	// Checked here rather than at the top: this session is a connection to a
	// port that answers nothing, so it ends on its own, and everything above
	// is time for it to do so. Under -race that is long enough.
	h.stillConnected()
	h.press("S", "p")
	if h.m.mode != modeBrowse {
		t.Errorf("mode = %v, want the snippet list closed", h.m.mode)
	}
	if h.m.focus != panelSession {
		t.Errorf("focus = %v, want the session", h.m.focus)
	}
	// The model's own status rather than the rendered line: a session whose
	// ssh has already exited has a message of its own that the view shows
	// instead, and that is a different piece of behaviour from this one.
	if !strings.Contains(h.m.status, "pasted into alpha") {
		t.Errorf("status = %q, want it to say where the script went", h.m.status)
	}
	if !strings.Contains(h.m.status, "press enter to run it") {
		t.Errorf("status = %q, want it to say the script has not been run", h.m.status)
	}
	if h.m.statusCtx != "uptime" {
		t.Errorf("statusCtx = %q, want the snippet's name", h.m.statusCtx)
	}
}

// What a multi-line paste does depends on the shell at the far end, and
// omassh cannot see which it is. Claiming it is waiting to be run — and being
// wrong, on a shell whose readline predates bracketed paste, which is what
// macOS ships — is how a snippet gets run by surprise.
func TestAMultiLinePasteDoesNotClaimItIsWaiting(t *testing.T) {
	one := pasteResult("uptime", "web-01")
	if !strings.Contains(one, "press enter to run it") {
		t.Errorf("one line: %q, want it to say enter runs it", one)
	}

	many := pasteResult("set -e\ncertbot renew", "web-01")
	if strings.Contains(many, "press enter to run it") {
		t.Errorf("several lines: %q, which promises something that is not known", many)
	}
	if !strings.Contains(many, "may have run it already") {
		t.Errorf("several lines: %q, want it to say to go and look", many)
	}
	for _, got := range []string{one, many} {
		if !strings.Contains(got, "web-01") {
			t.Errorf("%q does not say where it went", got)
		}
	}
}

// A host omassh never reached did not run the script, and saying "exit 255"
// reads as one that ran and failed. 255 is ssh's own failure status, which
// SessionEndedMsg.NeverConnected already treats this way.
func TestAHostThatWasNeverReachedSaysSoRatherThanGivingAStatus(t *testing.T) {
	h := newHarness(t)
	web := h.addHost("web-01", "10.0.1.1")
	h.addSnippet(store.Snippet{Name: "uptime", Script: "uptime"})
	h.selectHost("web-01")

	h.press("S", "enter")
	h.startRun()
	h.finishRun(web, sshx.Result{
		Output:   "ssh: connect to host 10.0.1.1 port 22: Connection refused\n",
		ExitCode: sshx.ConnectionFailed,
	})

	h.mustContain("could not connect")
	h.mustNotContain("exit 255")
	// And ssh's own diagnostic is still what is on screen, since that is the
	// part saying which of the many ways it failed. Only its beginning is
	// asserted: a long line is cut to the width of the box, as every line
	// drawn into one here is.
	h.mustContain("ssh: connect to host")
}

// A status that is the script's own is still shown as one.
func TestAScriptsOwnStatusIsStillANumber(t *testing.T) {
	h := newHarness(t)
	web := h.addHost("web-01", "10.0.1.1")
	h.addSnippet(store.Snippet{Name: "uptime", Script: "uptime"})
	h.selectHost("web-01")

	h.press("S", "enter")
	h.startRun()
	h.finishRun(web, sshx.Result{Output: "nope\n", ExitCode: 3})
	h.mustContain("exit 3")
	h.mustNotContain("could not connect")
}
