package ui

import (
	"errors"
	"strings"
	"testing"

	"github.com/cuonggt/omassh/internal/secret"
	"github.com/cuonggt/omassh/internal/store"
)

// addCredential writes one straight into the store, for tests that need one
// without driving the form for each.
func (h *harness) addCredential(c store.Credential) store.Credential {
	h.t.Helper()
	saved, err := h.store.PutCredential(c)
	if err != nil {
		h.t.Fatalf("put credential: %v", err)
	}
	h.reload()
	return saved
}

// The list opens on its own key and closes again.
func TestTheCredentialListOpensAndCloses(t *testing.T) {
	h := newHarness(t)
	h.addCredential(store.Credential{Name: "Prod deploy", Kind: store.CredentialKey,
		User: "deploy", Identity: "~/.ssh/prod"})

	h.press("C")
	if h.m.mode != modeCredentials {
		t.Fatalf("mode = %v, want the credential list", h.m.mode)
	}
	h.mustContain("Prod deploy")
	h.mustContain("deploy · ~/.ssh/prod")
	// The kind reads as a word, the way a forward writes local or remote.
	h.mustContain("key")

	h.press("esc")
	if h.m.mode != modeBrowse {
		t.Errorf("mode = %v, want the list closed", h.m.mode)
	}
}

// With none at all, the list says what one is rather than showing an empty box.
func TestAnEmptyCredentialListExplainsItself(t *testing.T) {
	h := newHarness(t)

	h.press("C")
	h.mustContain("no credentials yet")
	h.mustContain("n new")
}

// A credential shows only the fields its kind uses. Offering a key credential
// a password box invites someone to fill in something that is thrown away.
func TestACredentialFormShowsOnlyTheFieldsItsKindUses(t *testing.T) {
	h := newHarness(t)
	h.press("C")
	h.press("n")

	h.mustContain("Identity")
	h.mustNotContain("Password")

	// Changing the kind changes the form under you.
	h.m.form.fields[1].input.SetValue("password")
	h.m.reshapeCredentialForm()
	h.mustContain("Password")
	h.mustNotContain("Identity")
}

// What has been typed survives the form being rebuilt.
func TestChangingTheKindKeepsWhatWasTyped(t *testing.T) {
	h := newHarness(t)
	h.press("C")
	h.press("n")
	h.m.form.fields[0].input.SetValue("Legacy switch")
	h.m.form.fields[2].input.SetValue("admin")

	h.m.form.fields[1].input.SetValue("password")
	h.m.reshapeCredentialForm()

	if got := h.m.form.value("Name"); got != "Legacy switch" {
		t.Errorf("Name = %q, want it kept", got)
	}
	if got := h.m.form.value("User"); got != "admin" {
		t.Errorf("User = %q, want it kept", got)
	}
}

// A password credential with nothing typed and nothing stored cannot log in,
// and the moment to say so is while the form is still open.
func TestAPasswordCredentialNeedsAPassword(t *testing.T) {
	h := newHarness(t)
	h.press("C")
	h.press("n")
	h.m.form = newCredentialForm(store.Credential{
		Name: "Legacy", Kind: store.CredentialPassword, User: "admin"}, false)

	h.press("enter")
	if h.m.form == nil {
		t.Fatal("it was saved with no password at all")
	}
	if !strings.Contains(h.m.form.problem, "type the password") {
		t.Errorf("problem = %q, want it to ask for one", h.m.form.problem)
	}
}

// The password goes to the keychain and the record goes to the store, and
// neither holds the other's half.
func TestAPasswordGoesToTheKeychainAndNotToTheStore(t *testing.T) {
	h := newHarness(t)
	h.press("C")
	h.press("n")
	h.m.form = newCredentialForm(store.Credential{
		Name: "Legacy", Kind: store.CredentialPassword, User: "admin"}, false)
	h.m.form.fields[3].input.SetValue("hunter2")

	h.press("enter")
	if h.m.form != nil {
		t.Fatalf("refused: %q", h.m.form.problem)
	}

	creds, _ := h.store.Credentials()
	if len(creds) != 1 {
		t.Fatalf("%d credentials, want 1", len(creds))
	}
	// Nothing in the record is the password.
	for _, s := range []string{creds[0].Name, creds[0].User, creds[0].Identity, string(creds[0].Kind)} {
		if strings.Contains(s, "hunter2") {
			t.Errorf("the password is in the stored record: %q", s)
		}
	}
	got, err := h.m.secrets.Get(creds[0].ID)
	if err != nil || got != "hunter2" {
		t.Errorf("keychain has %q (%v), want the password", got, err)
	}
}

// Changed away from a password, the one in the keychain is nobody's: left
// behind it is a secret nothing on the machine can reach or account for.
func TestChangingAwayFromAPasswordTakesItOutOfTheKeychain(t *testing.T) {
	h := newHarness(t)
	c := h.addCredential(store.Credential{Name: "Legacy", Kind: store.CredentialPassword, User: "admin"})
	if err := h.m.secrets.Set(c.ID, "hunter2"); err != nil {
		t.Fatal(err)
	}

	h.press("C")
	h.m.form = newCredentialForm(store.Credential{
		ID: c.ID, Name: "Legacy", Kind: store.CredentialKey,
		User: "admin", Identity: "~/.ssh/k"}, true)
	h.m.mode, h.m.returnTo = modeForm, modeCredentials
	h.press("enter")
	if h.m.form != nil {
		t.Fatalf("refused: %q", h.m.form.problem)
	}

	if _, err := h.m.secrets.Get(c.ID); !errors.Is(err, secret.ErrNotFound) {
		t.Error("the password is still in the keychain for a credential that no longer uses one")
	}
}

// Deleting one says what stops inheriting it, the way deleting a group does,
// and takes the password with it.
func TestDeletingACredentialSaysWhatLosesItAndClearsTheKeychain(t *testing.T) {
	h := newHarness(t)
	c := h.addCredential(store.Credential{Name: "Legacy", Kind: store.CredentialPassword, User: "admin"})
	h.m.secrets.Set(c.ID, "hunter2")
	if _, err := h.store.PutHost(store.Host{Name: "sw", Addr: "10.0.9.1", CredentialID: c.ID}); err != nil {
		t.Fatal(err)
	}
	h.reload()

	h.press("C")
	h.press("d")
	h.mustContain("Delete credential Legacy?")
	h.mustContain("1 host")
	h.mustContain("password goes from your keychain")

	h.press("y")
	if _, err := h.m.secrets.Get(c.ID); !errors.Is(err, secret.ErrNotFound) {
		t.Error("the password outlived the credential")
	}
	creds, _ := h.store.Credentials()
	if len(creds) != 0 {
		t.Errorf("%d credentials left", len(creds))
	}
}

// A password credential with nothing behind it is the one failure nothing else
// on the screen would show, so the list says so.
func TestTheListSaysWhenAPasswordIsNotInTheKeychain(t *testing.T) {
	h := newHarness(t)
	h.addCredential(store.Credential{Name: "Legacy", Kind: store.CredentialPassword, User: "admin"})

	h.press("C")
	h.mustContain("no password in the keychain")
}

// A machine with nowhere to keep one says so rather than accepting a password
// it cannot store.
func TestWithNoKeychainTheFormSaysSoRatherThanLosingThePassword(t *testing.T) {
	h := newHarness(t)
	h.m.secrets, h.m.secretsErr = nil, secret.ErrNoStore

	h.press("C")
	h.press("n")
	h.m.form = newCredentialForm(store.Credential{
		Name: "Legacy", Kind: store.CredentialPassword, User: "admin"}, false)
	h.m.form.fields[3].input.SetValue("hunter2")
	h.press("enter")

	if h.m.form == nil {
		t.Fatal("it was saved on a machine with nowhere to put the password")
	}
	if !strings.Contains(h.m.form.problem, "keychain") {
		t.Errorf("problem = %q, want it to say there is nowhere to keep one", h.m.form.problem)
	}
}
