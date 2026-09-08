package store

import "strings"

// Resolved is a host with group-inherited attributes filled in, along with the
// name of the group each inherited value came from.
type Resolved struct {
	Host
	UserFrom      string
	IdentityFrom  string
	ProxyJumpFrom string
}

// Resolver applies group inheritance to hosts, and turns a jump host named by
// one of your hosts into the destination ssh actually needs.
type Resolver struct {
	byID    map[string]Group
	byName  map[string]Host
	maxHops int
}

// maxJumpHops bounds a chain of jump hosts. Past a handful this is far more
// likely to be a mistake than a real topology, and the bound is what stops a
// pathological store from building an enormous command line.
const maxJumpHops = 8

func NewResolver(gs []Group, hosts []Host) Resolver {
	m := make(map[string]Group, len(gs))
	for _, g := range gs {
		m[g.ID] = g
	}
	n := make(map[string]Host, len(hosts))
	for _, h := range hosts {
		// Last one wins, which matches what the list shows for a duplicate.
		n[strings.ToLower(h.Name)] = h
	}
	return Resolver{byID: m, byName: n, maxHops: maxJumpHops}
}

// Resolve fills any attribute the host leaves empty from the nearest ancestor
// group that sets it. A host's own value always wins.
func (r Resolver) Resolve(h Host) Resolved {
	out := r.inherit(h)
	out.Jump = r.jumpHost(out.ProxyJump, map[string]bool{}, 0)
	return out
}

// jumpHost resolves the jump host named by a field into the host itself, with
// its own jump host resolved behind it.
//
// The field holds one of your host *names*, because that is what a picker
// offers and what you would write by hand. Returning the host rather than a
// string is what lets the connection be built with that host's key and port:
// ssh -J would silently drop both. A name that is not one of your hosts is
// left for ssh to interpret, so `user@bastion.example.com` still works.
func (r Resolver) jumpHost(name string, seen map[string]bool, depth int) *Host {
	name = strings.TrimSpace(name)
	if name == "" || depth >= r.maxHops {
		return nil
	}
	h, ok := r.byName[strings.ToLower(name)]
	if !ok {
		return nil // not one of ours; Build falls back to -J with the raw text
	}
	if seen[h.ID] {
		return nil // a jump host reached through itself, directly or in a loop
	}
	seen[h.ID] = true

	// The hop inherits from its groups like any other host.
	hop := r.inherit(h).Host
	hop.Jump = r.jumpHost(hop.ProxyJump, seen, depth+1)
	return &hop
}

// inherit walks the group chain, filling anything the host leaves empty.
func (r Resolver) inherit(h Host) Resolved {
	out := Resolved{Host: h}

	seen := map[string]bool{}
	for id := h.GroupID; id != "" && !seen[id]; {
		seen[id] = true
		g, ok := r.byID[id]
		if !ok {
			break
		}
		if out.User == "" && g.User != "" {
			out.User, out.UserFrom = g.User, g.Name
		}
		if out.Identity == "" && g.Identity != "" {
			out.Identity, out.IdentityFrom = g.Identity, g.Name
		}
		if out.ProxyJump == "" && g.ProxyJump != "" {
			out.ProxyJump, out.ProxyJumpFrom = g.ProxyJump, g.Name
		}
		id = g.ParentID
	}
	return out
}
