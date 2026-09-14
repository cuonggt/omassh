// Package store holds Omassh's data model and its bbolt persistence.
package store

import (
	"strconv"
	"time"
)

// Group is a named collection of hosts. Groups nest, and a host inherits
// User, Identity and ProxyJump from its group chain unless it sets its own.
type Group struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	ParentID string `json:"parent_id,omitempty"`

	User      string `json:"user,omitempty"`
	Identity  string `json:"identity,omitempty"`
	ProxyJump string `json:"proxy_jump,omitempty"`
}

// Host is a single reachable machine.
type Host struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Addr      string   `json:"addr"`
	Port      int      `json:"port,omitempty"`
	User      string   `json:"user,omitempty"`
	Identity  string   `json:"identity,omitempty"`
	ProxyJump string   `json:"proxy_jump,omitempty"`
	GroupID   string   `json:"group_id,omitempty"`
	Tags      []string `json:"tags,omitempty"`

	// Jump is the resolved jump host, filled in at resolve time and never
	// persisted. ssh -J passes only -l, -p and -v to the hop, so the jump
	// host's own key and port would be ignored; carrying the host itself lets
	// the connection be built with them.
	Jump *Host `json:"-"`
}

// Target renders the [user@]host argument passed to ssh.
// JumpTarget is how this host is named to ssh -J: a destination, with the
// port only when it is not the default, since -J takes [user@]host[:port].
func (h Host) JumpTarget() string {
	t := h.Target()
	if h.Port != 0 && h.Port != 22 {
		t += ":" + strconv.Itoa(h.Port)
	}
	return t
}

func (h Host) Target() string {
	if h.User != "" {
		return h.User + "@" + h.Addr
	}
	return h.Addr
}

// StatKey identifies a host in the session-history bucket.
func (h Host) StatKey() string { return h.ID }

// Stat is the recorded session history for one host.
type Stat struct {
	LastSeen time.Time `json:"last_seen"`
	Count    int       `json:"count"`
}

// inCycle reports whether walking up from g comes back to g itself.
//
// Bounded by the number of groups, because a walk that visits more nodes than
// there are has revisited one — and the revisited node need not be g. That is
// the case this has to tell apart: a group *below* a cycle walks forever too,
// and it is not the one whose parent has to be let go of.
func inCycle(byID map[string]Group, g Group) bool {
	id := g.ParentID
	for range len(byID) + 1 {
		if id == "" {
			return false
		}
		if id == g.ID {
			return true
		}
		parent, ok := byID[id]
		if !ok {
			return false // a missing parent is a root, not a loop
		}
		id = parent.ParentID
	}
	return false
}

// GroupNode is a group positioned in the display tree.
type GroupNode struct {
	Group
	Depth int
}

// FlattenGroups orders groups depth-first by name, so the UI can render a
// nested tree as a flat list. A group whose parent is missing, or whose
// parents come back round to it, is treated as a root — which keeps a broken
// ParentID from hiding hosts.
//
// The walk starts from roots, and no group in a cycle is one, so a cycle used
// to leave every group in it out of the list entirely, and its hosts with it.
// The records were all still there; the only thing that could be done about
// them was to not see them. A list with nothing in it to select is also
// nothing to mend, so the loop could not be undone from the interface that was
// hiding it. This store will not create one, but a database written by an
// older build, by another writer, or by hand can hold one.
func FlattenGroups(gs []Group) []GroupNode {
	byParent := map[string][]Group{}
	exists := map[string]bool{}
	byID := make(map[string]Group, len(gs))
	for _, g := range gs {
		exists[g.ID] = true
		byID[g.ID] = g
	}
	for _, g := range gs {
		p := g.ParentID
		switch {
		case p != "" && !exists[p]:
			p = "" // orphaned: surface it at the root rather than losing it
		case inCycle(byID, g):
			// Only the groups the cycle runs through, not everything under
			// one: a group below a cycle keeps its parent, which is now a
			// root itself, so what was nested stays nested.
			p = ""
		}
		byParent[p] = append(byParent[p], g)
	}
	for k := range byParent {
		sortGroups(byParent[k])
	}

	var out []GroupNode
	seen := map[string]bool{}
	var walk func(parent string, depth int)
	walk = func(parent string, depth int) {
		for _, g := range byParent[parent] {
			// Visiting each group once is the whole guard needed. A group has
			// one parent, so nothing is reachable twice, and a cycle never
			// enters this walk at all — no group in one is a root. A depth
			// limit alongside it only ever fired on a tree that was genuinely
			// that deep, and dropped it silently: the groups vanished from the
			// list and their hosts became unreachable except by searching.
			if seen[g.ID] {
				continue
			}
			seen[g.ID] = true
			out = append(out, GroupNode{Group: g, Depth: depth})
			walk(g.ID, depth+1)
		}
	}
	walk("", 0)
	return out
}
