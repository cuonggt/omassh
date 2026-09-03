package store

// Resolved is a host with group-inherited attributes filled in, along with
// the name of the group or credential each inherited value came from.
type Resolved struct {
	Host
	UserFrom      string
	IdentityFrom  string
	ProxyJumpFrom string

	// Credential is the identity that supplied a key or user, if any.
	Credential Identity
}

// Resolver applies group and credential inheritance to hosts.
type Resolver struct {
	byID  map[string]Group
	idsBy map[string]Identity
}

func NewResolver(gs []Group, ids []Identity) Resolver {
	m := make(map[string]Group, len(gs))
	for _, g := range gs {
		m[g.ID] = g
	}
	n := make(map[string]Identity, len(ids))
	for _, i := range ids {
		n[i.ID] = i
	}
	return Resolver{byID: m, idsBy: n}
}

// Resolve fills any attribute the host leaves empty from the nearest ancestor
// group that sets it. A host's own value always wins; config-sourced hosts are
// returned untouched, since OpenSSH already owns their configuration.
func (r Resolver) Resolve(h Host) Resolved {
	out := Resolved{Host: h}
	if h.Source == SourceSSHConfig {
		return out
	}

	// The host's own values come first, then its bound credential, then each
	// ancestor group and that group's credential. A nearer source always wins.
	r.applyCredential(&out, h.IdentityID)

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
		r.applyCredential(&out, g.IdentityID)
		id = g.ParentID
	}
	return out
}

// applyCredential fills anything still unset from a bound identity. Provenance
// is reported as the credential name rather than the group it hung off, since
// that is what the user would change to alter the outcome.
func (r Resolver) applyCredential(out *Resolved, identityID string) {
	if identityID == "" {
		return
	}
	cred, ok := r.idsBy[identityID]
	if !ok {
		return
	}
	if out.Credential.ID == "" {
		out.Credential = cred
	}
	if out.Identity == "" && cred.KeyPath != "" {
		out.Identity, out.IdentityFrom = cred.KeyPath, cred.Name
	}
	if out.User == "" && cred.User != "" {
		out.User, out.UserFrom = cred.User, cred.Name
	}
}
