package ui

import (
	"errors"
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
// load reads the store into what the interface draws.
//
// A record the store could not decode is skipped rather than fatal, and what
// it could read is kept: treating any error as a reason to show nothing meant
// one damaged record emptied the whole list, over a message that named neither
// the database nor the record. The complaint is carried out alongside the
// data, so both are shown.
func load(s *store.Store) (data, error) {
	var d data
	var problems []string
	note := func(err error) {
		if err != nil {
			problems = append(problems, err.Error())
		}
	}

	var err error
	d.groups, err = s.Groups()
	note(err)
	d.hosts, err = s.Hosts()
	note(err)
	d.stats, err = s.Stats()
	note(err)

	// Which hosts already have a session waiting to be reattached.
	d.live = map[string]bool{}
	if sessions, err := term.LiveSessions(); err == nil {
		for _, s := range sessions {
			d.live[s.Name] = true
		}
	}

	d.resolver = store.NewResolver(d.groups, d.hosts)
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
	if len(problems) > 0 {
		return d, errors.New(strings.Join(problems, "; "))
	}
	return d, nil
}

// hasSession reports whether a host has a persistent session running.
func (d data) hasSession(h store.Host) bool { return d.live[term.SessionName(h)] }

// hostsIn returns the hosts displayed under a group id.
// hostsIn is every host in a group, including those in the groups beneath it.
//
// Counting only the hosts a group holds directly made a group whose machines
// all live in its children read as empty — a count of nought and an offer to
// add the first one — and that is the shape most people nest for: the
// organisation at the top, the machines in the regions under it. Selecting a
// group now answers "what is in here", which is what selecting a parent looks
// like it should do.
//
// The count beside a group and the list it opens both come from here, so they
// cannot disagree about what "in" means.
func (d data) hostsIn(groupID string) []store.Host {
	within := d.withDescendants(groupID)
	var out []store.Host
	for _, h := range d.hosts {
		gid := h.GroupID
		if gid == "" {
			gid = UngroupedID
		}
		if within[gid] {
			out = append(out, h)
		}
	}
	return out
}

// withDescendants is a group along with every group beneath it.
//
// The tree is flattened depth first, so what is beneath a group is the run of
// deeper entries following it — no walking of parents needed.
func (d data) withDescendants(id string) map[string]bool {
	out := map[string]bool{id: true}
	for i, n := range d.tree {
		if n.ID != id {
			continue
		}
		for _, c := range d.tree[i+1:] {
			if c.Depth <= n.Depth {
				break
			}
			out[c.ID] = true
		}
		break
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
