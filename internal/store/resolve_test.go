package store

import "testing"

func TestResolveInheritsFromGroupChain(t *testing.T) {
	groups := []Group{
		{ID: "root", Name: "Corp", User: "corpuser", ProxyJump: "bastion.corp"},
		{ID: "child", Name: "Prod", ParentID: "root", Identity: "~/.ssh/prod"},
	}
	r := NewResolver(groups)

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
	r := NewResolver([]Group{{ID: "g", Name: "Prod", User: "groupuser"}})

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
	})

	if got := r.Resolve(Host{GroupID: "b"}); got.User != "near" {
		t.Errorf("User = %q, want near", got.User)
	}
}

func TestResolveSurvivesGroupCycle(t *testing.T) {
	// A cycle should not hang the resolver even though PutGroup rejects one.
	r := NewResolver([]Group{
		{ID: "a", Name: "A", ParentID: "b"},
		{ID: "b", Name: "B", ParentID: "a", User: "u"},
	})

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
	r := NewResolver([]Group{{ID: SSHConfigGroupID, Name: "ssh_config", User: "nope"}})

	got := r.Resolve(Host{Name: "orb", GroupID: SSHConfigGroupID, Source: SourceSSHConfig})

	if got.User != "" {
		t.Errorf("User = %q, want empty", got.User)
	}
}
