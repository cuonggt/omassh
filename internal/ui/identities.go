package ui

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/cuonggt/omassh/internal/agent"
	"github.com/cuonggt/omassh/internal/keys"
	"github.com/cuonggt/omassh/internal/secrets"
	"github.com/cuonggt/omassh/internal/store"
	"github.com/cuonggt/omassh/internal/ui/theme"
)

func (m Model) openIdentities() (tea.Model, tea.Cmd) {
	m.mode = modeIdentities
	m.refreshAgent()
	m.identityIdx = clamp(m.identityIdx, 0, len(m.d.identities)-1)
	return m, nil
}

// refreshAgent re-reads what the agent holds. It runs ssh-add, so it is called
// on entering the view and after actions, never while rendering.
func (m *Model) refreshAgent() {
	m.agentKeys = map[string]bool{}
	m.agentErr = nil
	if !agent.Available() {
		m.agentErr = agent.ErrNoAgent
		return
	}
	loaded, err := agent.List()
	if err != nil {
		m.agentErr = err
		return
	}
	for _, k := range loaded {
		m.agentKeys[k.Fingerprint] = true
	}
}

func (m Model) selectedIdentity() (store.Identity, bool) {
	if len(m.d.identities) == 0 {
		return store.Identity{}, false
	}
	return m.d.identities[clamp(m.identityIdx, 0, len(m.d.identities)-1)], true
}

func (m Model) handleIdentitiesKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc", "q", "K":
		m.mode = modeBrowse
		return m, nil
	case "j", "down":
		m.identityIdx = clamp(m.identityIdx+1, 0, len(m.d.identities)-1)
	case "k", "up":
		m.identityIdx = clamp(m.identityIdx-1, 0, len(m.d.identities)-1)
	case "r":
		m.reload()
		m.refreshAgent()
		m.setStatus("reloaded")
	case "n":
		m.form = newIdentityForm(store.Identity{})
		m.returnTo, m.mode = backFor(m.mode), modeForm
		return m, m.form.focusCurrent()
	case "g":
		m.form = newGenKeyForm()
		m.returnTo, m.mode = backFor(m.mode), modeForm
		return m, m.form.focusCurrent()
	case "e":
		idn, ok := m.selectedIdentity()
		if !ok {
			return m, nil
		}
		m.form = newIdentityForm(idn)
		m.returnTo, m.mode = backFor(m.mode), modeForm
		return m, m.form.focusCurrent()
	case "d":
		return m.askDeleteIdentity()
	case "u":
		return m.unlockIdentity()
	case "x":
		return m.removeFromAgent()
	}
	return m, nil
}

// unlockIdentity loads a credential's key into the ssh-agent, supplying the
// stored passphrase through the askpass handoff so nothing is typed.
func (m Model) unlockIdentity() (tea.Model, tea.Cmd) {
	idn, ok := m.selectedIdentity()
	if !ok {
		return m, nil
	}
	if idn.KeyPath == "" {
		m.setStatus(idn.Name + " has no key file to load")
		return m, nil
	}
	path := ExpandTilde(idn.KeyPath)
	info, err := keys.Inspect(path)
	if err != nil {
		m.setErr(err)
		return m, nil
	}

	if !info.Encrypted {
		if err := agent.Add(nil, path, 0); err != nil {
			m.setErr(err)
			return m, nil
		}
		m.refreshAgent()
		m.setStatus("loaded " + idn.Name + " into the agent")
		return m, nil
	}

	secret, err := m.vault.Get(idn.ID)
	if errors.Is(err, secrets.ErrNotFound) {
		m.setStatus("no passphrase stored for " + idn.Name + " — press e to add one")
		return m, nil
	}
	if err != nil {
		m.setErr(fmt.Errorf("read secret: %w", err))
		return m, nil
	}
	err = secrets.WithAskpass(secret, func(env []string) error {
		return agent.Add(env, path, 0)
	})
	if err != nil {
		m.setErr(err)
		return m, nil
	}
	m.refreshAgent()
	m.setStatus("unlocked " + idn.Name + " into the agent")
	return m, nil
}

func (m Model) removeFromAgent() (tea.Model, tea.Cmd) {
	idn, ok := m.selectedIdentity()
	if !ok || idn.KeyPath == "" {
		return m, nil
	}
	if err := agent.Remove(ExpandTilde(idn.KeyPath)); err != nil {
		m.setErr(err)
		return m, nil
	}
	m.refreshAgent()
	m.setStatus("removed " + idn.Name + " from the agent")
	return m, nil
}

func (m Model) askDeleteIdentity() (tea.Model, tea.Cmd) {
	idn, ok := m.selectedIdentity()
	if !ok {
		return m, nil
	}
	hosts, groups := m.st.IdentityUsage(idn.ID)
	detail := "the key file on disk is left alone"
	if hosts+groups > 0 {
		detail = fmt.Sprintf("%d host(s) and %d group(s) lose this credential; the key file on disk is left alone",
			hosts, groups)
	}
	vault, id, name := m.vault, idn.ID, idn.Name
	m.confirm = &confirmation{
		prompt: "Delete credential " + name + "?",
		detail: detail,
		run: func() (string, error) {
			// Remove the secret first: a stored secret with no record pointing
			// at it would be unreachable from the UI but still in the keychain.
			if err := vault.Delete(id); err != nil {
				return "", err
			}
			return "deleted credential " + name, m.st.DeleteIdentity(id)
		},
	}
	m.returnTo, m.mode = backFor(m.mode), modeConfirm
	return m, nil
}

// --- forms -------------------------------------------------------------

func newIdentityForm(i store.Identity) *form {
	title := "New credential"
	hint := "leave empty for none"
	if i.ID != "" {
		title = "Edit " + i.Name
		if i.HasSecret {
			hint = "stored — type to replace"
		}
	}
	return &form{
		kind: formIdentity, title: title, editID: i.ID,
		fields: []field{
			newField("Name", "work-key", i.Name),
			newField("User", "default login user", i.User),
			newField("Key file", "~/.ssh/id_ed25519", i.KeyPath),
			newSecretField("Passphrase", hint),
		},
	}
}

func newGenKeyForm() *form {
	return &form{
		kind: formGenKey, title: "Generate key",
		fields: []field{
			newField("Name", "work-key", ""),
			newField("Key file", filepath.Join(keys.DefaultDir(), "id_omassh"), ""),
			newField("Comment", "you@machine", ""),
			newSecretField("Passphrase", "empty for an unencrypted key"),
		},
	}
}

func (m Model) saveIdentityForm() (tea.Model, tea.Cmd) {
	f := m.form
	name := f.value("Name")
	if name == "" {
		f.problem = "a credential needs a name"
		return m, nil
	}
	if other, ok := m.d.identityByName(name); ok && other.ID != f.editID {
		f.problem = "a credential named " + name + " already exists"
		return m, nil
	}

	idn := store.Identity{
		ID: f.editID, Name: name,
		User: f.value("User"), KeyPath: f.value("Key file"),
	}
	if prev, ok := m.d.identityByID(f.editID); ok {
		idn.HasSecret = prev.HasSecret
	}

	saved, err := m.st.PutIdentity(idn)
	if err != nil {
		f.problem = err.Error()
		return m, nil
	}
	// An empty passphrase field means "leave the stored one alone", so that
	// editing a name never silently discards a secret.
	if pass := f.value("Passphrase"); pass != "" {
		if err := m.vault.Set(saved.ID, pass); err != nil {
			f.problem = "store secret: " + err.Error()
			return m, nil
		}
		saved.HasSecret = true
		if _, err := m.st.PutIdentity(saved); err != nil {
			f.problem = err.Error()
			return m, nil
		}
	}

	m.form, m.mode = nil, modeIdentities
	m.reload()
	m.setStatus("saved credential " + name)
	return m, nil
}

func (m Model) saveGenKeyForm() (tea.Model, tea.Cmd) {
	f := m.form
	name, path := f.value("Name"), f.value("Key file")
	if name == "" || path == "" {
		f.problem = "a name and a key file path are both required"
		return m, nil
	}
	if _, ok := m.d.identityByName(name); ok {
		f.problem = "a credential named " + name + " already exists"
		return m, nil
	}

	pass := f.value("Passphrase")
	comment := f.value("Comment")
	if comment == "" {
		comment = name
	}
	if _, err := keys.Generate(ExpandTilde(path), comment, pass); err != nil {
		f.problem = err.Error()
		return m, nil
	}

	saved, err := m.st.PutIdentity(store.Identity{Name: name, KeyPath: path, HasSecret: pass != ""})
	if err != nil {
		f.problem = err.Error()
		return m, nil
	}
	if pass != "" {
		if err := m.vault.Set(saved.ID, pass); err != nil {
			f.problem = "store secret: " + err.Error()
			return m, nil
		}
	}

	m.form, m.mode = nil, modeIdentities
	m.reload()
	m.refreshAgent()
	m.setStatus("generated " + path + " and saved credential " + name)
	return m, nil
}

// --- rendering ---------------------------------------------------------

func (m Model) identitiesBody() string {
	var b strings.Builder
	b.WriteString("\n")

	switch {
	case m.agentErr != nil:
		b.WriteString("  " + theme.Fg(theme.Yellow).Render("agent: "+m.agentErr.Error()) + "\n\n")
	default:
		b.WriteString("  " + theme.Dim.Render(fmt.Sprintf("agent holds %d key(s)", len(m.agentKeys))) + "\n\n")
	}

	if len(m.d.identities) == 0 {
		b.WriteString(theme.Dim.Render("  no credentials yet — ") +
			theme.Key.Render("n") + theme.Dim.Render(" to add one, ") +
			theme.Key.Render("g") + theme.Dim.Render(" to generate a key\n"))
	}

	for i, idn := range m.d.identities {
		marker := "  "
		nameStyle := theme.Normal
		if i == m.identityIdx {
			marker = theme.Fg(theme.Accent).Render("▸ ")
			nameStyle = theme.Fg(theme.TextBrt).Bold(true)
		}
		b.WriteString("  " + marker + nameStyle.Render(idn.Name) + "\n")

		where := strOr(idn.KeyPath, "no key file")
		if idn.User != "" {
			where = idn.User + " · " + where
		}
		b.WriteString("      " + theme.Dim.Render(where) + "\n")
		b.WriteString("      " + m.identityBadges(idn) + "\n\n")
	}

	b.WriteString("\n  " +
		hint("n", "new") + sep() + hint("g", "generate key") + sep() +
		hint("e", "edit") + sep() + hint("d", "delete") + sep() +
		hint("u", "unlock into agent") + sep() + hint("x", "remove") + sep() + hint("esc", "back"))
	return b.String()
}

func (m Model) identityBadges(idn store.Identity) string {
	var parts []string
	info, hasKey := m.d.keyInfo[idn.ID]

	switch {
	case idn.KeyPath == "":
		parts = append(parts, theme.Dim.Render("password credential"))
	case !hasKey:
		parts = append(parts, theme.Fg(theme.Red).Render("key file unreadable"))
	default:
		parts = append(parts, theme.Dim.Render(info.Type))
		if info.Encrypted {
			parts = append(parts, theme.Fg(theme.Yellow).Render("encrypted"))
		} else {
			parts = append(parts, theme.Dim.Render("unencrypted"))
		}
	}

	if idn.HasSecret {
		parts = append(parts, theme.Fg(theme.Green).Render("secret stored"))
	} else if hasKey && info.Encrypted {
		parts = append(parts, theme.Fg(theme.Red).Render("no secret stored"))
	}
	if hasKey && m.agentKeys[info.Fingerprint] {
		parts = append(parts, theme.Fg(theme.Green).Render("● in agent"))
	}
	return strings.Join(parts, theme.Dim.Render(" · "))
}
