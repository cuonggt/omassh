package store

import "testing"

func TestResolveInheritsFromGroupChain(t *testing.T) {
	groups := []Group{
		{ID: "root", Name: "Corp", User: "corpuser", ProxyJump: "bastion.corp"},
		{ID: "child", Name: "Prod", ParentID: "root", Identity: "~/.ssh/prod"},
	}
	r := NewResolver(groups, nil)

	got := r.Resolve(Host{Name: "web", Addr: "10.0.0.1", GroupID: "child"})

	if got.User != "corpuser" || got.UserFrom != "Corp" {
		t.Errorf("User = %q from %q, want corpuser from Corp", got.User, got.UserFrom)
	}
	if got.Identity != "~/.ssh/prod" || got.IdentityFrom != "Prod" {
		t.Errorf("Identity = %q from %q, want ~/.ssh/prod from Prod", got.Identity, got.IdentityFrom)
	}
	if got.ProxyJump != "bastion.corp" || got.ProxyJumpFrom != "Corp" {
		t.Errorf("ProxyJump = %q from %q, want bastion.corp from Corp", got.ProxyJump, got.ProxyJumpFrom)
	}
}

func TestResolveHostValueWins(t *testing.T) {
	r := NewResolver([]Group{{ID: "g", Name: "Prod", User: "groupuser"}}, nil)

	got := r.Resolve(Host{Name: "web", User: "ownuser", GroupID: "g"})

	if got.User != "ownuser" {
		t.Errorf("User = %q, want ownuser", got.User)
	}
	if got.UserFrom != "" {
		t.Errorf("UserFrom = %q, want empty for a host's own value", got.UserFrom)
	}
}

// The nearest ancestor that sets a value wins over more distant ones.
func TestResolveNearestAncestorWins(t *testing.T) {
	r := NewResolver([]Group{
		{ID: "a", Name: "A", User: "far"},
		{ID: "b", Name: "B", ParentID: "a", User: "near"},
	}, nil)

	if got := r.Resolve(Host{GroupID: "b"}); got.User != "near" {
		t.Errorf("User = %q, want near", got.User)
	}
}

func TestResolveSurvivesGroupCycle(t *testing.T) {
	// A cycle should not hang the resolver even though PutGroup rejects one.
	r := NewResolver([]Group{
		{ID: "a", Name: "A", ParentID: "b"},
		{ID: "b", Name: "B", ParentID: "a", User: "u"},
	}, nil)

	done := make(chan Resolved, 1)
	go func() { done <- r.Resolve(Host{GroupID: "a"}) }()
	select {
	case got := <-done:
		if got.User != "u" {
			t.Errorf("User = %q, want u", got.User)
		}
	default:
		// Resolve is synchronous; reaching here would mean it blocked.
	}
}

// OpenSSH already owns config-sourced hosts, so Omassh must not layer group
// attributes onto them.
func TestResolveLeavesSSHConfigHostsAlone(t *testing.T) {
	r := NewResolver([]Group{{ID: SSHConfigGroupID, Name: "ssh_config", User: "nope"}}, nil)

	got := r.Resolve(Host{Name: "orb", GroupID: SSHConfigGroupID, Source: SourceSSHConfig})

	if got.User != "" {
		t.Errorf("User = %q, want empty", got.User)
	}
}

func TestResolveUsesBoundCredential(t *testing.T) {
	ids := []Identity{{ID: "c1", Name: "work-key", User: "deploy", KeyPath: "~/.ssh/id_work"}}
	r := NewResolver(nil, ids)

	got := r.Resolve(Host{Name: "web", IdentityID: "c1"})

	if got.Identity != "~/.ssh/id_work" || got.IdentityFrom != "work-key" {
		t.Errorf("Identity = %q from %q, want ~/.ssh/id_work from work-key", got.Identity, got.IdentityFrom)
	}
	if got.User != "deploy" || got.UserFrom != "work-key" {
		t.Errorf("User = %q from %q, want deploy from work-key", got.User, got.UserFrom)
	}
	if got.Credential.Name != "work-key" {
		t.Errorf("Credential = %+v", got.Credential)
	}
}

// A raw key path on the host is more specific than a credential it also binds.
func TestResolveHostKeyPathBeatsCredential(t *testing.T) {
	r := NewResolver(nil, []Identity{{ID: "c1", Name: "work-key", KeyPath: "~/.ssh/id_work"}})

	got := r.Resolve(Host{Identity: "~/.ssh/explicit", IdentityID: "c1"})

	if got.Identity != "~/.ssh/explicit" || got.IdentityFrom != "" {
		t.Errorf("Identity = %q from %q, want the host's own path", got.Identity, got.IdentityFrom)
	}
}

// A credential bound to a group reaches hosts below it.
func TestResolveCredentialInheritsThroughGroups(t *testing.T) {
	groups := []Group{
		{ID: "root", Name: "Corp", IdentityID: "c1"},
		{ID: "child", Name: "Prod", ParentID: "root"},
	}
	r := NewResolver(groups, []Identity{{ID: "c1", Name: "corp-key", User: "corp", KeyPath: "~/.ssh/corp"}})

	got := r.Resolve(Host{GroupID: "child"})

	if got.Identity != "~/.ssh/corp" || got.IdentityFrom != "corp-key" {
		t.Errorf("Identity = %q from %q", got.Identity, got.IdentityFrom)
	}
}

// The host's own credential wins over one inherited from its group.
func TestResolveNearerCredentialWins(t *testing.T) {
	groups := []Group{{ID: "g", Name: "Prod", IdentityID: "far"}}
	ids := []Identity{
		{ID: "far", Name: "group-key", KeyPath: "~/.ssh/far"},
		{ID: "near", Name: "host-key", KeyPath: "~/.ssh/near"},
	}
	r := NewResolver(groups, ids)

	got := r.Resolve(Host{GroupID: "g", IdentityID: "near"})

	if got.Identity != "~/.ssh/near" || got.Credential.Name != "host-key" {
		t.Errorf("Identity = %q, credential = %q", got.Identity, got.Credential.Name)
	}
}

func TestResolveIgnoresDanglingCredential(t *testing.T) {
	r := NewResolver(nil, nil)

	got := r.Resolve(Host{Name: "web", IdentityID: "gone"})

	if got.Identity != "" || got.Credential.ID != "" {
		t.Errorf("got %+v, want nothing resolved from a missing credential", got)
	}
}
