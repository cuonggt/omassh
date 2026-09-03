package ui

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/cuonggt/omassh/internal/keys"
	"github.com/cuonggt/omassh/internal/store"
)

// UngroupedID is the synthetic group holding local hosts with no group.
const UngroupedID = "__ungrouped"

// data is one consistent snapshot of everything the UI draws: the persisted
// store plus whatever ~/.ssh/config currently says.
type data struct {
	groups     []store.Group // persisted groups only
	hosts      []store.Host  // persisted hosts plus config-sourced hosts
	identities []store.Identity
	forwards   []store.Forward
	snippets   []store.Snippet
	stats      map[string]store.Stat
	tree       []store.GroupNode    // display order, including synthetic groups
	keyInfo    map[string]keys.Info // identity id -> on-disk key metadata
	resolver   store.Resolver
}

type dataMsg struct {
	data data
	err  error
}

// DefaultSSHConfigPath is where OpenSSH keeps the user's client config.
func DefaultSSHConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".ssh", "config")
}

// load reads the store and re-reads ~/.ssh/config.
//
// Config hosts are deliberately not copied into the store: they are read fresh
// every time, so Omassh can never show a stale copy of a file the user edits
// by hand. The cost is that they cannot be edited here, which is why they are
// badged read-only in the list.
func load(s *store.Store, sshConfig string) (data, error) {
	var d data
	var err error

	if d.groups, err = s.Groups(); err != nil {
		return d, err
	}
	if d.hosts, err = s.Hosts(); err != nil {
		return d, err
	}
	if d.identities, err = s.Identities(); err != nil {
		return d, err
	}
	if d.forwards, err = s.Forwards(); err != nil {
		return d, err
	}
	if d.snippets, err = s.Snippets(); err != nil {
		return d, err
	}
	if d.stats, err = s.Stats(); err != nil {
		return d, err
	}

	// Read each credential's key once per load rather than per frame; Inspect
	// shells out to ssh-keygen twice, which is far too costly to render with.
	d.keyInfo = map[string]keys.Info{}
	for _, i := range d.identities {
		if i.KeyPath == "" {
			continue
		}
		if info, err := keys.Inspect(ExpandTilde(i.KeyPath)); err == nil {
			d.keyInfo[i.ID] = info
		}
	}

	// A broken ~/.ssh/config must not take the whole app down; the store side
	// still works, and the error surfaces in the status bar.
	cfgHosts, cfgErr := store.LoadSSHConfig(sshConfig)

	d.resolver = store.NewResolver(d.groups, d.identities)
	d.tree = store.FlattenGroups(d.groups)

	ungrouped := 0
	for _, h := range d.hosts {
		if h.GroupID == "" {
			ungrouped++
		}
	}
	if ungrouped > 0 {
		d.tree = append(d.tree, store.GroupNode{Group: store.Group{ID: UngroupedID, Name: "Ungrouped"}})
	}
	if len(cfgHosts) > 0 {
		d.tree = append(d.tree, store.GroupNode{Group: store.Group{ID: store.SSHConfigGroupID, Name: "ssh_config"}})
		d.hosts = append(d.hosts, cfgHosts...)
	}
	return d, cfgErr
}

// hostsIn returns the hosts displayed under a group id.
func (d data) hostsIn(groupID string) []store.Host {
	var out []store.Host
	for _, h := range d.hosts {
		gid := h.GroupID
		if gid == "" {
			gid = UngroupedID
		}
		if gid == groupID {
			out = append(out, h)
		}
	}
	return out
}

func (d data) groupName(id string) string {
	for _, g := range d.groups {
		if g.ID == id {
			return g.Name
		}
	}
	return ""
}

// identityByName finds a credential by case-insensitive name.
func (d data) identityByName(name string) (store.Identity, bool) {
	for _, i := range d.identities {
		if equalFold(i.Name, name) {
			return i, true
		}
	}
	return store.Identity{}, false
}

func (d data) identityByID(id string) (store.Identity, bool) {
	for _, i := range d.identities {
		if i.ID == id {
			return i, true
		}
	}
	return store.Identity{}, false
}

// ExpandTilde resolves a leading ~ so paths shown in the UI can also be opened.
func ExpandTilde(p string) string {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	return filepath.Join(home, strings.TrimPrefix(p, "~"))
}

// groupByName finds a persisted group by case-insensitive name.
func (d data) groupByName(name string) (store.Group, bool) {
	for _, g := range d.groups {
		if equalFold(g.Name, name) {
			return g, true
		}
	}
	return store.Group{}, false
}
