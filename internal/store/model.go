// Package store holds Omassh's data model and its bbolt persistence.
package store

import "time"

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
}

// Target renders the [user@]host argument passed to ssh.
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

// GroupNode is a group positioned in the display tree.
type GroupNode struct {
	Group
	Depth int
}

// FlattenGroups orders groups depth-first by name, so the UI can render a
// nested tree as a flat list. Groups whose parent is missing are treated as
// roots, which keeps a broken ParentID from hiding hosts.
func FlattenGroups(gs []Group) []GroupNode {
	byParent := map[string][]Group{}
	exists := map[string]bool{}
	for _, g := range gs {
		exists[g.ID] = true
	}
	for _, g := range gs {
		p := g.ParentID
		if p != "" && !exists[p] {
			p = "" // orphaned: surface it at the root rather than losing it
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
			if seen[g.ID] || depth > 16 { // cycle / runaway nesting guard
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
