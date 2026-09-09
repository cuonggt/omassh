package store

import (
	"fmt"
	"path/filepath"
	"sync"

	bolt "go.etcd.io/bbolt"
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

// A second Omassh on the same database is allowed, and has to be: the default
// way to connect hands the whole terminal to ssh, so the only way to look the
// next host up is another window. The file is held for the length of an
// operation, not the length of the program.
func TestTwoInstancesShareOneDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared.db")
	first, err := Open(path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	defer first.Close()

	second, err := Open(path)
	if err != nil {
		t.Fatalf("a second Omassh could not open the same database: %v", err)
	}
	defer second.Close()

	// What one writes, the other sees on its next read.
	if _, err := first.PutHost(Host{Name: "web", Addr: "10.0.0.1"}); err != nil {
		t.Fatal(err)
	}
	hosts, err := second.Hosts()
	if err != nil {
		t.Fatal(err)
	}
	if len(hosts) != 1 || hosts[0].Name != "web" {
		t.Fatalf("the second instance sees %v, want the host the first wrote", hosts)
	}

	// And the other way round.
	if _, err := second.PutHost(Host{Name: "db", Addr: "10.0.0.2"}); err != nil {
		t.Fatal(err)
	}
	if hosts, err = first.Hosts(); err != nil {
		t.Fatal(err)
	}
	if len(hosts) != 2 {
		t.Errorf("the first instance sees %d hosts, want both", len(hosts))
	}
}

// corrupt puts a value into a bucket that will not decode, the way a damaged
// file or an older format would.
func corrupt(t *testing.T, s *Store, bucket []byte, key string) {
	t.Helper()
	err := s.write(func(tx *bolt.Tx) error {
		return tx.Bucket(bucket).Put([]byte(key), []byte("{not json at all"))
	})
	if err != nil {
		t.Fatal(err)
	}
}

// One record that will not decode used to end the walk over its bucket, so it
// took every other with it: the list came back empty and the interface offered
// to add a host to a store that already held three.
func TestOneUnreadableRecordDoesNotHideTheRest(t *testing.T) {
	s := openTest(t)
	for _, n := range []string{"alpha", "bravo", "charlie"} {
		if _, err := s.PutHost(Host{Name: n, Addr: "10.0.0.1"}); err != nil {
			t.Fatal(err)
		}
	}
	corrupt(t, s, bucketHosts, "badrecord")

	hosts, err := s.Hosts()
	if len(hosts) != 3 {
		t.Fatalf("got %d hosts, want the 3 that are readable", len(hosts))
	}
	if err == nil {
		t.Fatal("nothing said a record had been skipped")
	}
	if !strings.Contains(err.Error(), "badrecord") {
		t.Errorf("the error does not name the record: %v", err)
	}
}

func TestAnUnreadableGroupDoesNotHideTheRest(t *testing.T) {
	s := openTest(t)
	if _, err := s.PutGroup(Group{Name: "Production"}); err != nil {
		t.Fatal(err)
	}
	corrupt(t, s, bucketGroups, "badgroup")

	groups, err := s.Groups()
	if len(groups) != 1 || groups[0].Name != "Production" {
		t.Fatalf("got %v, want the one readable group", groups)
	}
	if err == nil || !strings.Contains(err.Error(), "badgroup") {
		t.Errorf("err = %v, want it to name the skipped record", err)
	}
}

// Session history for one host is not worth losing everyone else's.
func TestAnUnreadableStatDoesNotHideTheRest(t *testing.T) {
	s := openTest(t)
	if err := s.RecordSession("keep", time.Now()); err != nil {
		t.Fatal(err)
	}
	corrupt(t, s, bucketStats, "badstat")

	stats, err := s.Stats()
	if err != nil {
		t.Fatalf("Stats() = %v", err)
	}
	if _, ok := stats["keep"]; !ok {
		t.Errorf("got %v, want the readable history", stats)
	}
}

// Everything at once, in one transaction.
func TestPutAllWritesTheLot(t *testing.T) {
	s := openTest(t)
	gs := []Group{{ID: "g1", Name: "Production"}, {ID: "g2", Name: "EU", ParentID: "g1"}}
	hs := []Host{{ID: "h1", Name: "web", Addr: "10.0.0.1", GroupID: "g2"}}

	if err := s.PutAll(gs, hs); err != nil {
		t.Fatal(err)
	}
	groups, _ := s.Groups()
	hosts, _ := s.Hosts()
	if len(groups) != 2 || len(hosts) != 1 {
		t.Fatalf("got %d groups and %d hosts, want 2 and 1", len(groups), len(hosts))
	}
	if hosts[0].GroupID != "g2" {
		t.Errorf("the host lost its group: %+v", hosts[0])
	}
}

// A set that would leave a group as its own ancestor is refused, and refused
// whole: one transaction means nothing of it lands.
func TestPutAllRefusesACycleAndWritesNothing(t *testing.T) {
	s := openTest(t)
	err := s.PutAll([]Group{
		{ID: "a", Name: "A", ParentID: "b"},
		{ID: "b", Name: "B", ParentID: "a"},
	}, []Host{{ID: "h1", Name: "web", Addr: "10.0.0.1"}})

	if err == nil {
		t.Fatal("a cycle was accepted")
	}
	groups, _ := s.Groups()
	hosts, _ := s.Hosts()
	if len(groups) != 0 || len(hosts) != 0 {
		t.Errorf("part of a refused write landed: %d groups, %d hosts", len(groups), len(hosts))
	}
}

// A cycle formed with what is already stored is caught too.
func TestPutAllSeesTheGroupsAlreadyThere(t *testing.T) {
	s := openTest(t)
	if _, err := s.PutGroup(Group{ID: "a", Name: "A"}); err != nil {
		t.Fatal(err)
	}
	// b under a, then a moved under b.
	if err := s.PutAll([]Group{
		{ID: "b", Name: "B", ParentID: "a"},
		{ID: "a", Name: "A", ParentID: "b"},
	}, nil); err == nil {
		t.Error("a cycle formed against the stored groups was accepted")
	}
}

// Writing from two instances at once. The file is taken for the length of an
// operation, so they queue behind each other rather than collide, and nothing
// written is lost.
func TestConcurrentInstancesDoNotLoseWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "busy.db")
	a, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	const each = 20
	var wg sync.WaitGroup
	for i, s := range []*Store{a, b} {
		wg.Add(1)
		go func(n int, st *Store) {
			defer wg.Done()
			for j := range each {
				h := Host{Name: fmt.Sprintf("i%d-h%02d", n, j), Addr: "10.0.0.1"}
				if _, err := st.PutHost(h); err != nil {
					t.Errorf("instance %d writing %s: %v", n, h.Name, err)
					return
				}
			}
		}(i, s)
	}
	wg.Wait()

	hosts, err := a.Hosts()
	if err != nil {
		t.Fatal(err)
	}
	if len(hosts) != each*2 {
		t.Errorf("the database holds %d hosts, want %d — writes were lost", len(hosts), each*2)
	}
}

// Reading while another instance writes: a reader waits its turn and sees a
// whole state, never half of a write.
func TestReadingWhileAnotherInstanceWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mixed.db")
	writer, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	reader, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range 30 {
			if _, err := writer.PutHost(Host{Name: fmt.Sprintf("h%02d", i), Addr: "10.0.0.1"}); err != nil {
				t.Errorf("write %d: %v", i, err)
				return
			}
		}
	}()

	for {
		select {
		case <-done:
			hosts, err := reader.Hosts()
			if err != nil {
				t.Fatal(err)
			}
			if len(hosts) != 30 {
				t.Errorf("after the writer finished the reader sees %d hosts, want 30", len(hosts))
			}
			return
		default:
			if _, err := reader.Hosts(); err != nil {
				t.Fatalf("reading while another instance writes: %v", err)
			}
		}
	}
}
