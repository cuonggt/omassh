package ui

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/cuonggt/omassh/internal/store"
)

// writeKey puts something ssh would take for a private key in dir.
func writeKey(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	body := "-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaA\n-----END OPENSSH PRIVATE KEY-----\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// onField tabs to a field by name, since the index it sits at is a fact about
// the form's shape rather than about the field.
func onField(t *testing.T, h *harness, label string) {
	t.Helper()
	for range len(h.m.form.fields) {
		if h.m.form.fields[h.m.form.idx].label == label {
			return
		}
		h.press("tab")
	}
	t.Fatalf("no %s field on this form", label)
}

// The key path was the one field with known values that still had to be typed
// from memory, while the credential, jump host, group and tags beside it all
// offered what you already use.
func TestTheKeyPathOffersTheKeysYouHave(t *testing.T) {
	dir := t.TempDir()
	key := writeKey(t, dir, "id_ed25519")
	h := newHarness(t, func(o *Options) { o.KeyDir = dir })
	h.addHost("web", "10.0.0.2")

	h.press("2", "e")
	onField(t, h, "Identity")
	h.press("down")

	if !h.m.form.picking {
		t.Fatal("down on the key path did not open a picker")
	}
	if got := h.m.form.fields[h.m.form.idx].choices; !slices.Contains(got, key) {
		t.Errorf("offered %q, want the key in the directory (%q)", got, key)
	}
}

// And a key kept somewhere else, typed once, is offered for the next host:
// that is the typing actually worth doing away with.
func TestTheKeyPathOffersPathsAlreadyInUse(t *testing.T) {
	h := newHarness(t)
	if _, err := h.store.PutHost(store.Host{
		Name: "old", Addr: "10.0.0.1", Identity: "~/keys/deploy"}); err != nil {
		t.Fatal(err)
	}
	h.reload()
	h.addHost("web", "10.0.0.2")

	h.selectHost("web")
	h.press("e")
	onField(t, h, "Identity")
	h.press("down")

	if got := h.m.form.fields[h.m.form.idx].choices; !slices.Contains(got, "~/keys/deploy") {
		t.Errorf("offered %q, want the path another host already uses", got)
	}
}

// Picking one fills the field, and a path typed by hand still works: the
// picker is an offer, not the only way to answer.
func TestPickingAKeyFillsTheFieldAndTypingStillWorks(t *testing.T) {
	dir := t.TempDir()
	key := writeKey(t, dir, "id_ed25519")
	h := newHarness(t, func(o *Options) { o.KeyDir = dir })
	h.addHost("web", "10.0.0.2")

	h.press("2", "e")
	onField(t, h, "Identity")
	h.press("down")
	for h.m.form.currentChoice() != key {
		h.press("down")
	}
	h.press("enter")

	if h.m.form.picking {
		t.Error("enter left the picker open")
	}
	if got := h.m.form.value("Identity"); got != key {
		t.Errorf("Identity = %q, want the key that was picked (%q)", got, key)
	}

	// Still a field and not only a list of choices: what is typed reaches it,
	// which is what a key kept somewhere the picker cannot see depends on.
	h.type_("-second")
	if got := h.m.form.value("Identity"); got != key+"-second" {
		t.Errorf("Identity = %q, want what was typed after the pick", got)
	}
}

// A credential names a key path too, and had the same field.
func TestACredentialsKeyPathOffersTheKeysYouHave(t *testing.T) {
	dir := t.TempDir()
	key := writeKey(t, dir, "id_ed25519")
	h := newHarness(t, func(o *Options) { o.KeyDir = dir })

	h.press("C", "n")
	onField(t, h, "Identity")
	h.press("down")

	if got := h.m.form.fields[h.m.form.idx].choices; !slices.Contains(got, key) {
		t.Errorf("offered %q, want the key in the directory (%q)", got, key)
	}
}

// With nowhere to look the suite offers nothing of its own, which is the
// point: a test that read the runner's ~/.ssh would assert on whatever they
// keep in there, and a release once shipped a test that passed only because
// of a key in one developer's.
func TestWithNoKeyDirectoryNothingOfTheMachinesIsOffered(t *testing.T) {
	h := newHarness(t)
	h.addHost("web", "10.0.0.2")

	h.press("2", "e")
	onField(t, h, "Identity")
	h.press("down")

	if got := h.m.form.fields[h.m.form.idx].choices; !slices.Equal(got, []string{noChoice}) {
		t.Errorf("offered %q, want only the empty choice", got)
	}
}
