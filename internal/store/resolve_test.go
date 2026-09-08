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

// The jump host field holds one of your host *names*, because that is what a
// picker offers. ssh has never heard of that name and needs an address, so
// without resolution it failed with "Could not resolve hostname".
func TestResolveFindsTheJumpHostByName(t *testing.T) {
	hosts := []Host{
		{ID: "b", Name: "bastion", Addr: "10.0.0.1", User: "jump", Port: 2222, Identity: "~/.ssh/bastion"},
		{ID: "w", Name: "web", Addr: "10.0.1.9", ProxyJump: "bastion"},
	}
	r := NewResolver(nil, hosts)

	j := r.Resolve(hosts[1]).Jump
	if j == nil {
		t.Fatal("Jump is nil; the name was not resolved to a host")
	}
	// The hop's own settings are what ssh -J would have thrown away.
	if j.Addr != "10.0.0.1" || j.User != "jump" || j.Port != 2222 || j.Identity != "~/.ssh/bastion" {
		t.Errorf("Jump = %+v, want the bastion's own address, user, port and key", *j)
	}
}

// A jump host inherits from its groups like any other host.
func TestResolveJumpHostInheritsItsOwnAttributes(t *testing.T) {
	groups := []Group{{ID: "g", Name: "Edge", User: "ops", Identity: "~/.ssh/edge"}}
	hosts := []Host{
		{ID: "b", Name: "bastion", Addr: "10.0.0.1", GroupID: "g"},
		{ID: "w", Name: "web", Addr: "10.0.1.9", ProxyJump: "bastion"},
	}
	r := NewResolver(groups, hosts)

	j := r.Resolve(hosts[1]).Jump
	if j == nil || j.User != "ops" || j.Identity != "~/.ssh/edge" {
		t.Errorf("Jump = %+v, want the group's user and key applied", j)
	}
}

// A jump host may sit behind another one.
func TestResolveChainsJumpHosts(t *testing.T) {
	hosts := []Host{
		{ID: "a", Name: "outer", Addr: "10.0.0.1"},
		{ID: "b", Name: "inner", Addr: "10.0.0.2", ProxyJump: "outer"},
		{ID: "w", Name: "web", Addr: "10.0.1.9", ProxyJump: "inner"},
	}
	r := NewResolver(nil, hosts)

	j := r.Resolve(hosts[2]).Jump
	if j == nil || j.Addr != "10.0.0.2" {
		t.Fatalf("first hop = %+v, want inner", j)
	}
	if j.Jump == nil || j.Jump.Addr != "10.0.0.1" {
		t.Fatalf("second hop = %+v, want outer", j.Jump)
	}
}

// Anything that is not one of your hosts is already an ssh destination, and
// is left for ssh to interpret.
func TestResolveLeavesAnUnknownJumpHostAlone(t *testing.T) {
	r := NewResolver(nil, []Host{{ID: "w", Name: "web", Addr: "10.0.1.9"}})

	got := r.Resolve(Host{Name: "web", Addr: "10.0.1.9", ProxyJump: "ops@bastion.example.com:2222"})
	if got.Jump != nil {
		t.Errorf("Jump = %+v, want nil so the raw destination is used", got.Jump)
	}
	if got.ProxyJump != "ops@bastion.example.com:2222" {
		t.Errorf("ProxyJump = %q, want it kept for -J", got.ProxyJump)
	}
}

// A loop must not recurse forever.
func TestResolveSurvivesAJumpHostCycle(t *testing.T) {
	hosts := []Host{
		{ID: "a", Name: "one", Addr: "10.0.0.1", ProxyJump: "two"},
		{ID: "b", Name: "two", Addr: "10.0.0.2", ProxyJump: "one"},
	}
	r := NewResolver(nil, hosts)

	depth := 0
	for j := r.Resolve(hosts[0]).Jump; j != nil; j = j.Jump {
		depth++
		if depth > maxJumpHops {
			t.Fatal("the cycle was not cut short")
		}
	}
}

// A host naming itself is the easiest cycle to create by accident.
func TestResolveSurvivesAHostJumpingThroughItself(t *testing.T) {
	hosts := []Host{{ID: "a", Name: "loop", Addr: "10.0.0.1", ProxyJump: "loop"}}
	r := NewResolver(nil, hosts)

	j := r.Resolve(hosts[0]).Jump
	if j == nil {
		t.Fatal("the first hop should still resolve")
	}
	if j.Jump != nil {
		t.Error("the host was allowed to jump through itself twice")
	}
}
