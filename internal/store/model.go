// Package store holds Omassh's data model and its bbolt persistence.
package store

import "time"

// SSHConfigGroupID is the reserved id of the synthetic group that holds hosts
// read from ~/.ssh/config. It is never persisted.
const SSHConfigGroupID = "__sshconfig"

// Source records where a host definition came from. It matters at connect
// time: config-sourced hosts are addressed by alias so OpenSSH applies the
// user's own directives, rather than having Omassh re-derive them.
type Source uint8

const (
	SourceLocal Source = iota
	SourceSSHConfig
)

func (s Source) String() string {
	if s == SourceSSHConfig {
		return "ssh_config"
	}
	return "local"
}

// Group is a named collection of hosts. Groups nest, and a host inherits
// User, Identity and ProxyJump from its group chain unless it sets its own.
type Group struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	ParentID string `json:"parent_id,omitempty"`

	User       string `json:"user,omitempty"`
	Identity   string `json:"identity,omitempty"`
	ProxyJump  string `json:"proxy_jump,omitempty"`
	IdentityID string `json:"identity_id,omitempty"`
}

// Identity is a named credential: a login user, optionally a private key, and
// optionally a secret. The secret itself never appears here — it lives in the
// OS keychain under the identity's id, and HasSecret only records that one was
// stored, so the UI can render without unlocking the keychain on every frame.
type Identity struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	User      string `json:"user,omitempty"`
	KeyPath   string `json:"key_path,omitempty"`
	HasSecret bool   `json:"has_secret,omitempty"`
}

// Snippet is a saved command, runnable on one host or fanned out over a group.
type Snippet struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Command string `json:"command"`
}

// Host is a single reachable machine.
type Host struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Addr      string `json:"addr"`
	Port      int    `json:"port,omitempty"`
	User      string `json:"user,omitempty"`
	Identity  string `json:"identity,omitempty"`
	ProxyJump string `json:"proxy_jump,omitempty"`
	GroupID   string `json:"group_id,omitempty"`
	// IdentityID binds a stored credential; Identity is a raw key path that
	// overrides it. Both are optional and both are inherited from groups.
	IdentityID string   `json:"identity_id,omitempty"`
	Tags       []string `json:"tags,omitempty"`

	// Source is runtime-only: ssh_config hosts are never written to the store.
	Source Source `json:"-"`
	// Note carries a human-readable caveat, e.g. that the host is reached via
	// a ProxyCommand that Omassh deliberately does not try to reinterpret.
	Note string `json:"-"`
}

// Target renders the [user@]host argument passed to ssh.
func (h Host) Target() string {
	if h.User != "" {
		return h.User + "@" + h.Addr
	}
	return h.Addr
}

// StatKey identifies a host in the session-history bucket. Config-sourced
// hosts have no stable id of their own, so they are keyed by alias — which
// means their history survives edits to ~/.ssh/config.
func (h Host) StatKey() string {
	if h.Source == SourceSSHConfig {
		return "cfg:" + h.Name
	}
	return h.ID
}

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
