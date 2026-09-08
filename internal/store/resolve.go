package store

// Resolved is a host with group-inherited attributes filled in, along with the
// name of the group each inherited value came from.
type Resolved struct {
	Host
	UserFrom      string
	IdentityFrom  string
	ProxyJumpFrom string
}

// Resolver applies group inheritance to hosts.
type Resolver struct {
	byID map[string]Group
}

func NewResolver(gs []Group) Resolver {
	m := make(map[string]Group, len(gs))
	for _, g := range gs {
		m[g.ID] = g
	}
	return Resolver{byID: m}
}

// Resolve fills any attribute the host leaves empty from the nearest ancestor
// group that sets it. A host's own value always wins; config-sourced hosts are
// returned untouched, since OpenSSH already owns their configuration.
func (r Resolver) Resolve(h Host) Resolved {
	out := Resolved{Host: h}
	if h.Source == SourceSSHConfig {
		return out
	}

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
