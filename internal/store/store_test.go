package store

import (
	"errors"
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

	if err := s.PutAll(gs, hs, nil); err != nil {
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
	}, []Host{{ID: "h1", Name: "web", Addr: "10.0.0.1"}}, nil)

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
	// The id comes from the store rather than being invented: an id handed to
	// PutGroup means a record read out of it, and one that names nothing is
	// how a group deleted in another window would come back.
	a, err := s.PutGroup(Group{Name: "A"})
	if err != nil {
		t.Fatal(err)
	}
	// b under a, then a moved under b.
	a.ParentID = "b"
	if err := s.PutAll([]Group{
		{ID: "b", Name: "B", ParentID: a.ID},
		a,
	}, nil, nil); err == nil {
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

// A jump host is named, not pointed at, so nothing stopped two hosts naming
// each other. The loop became a ProxyCommand nested until the resolver ran out
// of hops, and ssh answered "Connection closed by UNKNOWN port 65535" — while
// the host list showed one host "via" the other, looking like any other pair.
func TestAHostCannotBeMadeToJumpThroughItself(t *testing.T) {
	s := openTest(t)

	a, err := s.PutHost(Host{Name: "bastion-a", Addr: "10.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.PutHost(Host{Name: "bastion-b", Addr: "10.0.0.2", ProxyJump: "bastion-a"})
	if err != nil {
		t.Fatalf("a plain chain was refused: %v", err)
	}

	// Closing the loop is what must be refused, and the message has to show
	// the way round, since neither host is wrong on its own.
	a.ProxyJump = "bastion-b"
	_, err = s.PutHost(a)
	if err == nil {
		t.Fatal("two hosts were allowed to jump through each other")
	}
	for _, want := range []string{"bastion-a", "bastion-b", "→"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not show the loop (%q missing): %v", want, err)
		}
	}
	_ = b

	// And a host naming itself, whose loop is exactly one hop long: said any
	// longer it reads as a fault in the reporting rather than in the host.
	_, err = s.PutHost(Host{Name: "selfie", Addr: "10.0.0.3", ProxyJump: "selfie"})
	if err == nil {
		t.Fatal("a host was allowed to name itself as its own jump host")
	}
	if !strings.Contains(err.Error(), "selfie → selfie") || strings.Contains(err.Error(), "selfie → selfie → ") {
		t.Errorf("the loop should be one hop: %v", err)
	}
}

// Import writes the lot in one transaction, and a document can name a loop as
// easily as a form can.
func TestAnImportCannotBringInALoop(t *testing.T) {
	s := openTest(t)
	err := s.PutAll(nil, []Host{
		{ID: NewID(), Name: "a", Addr: "10.0.0.1", ProxyJump: "b"},
		{ID: NewID(), Name: "b", Addr: "10.0.0.2", ProxyJump: "a"},
	}, nil)
	if err == nil {
		t.Fatal("an import brought in two hosts jumping through each other")
	}
	if !strings.Contains(err.Error(), "→") {
		t.Errorf("the error does not show the loop: %v", err)
	}
	// All or nothing, as every other refusal in PutAll is.
	if hosts, _ := s.Hosts(); len(hosts) != 0 {
		t.Errorf("a refused import left %d hosts behind", len(hosts))
	}
}

// The loop closes through a group two hops out: the host names its bastion,
// and the bastion takes its own jump host from the group it sits in. Nothing
// on either record looks wrong on its own.
func TestALoopThatClosesThroughAnInheritedJumpHostIsRefused(t *testing.T) {
	s := openTest(t)
	g, err := s.PutGroup(Group{Name: "edge", ProxyJump: "app"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PutHost(Host{Name: "bastion", Addr: "10.0.0.1", GroupID: g.ID}); err != nil {
		t.Fatalf("the bastion could not be saved: %v", err)
	}
	// app → bastion → (group edge) → app
	_, err = s.PutHost(Host{Name: "app", Addr: "10.0.0.2", ProxyJump: "bastion"})
	if err == nil {
		t.Fatal("a loop closing through a group's jump host was allowed")
	}
	for _, want := range []string{"app", "bastion"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the loop does not name %q: %v", want, err)
		}
	}
}

// The chain is followed only through hosts this store holds. Anything else is
// an ssh destination, which is the documented way to reach a bastion omassh
// does not know about.
func TestAJumpHostOmasshDoesNotKnowIsLeftAlone(t *testing.T) {
	s := openTest(t)
	if _, err := s.PutHost(Host{Name: "web", Addr: "10.0.0.9", ProxyJump: "ops@edge.example.com"}); err != nil {
		t.Errorf("an ssh destination was refused as a jump host: %v", err)
	}
}

// A group hands its jump host to everything beneath it, so a loop can be made
// by editing a group alone, with no host written at all.
func TestAGroupCannotPutItsOwnBastionIntoALoop(t *testing.T) {
	s := openTest(t)
	g, err := s.PutGroup(Group{Name: "behind"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PutHost(Host{Name: "bastion", Addr: "10.0.0.1", GroupID: g.ID}); err != nil {
		t.Fatal(err)
	}
	// The bastion is in the group, so pointing the group at it makes the
	// bastion its own jump host.
	g.ProxyJump = "bastion"
	if _, err := s.PutGroup(g); err == nil {
		t.Error("a group was allowed to make its own member jump through itself")
	}
}

// A database written before any of this was checked has to stay editable —
// most of all the host at fault, which is where the loop has to be undone.
func TestALoopAlreadyStoredDoesNotBlockUnrelatedEdits(t *testing.T) {
	s := openTest(t)
	a, _ := s.PutHost(Host{Name: "a", Addr: "10.0.0.1"})
	b, _ := s.PutHost(Host{Name: "b", Addr: "10.0.0.2", ProxyJump: "a"})
	// Put the loop in behind the check, the way an older omassh would have.
	a.ProxyJump = "b"
	if err := s.put(bucketHosts, a.ID, a); err != nil {
		t.Fatal(err)
	}

	other, err := s.PutHost(Host{Name: "unrelated", Addr: "10.0.0.3"})
	if err != nil {
		t.Errorf("an unrelated host could not be saved: %v", err)
	}
	_ = other

	// And the way out: editing the looping host to break the loop.
	a.ProxyJump = ""
	if _, err := s.PutHost(a); err != nil {
		t.Errorf("the looping host could not be mended: %v", err)
	}
	_ = b
}

// jumpChain has to follow inherited jump hosts at every hop, not only the
// first. Walking from every host happens to reveal any loop from one end or
// the other, so this is checked here rather than left to that coincidence.
func TestJumpChainFollowsInheritedJumpHostsAtEveryHop(t *testing.T) {
	edge := Group{ID: "g1", Name: "edge", ProxyJump: "app"}
	app := Host{ID: "h1", Name: "app", ProxyJump: "bastion"}
	bastion := Host{ID: "h2", Name: "bastion", GroupID: "g1"} // takes "app" from edge

	r := NewResolver([]Group{edge}, []Host{app, bastion})
	loop, ok := jumpChain(r, app)
	if !ok {
		t.Fatal("the chain app → bastion → (edge) → app was not followed round")
	}
	if got := strings.Join(loop, " → "); got != "bastion → app" {
		t.Errorf("path = %q, want bastion → app", got)
	}
}

// Two Omassh windows share one database, so a host can be deleted in one while
// the other still has an edit form open on it. Writing it back undid the
// delete — and not even faithfully: DeleteHost takes the host's forwarding
// rules with it, so what returned was the host stripped of them, reported in
// the window that did it as an ordinary "saved".
func TestSavingAHostDeletedElsewhereDoesNotBringItBack(t *testing.T) {
	s := openTest(t)
	h, err := s.PutHost(Host{Name: "delta", Addr: "10.0.0.4"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PutForward(Forward{HostID: h.ID, Kind: ForwardLocal, ListenPort: 5432, Dest: "db", DestPort: 5432}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteHost(h.ID); err != nil {
		t.Fatal(err)
	}

	h.Addr = "10.4.4.4"
	if _, err := s.PutHost(h); !errors.Is(err, ErrNoSuchHost) {
		t.Fatalf("PutHost = %v, want it refused as gone", err)
	}
	if hosts, _ := s.Hosts(); len(hosts) != 0 {
		t.Errorf("the deleted host came back: %+v", hosts)
	}
	if fs, _ := s.Forwards(); len(fs) != 0 {
		t.Errorf("rules came back with it: %+v", fs)
	}
}

// The same for a group, which DeleteGroup empties before removing — so what
// came back was an empty group wearing the name of one that had held things.
func TestSavingAGroupDeletedElsewhereDoesNotBringItBack(t *testing.T) {
	s := openTest(t)
	g, err := s.PutGroup(Group{Name: "Production"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteGroup(g.ID); err != nil {
		t.Fatal(err)
	}

	g.User = "deploy"
	if _, err := s.PutGroup(g); !errors.Is(err, ErrNoSuchGroup) {
		t.Fatalf("PutGroup = %v, want it refused as gone", err)
	}
	if gs, _ := s.Groups(); len(gs) != 0 {
		t.Errorf("the deleted group came back: %+v", gs)
	}
}

// Creating is unaffected: a record with no id is new, and gets one.
func TestCreatingStillWorksWhenTheStoreIsEmpty(t *testing.T) {
	s := openTest(t)
	h, err := s.PutHost(Host{Name: "new", Addr: "10.0.0.1"})
	if err != nil || h.ID == "" {
		t.Fatalf("PutHost = %+v, %v", h, err)
	}
	g, err := s.PutGroup(Group{Name: "new"})
	if err != nil || g.ID == "" {
		t.Fatalf("PutGroup = %+v, %v", g, err)
	}
	// And editing what was just created still works.
	h.Addr = "10.0.0.2"
	if _, err := s.PutHost(h); err != nil {
		t.Errorf("editing a host that is there was refused: %v", err)
	}
}

// A window whose list is older than the store will offer a group another
// window has since deleted. A host written into one belongs to no group that
// exists, and the group panel is how hosts are reached — so it could not be
// got at again.
func TestAHostCannotBeFiledUnderAGroupThatHasGone(t *testing.T) {
	s := openTest(t)
	g, err := s.PutGroup(Group{Name: "Production"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteGroup(g.ID); err != nil {
		t.Fatal(err)
	}

	if _, err := s.PutHost(Host{Name: "web", Addr: "10.0.0.9", GroupID: g.ID}); !errors.Is(err, ErrNoSuchGroup) {
		t.Fatalf("PutHost = %v, want it refused as a group that is gone", err)
	}
	if hosts, _ := s.Hosts(); len(hosts) != 0 {
		t.Errorf("the host was written anyway: %+v", hosts)
	}
	// Without a group it is fine, and that is where such a host belongs.
	if _, err := s.PutHost(Host{Name: "web", Addr: "10.0.0.9"}); err != nil {
		t.Errorf("an ungrouped host was refused: %v", err)
	}
}
