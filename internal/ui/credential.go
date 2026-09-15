package ui

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/cuonggt/omassh/internal/secret"
	"github.com/cuonggt/omassh/internal/store"
	"github.com/cuonggt/omassh/internal/ui/theme"
)

// unchanged is what an edited password field says instead of the password.
//
// Never the value, even masked: a form that shows the right number of dots is
// a form that has read the keychain to draw itself, and there is no reason to
// take a password out of it to put it on the screen as bullets.
const unchanged = "(unchanged) — type to replace"

func (m Model) openCredentials() (tea.Model, tea.Cmd) {
	m.mode = modeCredentials
	m.credIdx = clamp(m.credIdx, 0, len(m.d.creds)-1)
	switch n := len(m.d.creds); {
	case n == 0:
		m.setStatus("no credentials yet — n to add one")
	default:
		m.setStatus(fmt.Sprintf("%d credential%s", n, plural(n)))
	}
	return m, nil
}

func (m Model) closeCredentials() (tea.Model, tea.Cmd) {
	m.mode = modeBrowse
	m.setStatus("")
	return m, nil
}

func (m Model) handleCredentialsKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		return m.closeCredentials()
	case "j", "down":
		m.credIdx = clamp(m.credIdx+1, 0, len(m.d.creds)-1)
	case "k", "up":
		m.credIdx = clamp(m.credIdx-1, 0, len(m.d.creds)-1)
	case "n":
		return m.openCredentialForm(store.Credential{Kind: store.CredentialKey})
	case "e":
		if c, ok := m.selectedCredential(); ok {
			return m.openCredentialForm(c)
		}
	case "d":
		return m.askDeleteCredential()
	}
	return m, nil
}

func (m Model) selectedCredential() (store.Credential, bool) {
	if len(m.d.creds) == 0 {
		return store.Credential{}, false
	}
	return m.d.creds[clamp(m.credIdx, 0, len(m.d.creds)-1)], true
}

func (m Model) openCredentialForm(c store.Credential) (tea.Model, tea.Cmd) {
	m.form = newCredentialForm(c, c.ID != "" && m.hasPassword(c.ID))
	m.returnTo, m.mode = modeCredentials, modeForm
	return m, m.form.focusCurrent()
}

// hasPassword reports whether the keychain already holds one for a credential,
// which is what lets an edit leave the field empty and mean "as it was".
func (m Model) hasPassword(id string) bool {
	if m.secrets == nil || id == "" {
		return false
	}
	_, err := m.secrets.Get(id)
	return err == nil
}

func newCredentialForm(c store.Credential, stored bool) *form {
	title := "New credential"
	if c.ID != "" {
		title = "Edit " + c.Name
	}
	kind := string(c.Kind)
	if kind == "" {
		kind = string(store.CredentialKey)
	}
	pwHint := "kept in your keychain, never in omassh's own files"
	if stored {
		pwHint = unchanged
	}
	byLabel := map[string]field{
		"Name": newField("Name", "Prod deploy", c.Name),
		"Kind": asSuggestion(withChoices(newField("Kind", "key, agent or password — ↓ to pick", kind),
			[]string{string(store.CredentialKey), string(store.CredentialAgent), string(store.CredentialPassword)})),
		"User":     newField("User", "deploy", c.User),
		"Identity": newField("Identity", "path to a private key", c.Identity),
		"Password": newSecretField("Password", pwHint),
	}
	var fields []field
	for _, l := range fieldsFor(store.CredentialKind(kind)) {
		fields = append(fields, byLabel[l])
	}
	return &form{
		kind: formCredential, title: title, editID: c.ID,
		fields: fields, passwordStored: stored,
	}
}

// reshapeCredentialForm rebuilds the field list when the kind has changed,
// keeping what has already been typed.
//
// The cursor is kept on the field it was on by name rather than by index: the
// list it is an index into is the thing that just changed length.
func (m *Model) reshapeCredentialForm() {
	f := m.form
	if f == nil || f.kind != formCredential {
		return
	}
	want := fieldsFor(store.CredentialKind(strings.TrimSpace(f.value("Kind"))))
	if slices.Equal(labels(f.fields), want) {
		return
	}
	here := f.fields[f.idx].label
	values := map[string]string{}
	for _, x := range f.fields {
		values[x.label] = x.input.Value()
	}

	rebuilt := newCredentialForm(store.Credential{
		ID:       f.editID,
		Name:     values["Name"],
		Kind:     store.CredentialKind(strings.TrimSpace(values["Kind"])),
		User:     values["User"],
		Identity: values["Identity"],
	}, f.passwordStored)
	rebuilt.title, rebuilt.passwordStored = f.title, f.passwordStored
	// The password is not in the Credential above — it never goes near the
	// store — so it is carried across by hand.
	for i := range rebuilt.fields {
		if rebuilt.fields[i].label == "Password" {
			rebuilt.fields[i].input.SetValue(values["Password"])
		}
		if rebuilt.fields[i].label == here {
			rebuilt.idx = i
		}
	}
	m.form = rebuilt
	m.form.focusCurrent()
}

func labels(fs []field) []string {
	out := make([]string, 0, len(fs))
	for _, f := range fs {
		out = append(out, f.label)
	}
	return out
}

// fieldsFor is which fields a kind of credential actually uses.
func fieldsFor(k store.CredentialKind) []string {
	switch k {
	case store.CredentialPassword:
		return []string{"Name", "Kind", "User", "Password"}
	case store.CredentialAgent:
		return []string{"Name", "Kind", "User"}
	default: // a key, and anything half-typed on the way to one
		return []string{"Name", "Kind", "User", "Identity"}
	}
}

func (m Model) saveCredentialForm() (tea.Model, tea.Cmd) {
	f := m.form
	c := store.Credential{
		ID:       f.editID,
		Name:     strings.TrimSpace(f.value("Name")),
		Kind:     store.CredentialKind(strings.TrimSpace(f.value("Kind"))),
		User:     strings.TrimSpace(f.value("User")),
		Identity: strings.TrimSpace(f.value("Identity")),
	}
	if c.Name == "" {
		f.problem = "a name is required"
		return m, nil
	}
	if err := c.Valid(); err != nil {
		f.problem = err.Error()
		return m, nil
	}

	// The password is not trimmed. Leading and trailing spaces are as much a
	// part of one as any other character, and a password silently shortened
	// on the way in is a credential that cannot log in and cannot say why.
	pw := f.value("Password")
	if c.Kind == store.CredentialPassword {
		if m.secrets == nil {
			f.problem = bareErr(m.secretsErr)
			return m, nil
		}
		// Nothing typed and nothing stored cannot log in, and the moment to
		// say so is while the form is still open.
		if pw == "" && !m.hasPassword(c.ID) {
			f.problem = "type the password — it goes to the keychain, not to omassh's own files"
			return m, nil
		}
	}

	saved, err := m.st.PutCredential(c)
	if err != nil {
		f.problem = err.Error()
		return m, nil
	}

	switch {
	case saved.Kind == store.CredentialPassword && pw != "":
		if err := m.secrets.Set(saved.ID, pw); err != nil {
			// The record is written and the password is not, which is the one
			// state worth naming precisely: the credential exists and will be
			// refused until a password reaches the keychain.
			m.form, m.mode = nil, modeCredentials
			m.reload()
			m.setErrOf(saved.Name, err)
			return m, nil
		}
	case saved.Kind != store.CredentialPassword && m.secrets != nil:
		// Changed away from a password, so the one in the keychain is no
		// longer anything's. Left behind it would be a secret nothing on this
		// machine can reach or account for.
		m.secrets.Delete(saved.ID)
	}

	m.form, m.mode = nil, modeCredentials
	m.reload()
	m.selectCredential(saved)
	m.setStatusOf(saved.Name, "saved")
	return m, nil
}

func (m Model) askDeleteCredential() (tea.Model, tea.Cmd) {
	c, ok := m.selectedCredential()
	if !ok {
		return m, nil
	}
	hosts, groups := m.st.CredentialUses(c.ID)
	detail := "nothing is using it"
	if hosts+groups > 0 {
		// Said the way deleting a group says it: what stops being inherited,
		// rather than a count on its own.
		detail = fmt.Sprintf("%d host%s and %d group%s lose it and go back to what they say themselves",
			hosts, plural(hosts), groups, plural(groups))
	}
	if c.Kind == store.CredentialPassword {
		detail += "; the password goes from your keychain too"
	}

	st, id, name := m.secrets, c.ID, c.Name
	m.confirm = &confirmation{
		prompt: "Delete credential " + name + "?",
		detail: detail,
		run: func() (string, error) {
			if err := m.st.DeleteCredential(id); err != nil {
				return "", err
			}
			// After the record, so a keychain that refuses cannot leave a
			// credential nothing can use and nobody can remove.
			if st != nil {
				st.Delete(id)
			}
			return "deleted " + name, nil
		},
	}
	m.returnTo, m.mode = modeCredentials, modeConfirm
	return m, nil
}

func (m *Model) selectCredential(c store.Credential) {
	for i, x := range m.d.creds {
		if x.ID == c.ID {
			m.credIdx = i
			return
		}
	}
}

// bareErr is an error in the words it was written in, or a plain sentence when
// there is not one.
func bareErr(err error) string {
	if err == nil {
		return "there is nowhere to keep a password on this machine"
	}
	return err.Error()
}

func (m Model) credentialListRows(content int) int { return max(content-8, 1) }

func (m Model) credentialsBody(w, content int) string {
	if len(m.d.creds) == 0 {
		return "\n" + theme.Dim.Render("  no credentials yet") + "\n\n" +
			theme.Dim.Render("  a credential is a user and a way of proving it,") + "\n" +
			theme.Dim.Render("  named once and shared by as many hosts as you like") + "\n\n" +
			theme.Dim.Render("  n new  ·  esc close")
	}

	rows := m.credentialListRows(content)
	start, end := listWindow(m.credIdx, len(m.d.creds), rows)

	lines := []string{""}
	for i, c := range m.d.creds[start:end] {
		i += start
		mark, colour := "  ", theme.Text
		if i == m.credIdx {
			mark, colour = "▸ ", theme.Accent
		}
		// The kind as a word, the way a forward writes local or remote: a
		// symbol would need a legend, and there are only three of them.
		row := fmt.Sprintf("%-9s %s", string(c.Kind), c.Name)
		if d := c.Describe(); d != "" {
			row += "  " + d
		}
		lines = append(lines, theme.Fg(colour).Render("  "+mark+ansi.Truncate(row, max(w-6, 8), "…")))
	}

	lines = append(lines, "")
	if warn := m.credentialWarning(); warn != "" {
		lines = append(lines, theme.Fg(theme.Yellow).Render("  "+warn), "")
	}
	lines = append(lines, theme.Dim.Render("  n/e/d new/edit/delete  ·  esc close"))
	return strings.Join(lines, "\n")
}

// credentialWarning is what is wrong with the credential under the cursor, if
// anything — a password that is not in the keychain being the case that
// matters, since nothing else on the screen would show it.
func (m Model) credentialWarning() string {
	c, ok := m.selectedCredential()
	if !ok || c.Kind != store.CredentialPassword {
		return ""
	}
	if m.secrets == nil {
		return bareErr(m.secretsErr)
	}
	if _, err := m.secrets.Get(c.ID); err != nil {
		if errors.Is(err, secret.ErrNotFound) {
			return "no password in the keychain for this — e to type one"
		}
		return secretProblem(err)
	}
	return ""
}

// secretProblem is what the keychain said, kept short enough for the row.
func secretProblem(err error) string { return "the keychain said: " + err.Error() }
