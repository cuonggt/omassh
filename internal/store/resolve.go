package store

import "strings"

// Resolved is a host with group-inherited attributes filled in, along with the
// name of the group each inherited value came from.
type Resolved struct {
	Host
	UserFrom      string
	IdentityFrom  string
	ProxyJumpFrom string

	// AliasElsewhere is ssh's own config's entry under this host's name when
	// that entry goes somewhere else — another address, or another port — so
	// the settings written there are not this host's. Kept so the detail
	// pane can say why they are not in use, rather than leave someone who
	// wrote them wondering.
	AliasElsewhere *ConfigAlias
}

// ConfigAlias is a name ssh's own config gives a machine — a name on a Host
// line — and where that name takes ssh.
type ConfigAlias struct {
	// Name is spelled as the config spells it. ssh matches a Host line
	// against the name it is handed exactly, so `ssh PROD-WEB` passes over a
	// block written under `Host prod-web`.
	Name string
	// Addr and Port are where ssh goes for the name: its HostName, or the
	// name itself where nothing gives one, and 22 where nothing says
	// otherwise.
	Addr string
	Port int
}

// Resolver applies group inheritance to hosts, and turns a jump host named by
// one of your hosts into the destination ssh actually needs.
type Resolver struct {
	byID    map[string]Group
	byName  map[string]Host
	byCred  map[string]Credential
	aliases map[string]ConfigAlias
	maxHops int
}

// maxJumpHops bounds a chain of jump hosts. Past a handful this is far more
// likely to be a mistake than a real topology, and the bound is what stops a
// pathological store from building an enormous command line.
const maxJumpHops = 8

func NewResolver(gs []Group, hosts []Host, creds []Credential) Resolver {
	m := make(map[string]Group, len(gs))
	for _, g := range gs {
		m[g.ID] = g
	}
	n := make(map[string]Host, len(hosts))
	for _, h := range hosts {
		// Last one wins, which matches what the list shows for a duplicate.
		n[strings.ToLower(h.Name)] = h
	}
	c := make(map[string]Credential, len(creds))
	for _, cr := range creds {
		c[cr.ID] = cr
	}
	return Resolver{byID: m, byName: n, byCred: c, maxHops: maxJumpHops}
}

// WithAliases is the resolver knowing what ssh's own config calls machines, so
// that a host the config names is reached by that name.
//
// Where two names differ only in case, the first is kept: names are matched
// without regard to case here, as they are everywhere else in omassh.
func (r Resolver) WithAliases(as []ConfigAlias) Resolver {
	r.aliases = make(map[string]ConfigAlias, len(as))
	for _, a := range as {
		k := strings.ToLower(a.Name)
		if _, dup := r.aliases[k]; !dup {
			r.aliases[k] = a
		}
	}
	return r
}

// Resolve fills any attribute the host leaves empty from the nearest ancestor
// group that sets it. A host's own value always wins.
func (r Resolver) Resolve(h Host) Resolved {
	out := r.inherit(h)
	out.Jump = r.jumpHost(out.ProxyJump, map[string]bool{}, 0)
	out.Alias, out.AliasElsewhere = r.alias(h)
	return out
}

// alias is the name to reach a host by, if ssh's own config has one for it.
//
// The name has to be the host's own, and the machine the same: the config's
// entry has to take ssh to this host's address and port. A `Host web` that
// goes somewhere else is a different machine that happens to share a name,
// and its settings are not this one's to take — ForwardAgent written for it
// would hand this machine your agent. That entry comes back as elsewhere
// instead, so the reason it is not used can be shown.
//
// The address and port are compared because they are what makes a host that
// host, and the reason neither is ever inherited. Hosts the config does not
// name are reached by address as they always were, so a block written for an
// address or a domain — `Host *.corp.example.com` — still applies to them;
// reaching every host by its name would trade those blocks for these.
func (r Resolver) alias(h Host) (name string, elsewhere *ConfigAlias) {
	a, ok := r.aliases[strings.ToLower(strings.TrimSpace(h.Name))]
	if !ok {
		return "", nil
	}
	if !strings.EqualFold(a.Addr, h.Addr) || portOr22(a.Port) != portOr22(h.Port) {
		return "", &a
	}
	return a.Name, nil
}

// portOr22 is the port ssh will use, which is 22 where none is given.
func portOr22(p int) int {
	if p == 0 {
		return 22
	}
	return p
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

	// The hop inherits from its groups like any other host, and is reached by
	// the name ssh's config gives it like any other host: ssh's own -J does
	// the same, handing the hop's name to the ssh that reaches it.
	hop := r.inherit(h).Host
	hop.Jump = r.jumpHost(hop.ProxyJump, seen, depth+1)
	hop.Alias, _ = r.alias(hop)
	return &hop
}

// inherit walks the group chain, filling anything the host leaves empty.
func (r Resolver) inherit(h Host) Resolved {
	out := Resolved{Host: h}

	// The host's own credential comes before any group: after the fields
	// typed onto the host itself, it is the most specific thing anyone said
	// about how this host logs in.
	r.fromCredential(&out, h.CredentialID)

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
		// After the group's own fields, for the same reason a host's fields
		// come before its credential: what was written here is more specific
		// than what was written once and shared.
		r.fromCredential(&out, g.CredentialID)
		id = g.ParentID
	}
	return out
}

// fromCredential fills whatever is still empty from a credential.
//
// One attribute at a time, like the group walk around it, so the rules stay
// the same wherever a value comes from: the nearest thing that sets it wins,
// and the provenance recorded beside it is the credential's name — which is
// what puts "← Prod deploy" in the detail pane through the machinery that
// already draws "← Production".
//
// A credential that is not there is passed over rather than reported. It means
// one was deleted in another window, and the host going on with whatever else
// it has is better than the resolver inventing a failure for a list that is
// about to be reloaded anyway.
func (r Resolver) fromCredential(out *Resolved, id string) {
	if id == "" {
		return
	}
	c, ok := r.byCred[id]
	if !ok {
		return
	}
	// The nearest credential is the one that decides how this host logs in,
	// even where it supplies no user or key of its own.
	if out.Cred == nil {
		out.Cred = &c
	}
	if out.User == "" && c.User != "" {
		out.User, out.UserFrom = c.User, c.Name
	}
	if out.Identity == "" && c.Identity != "" {
		out.Identity, out.IdentityFrom = c.Identity, c.Name
	}
}
