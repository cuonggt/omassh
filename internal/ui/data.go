package ui

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/cuonggt/omassh/internal/store"
	"github.com/cuonggt/omassh/internal/term"
)

// UngroupedID is the synthetic group holding local hosts with no group.
const UngroupedID = "__ungrouped"

// data is one consistent snapshot of everything the UI draws: the persisted
// store plus whatever ~/.ssh/config currently says.
type data struct {
	groups   []store.Group // persisted groups only
	hosts    []store.Host  // persisted hosts plus config-sourced hosts
	stats    map[string]store.Stat
	tree     []store.GroupNode // display order, including synthetic groups
	resolver store.Resolver
	// live maps a tmux session name to whether it is running, so the host
	// list can show which hosts have a session waiting to be reattached.
	live map[string]bool
}

type dataMsg struct {
	data data
	err  error
}

// load reads everything the interface renders from the store.
func load(s *store.Store) (data, error) {
	var d data
	var err error

	if d.groups, err = s.Groups(); err != nil {
		return d, err
	}
	if d.hosts, err = s.Hosts(); err != nil {
		return d, err
	}
	if d.stats, err = s.Stats(); err != nil {
		return d, err
	}

	// Which hosts already have a session waiting to be reattached.
	d.live = map[string]bool{}
	if sessions, err := term.LiveSessions(); err == nil {
		for _, s := range sessions {
			d.live[s.Name] = true
		}
	}

	d.resolver = store.NewResolver(d.groups)
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
	return d, nil
}

// hasSession reports whether a host has a persistent session running.
func (d data) hasSession(h store.Host) bool { return d.live[term.SessionName(h)] }

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
