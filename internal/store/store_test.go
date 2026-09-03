package store

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestPutAndReadBack(t *testing.T) {
	s := openTest(t)

	g, err := s.PutGroup(Group{Name: "Production", User: "admin"})
	if err != nil {
		t.Fatalf("PutGroup: %v", err)
	}
	if g.ID == "" {
		t.Fatal("PutGroup did not assign an id")
	}

	h, err := s.PutHost(Host{Name: "web", Addr: "10.0.0.1", Port: 2222, GroupID: g.ID, Tags: []string{"prod"}})
	if err != nil {
		t.Fatalf("PutHost: %v", err)
	}

	hosts, err := s.Hosts()
	if err != nil || len(hosts) != 1 {
		t.Fatalf("Hosts() = %v, %v; want 1 host", hosts, err)
	}
	got := hosts[0]
	if got.ID != h.ID || got.Addr != "10.0.0.1" || got.Port != 2222 || got.GroupID != g.ID {
		t.Errorf("round-trip mismatch: %+v", got)
	}
	if len(got.Tags) != 1 || got.Tags[0] != "prod" {
		t.Errorf("Tags = %v, want [prod]", got.Tags)
	}
}

func TestPutHostUpdatesInPlace(t *testing.T) {
	s := openTest(t)
	h, _ := s.PutHost(Host{Name: "web", Addr: "old"})
	h.Addr = "new"
	if _, err := s.PutHost(h); err != nil {
		t.Fatalf("PutHost update: %v", err)
	}

	hosts, _ := s.Hosts()
	if len(hosts) != 1 {
		t.Fatalf("got %d hosts, want 1 (update must not insert)", len(hosts))
	}
	if hosts[0].Addr != "new" {
		t.Errorf("Addr = %q, want new", hosts[0].Addr)
	}
}

// Deleting a group must never delete hosts; they move up to its parent.
func TestDeleteGroupReparentsRatherThanDeletes(t *testing.T) {
	s := openTest(t)
	root, _ := s.PutGroup(Group{Name: "Corp"})
	mid, _ := s.PutGroup(Group{Name: "Prod", ParentID: root.ID})
	leaf, _ := s.PutGroup(Group{Name: "EU", ParentID: mid.ID})
	h, _ := s.PutHost(Host{Name: "web", GroupID: mid.ID})

	if gs, hs := s.Counts(mid.ID); gs != 1 || hs != 1 {
		t.Errorf("Counts = %d groups, %d hosts; want 1, 1", gs, hs)
	}
	if err := s.DeleteGroup(mid.ID); err != nil {
		t.Fatalf("DeleteGroup: %v", err)
	}

	groups, _ := s.Groups()
	if len(groups) != 2 {
		t.Fatalf("got %d groups, want 2", len(groups))
	}
	for _, g := range groups {
		if g.ID == leaf.ID && g.ParentID != root.ID {
			t.Errorf("child group parent = %q, want %q", g.ParentID, root.ID)
		}
	}
	hosts, _ := s.Hosts()
	if len(hosts) != 1 {
		t.Fatalf("host was deleted along with its group")
	}
	if hosts[0].ID != h.ID || hosts[0].GroupID != root.ID {
		t.Errorf("host group = %q, want %q", hosts[0].GroupID, root.ID)
	}
}

func TestPutGroupRejectsCycles(t *testing.T) {
	s := openTest(t)
	a, _ := s.PutGroup(Group{Name: "A"})
	b, _ := s.PutGroup(Group{Name: "B", ParentID: a.ID})

	a.ParentID = b.ID
	if _, err := s.PutGroup(a); err == nil {
		t.Error("PutGroup accepted a cycle, want error")
	}

	self := Group{ID: a.ID, Name: "A", ParentID: a.ID}
	if _, err := s.PutGroup(self); err == nil {
		t.Error("PutGroup accepted a self-parent, want error")
	}
}

func TestSessionHistory(t *testing.T) {
	s := openTest(t)
	now := time.Now().Truncate(time.Second)

	for range 3 {
		if err := s.RecordSession("cfg:orb", now); err != nil {
			t.Fatalf("RecordSession: %v", err)
		}
	}
	stats, err := s.Stats()
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if got := stats["cfg:orb"]; got.Count != 3 || !got.LastSeen.Equal(now) {
		t.Errorf("stat = %+v, want count 3 at %v", got, now)
	}

	// An empty key (an unsaved host) is a no-op, not an error.
	if err := s.RecordSession("", now); err != nil {
		t.Errorf("RecordSession(\"\") = %v, want nil", err)
	}
}

func TestPersistsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	s.PutHost(Host{Name: "web", Addr: "10.0.0.1"})
	s.Close()

	s2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	hosts, _ := s2.Hosts()
	if len(hosts) != 1 || hosts[0].Name != "web" {
		t.Errorf("after reopen got %v, want the saved host", hosts)
	}
}

func TestIdentityCRUD(t *testing.T) {
	s := openTest(t)

	id, err := s.PutIdentity(Identity{Name: "work-key", User: "deploy", KeyPath: "~/.ssh/id_work"})
	if err != nil || id.ID == "" {
		t.Fatalf("PutIdentity = %+v, %v", id, err)
	}

	got, err := s.Identities()
	if err != nil || len(got) != 1 {
		t.Fatalf("Identities = %v, %v", got, err)
	}
	if got[0].Name != "work-key" || got[0].User != "deploy" {
		t.Errorf("round-trip mismatch: %+v", got[0])
	}
	// The secret must never be part of the record.
	if got[0].HasSecret {
		t.Error("HasSecret set without one being stored")
	}
}

// Deleting a credential must not leave hosts or groups pointing at a dead id.
func TestDeleteIdentityUnbindsReferences(t *testing.T) {
	s := openTest(t)
	id, _ := s.PutIdentity(Identity{Name: "work-key", KeyPath: "~/.ssh/id_work"})
	g, _ := s.PutGroup(Group{Name: "Prod", IdentityID: id.ID})
	h, _ := s.PutHost(Host{Name: "web", GroupID: g.ID, IdentityID: id.ID})

	if hs, gs := s.IdentityUsage(id.ID); hs != 1 || gs != 1 {
		t.Errorf("IdentityUsage = %d hosts, %d groups; want 1, 1", hs, gs)
	}
	if err := s.DeleteIdentity(id.ID); err != nil {
		t.Fatalf("DeleteIdentity: %v", err)
	}

	hosts, _ := s.Hosts()
	if len(hosts) != 1 || hosts[0].ID != h.ID {
		t.Fatal("host was deleted along with the credential")
	}
	if hosts[0].IdentityID != "" {
		t.Errorf("host still bound to %q", hosts[0].IdentityID)
	}
	groups, _ := s.Groups()
	if len(groups) != 1 || groups[0].IdentityID != "" {
		t.Errorf("group still bound to %q", groups[0].IdentityID)
	}
	ids, _ := s.Identities()
	if len(ids) != 0 {
		t.Errorf("Identities = %v, want empty", ids)
	}
}

// A second instance must say why it cannot start. bbolt's own error is a bare
// "timeout", which gives the user nothing to act on.
func TestSecondInstanceExplainsTheLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "locked.db")
	first, err := Open(path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	defer first.Close()

	_, err = Open(path)
	if err == nil {
		t.Fatal("second Open succeeded while the database was locked")
	}
	if !strings.Contains(err.Error(), "already in use") {
		t.Errorf("error = %q, want it to name the cause", err)
	}
	t.Logf("reported: %v", err)

	// And it must recover once the holder lets go.
	first.Close()
	second, err := Open(path)
	if err != nil {
		t.Fatalf("Open after the lock was released: %v", err)
	}
	second.Close()
}
