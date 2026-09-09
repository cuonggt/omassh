package store

import (
	"path/filepath"
	"strings"
	"testing"

	bolt "go.etcd.io/bbolt"
)

func TestForwardSpec(t *testing.T) {
	for _, c := range []struct {
		name       string
		f          Forward
		flag, spec string
	}{
		{
			"local",
			Forward{Kind: ForwardLocal, ListenPort: 5432, Dest: "db.internal", DestPort: 5432},
			"-L", "5432:db.internal:5432",
		},
		{
			"local bound to one interface",
			Forward{Kind: ForwardLocal, Listen: "127.0.0.1", ListenPort: 8080, Dest: "web", DestPort: 80},
			"-L", "127.0.0.1:8080:web:80",
		},
		{
			"remote",
			Forward{Kind: ForwardRemote, ListenPort: 8080, Dest: "localhost", DestPort: 3000},
			"-R", "8080:localhost:3000",
		},
		{
			"dynamic takes no destination",
			Forward{Kind: ForwardDynamic, ListenPort: 1080},
			"-D", "1080",
		},
		{
			// Unbracketed, the colons in the address are read as the
			// separators between the four fields of the spec.
			"an IPv6 literal is bracketed",
			Forward{Kind: ForwardLocal, Listen: "::1", ListenPort: 8080, Dest: "fd00::5", DestPort: 80},
			"-L", "[::1]:8080:[fd00::5]:80",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := c.f.Flag(); got != c.flag {
				t.Errorf("Flag() = %q, want %q", got, c.flag)
			}
			if got := c.f.Spec(); got != c.spec {
				t.Errorf("Spec() = %q, want %q", got, c.spec)
			}
		})
	}
}

// What the form shows has to be what the form can read back, or editing a rule
// and saving it unchanged would not leave it unchanged.
func TestForwardTextRoundTrips(t *testing.T) {
	for _, f := range []Forward{
		{Kind: ForwardLocal, ListenPort: 5432, Dest: "db.internal", DestPort: 5432},
		{Kind: ForwardLocal, Listen: "127.0.0.1", ListenPort: 8080, Dest: "web", DestPort: 80},
		{Kind: ForwardLocal, Listen: "::1", ListenPort: 8080, Dest: "fd00::5", DestPort: 80},
	} {
		addr, port, err := ParseListen(f.ListenText())
		if err != nil {
			t.Fatalf("ParseListen(%q): %v", f.ListenText(), err)
		}
		if addr != f.Listen || port != f.ListenPort {
			t.Errorf("listen %q read back as %q:%d, want %q:%d", f.ListenText(), addr, port, f.Listen, f.ListenPort)
		}
		addr, port, err = ParseDest(f.DestText())
		if err != nil {
			t.Fatalf("ParseDest(%q): %v", f.DestText(), err)
		}
		if addr != f.Dest || port != f.DestPort {
			t.Errorf("dest %q read back as %q:%d, want %q:%d", f.DestText(), addr, port, f.Dest, f.DestPort)
		}
	}
}

func TestParseListen(t *testing.T) {
	for _, c := range []struct {
		in   string
		addr string
		port int
		bad  bool
	}{
		{in: "5432", port: 5432},
		{in: "127.0.0.1:5432", addr: "127.0.0.1", port: 5432},
		{in: "*:5432", addr: "*", port: 5432},
		{in: "[::1]:5432", addr: "::1", port: 5432},
		{in: "", bad: true},
		{in: "http", bad: true},
		{in: "0", bad: true},
		{in: "70000", bad: true},
		{in: "localhost:0", bad: true},
	} {
		addr, port, err := ParseListen(c.in)
		if c.bad {
			if err == nil {
				t.Errorf("ParseListen(%q) = %q:%d, want an error", c.in, addr, port)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseListen(%q): %v", c.in, err)
			continue
		}
		if addr != c.addr || port != c.port {
			t.Errorf("ParseListen(%q) = %q:%d, want %q:%d", c.in, addr, port, c.addr, c.port)
		}
	}
}

// A destination has no default, so a bare port is a mistake rather than
// shorthand — and left to ssh it would be a spec with too few fields.
func TestParseDestNeedsBothHalves(t *testing.T) {
	if _, _, err := ParseDest("5432"); err == nil {
		t.Error("a bare port was accepted as a destination")
	}
	if _, _, err := ParseDest(""); err == nil {
		t.Error("an empty destination was accepted")
	}
	addr, port, err := ParseDest("db.internal:5432")
	if err != nil || addr != "db.internal" || port != 5432 {
		t.Errorf("ParseDest = %q:%d, %v", addr, port, err)
	}
}

func TestForwardValidate(t *testing.T) {
	ok := Forward{Kind: ForwardLocal, ListenPort: 5432, Dest: "db", DestPort: 5432}
	if err := ok.Validate(); err != nil {
		t.Errorf("a complete rule was rejected: %v", err)
	}
	// A dynamic forward decides its destination per connection, so it needs
	// none — and demanding one would make the kind unusable.
	dyn := Forward{Kind: ForwardDynamic, ListenPort: 1080}
	if err := dyn.Validate(); err != nil {
		t.Errorf("a dynamic rule was rejected: %v", err)
	}
	for name, f := range map[string]Forward{
		"no kind":        {ListenPort: 1, Dest: "db", DestPort: 1},
		"unknown kind":   {Kind: "sideways", ListenPort: 1, Dest: "db", DestPort: 1},
		"no listen port": {Kind: ForwardLocal, Dest: "db", DestPort: 1},
		"no destination": {Kind: ForwardLocal, ListenPort: 1, DestPort: 1},
		"no dest port":   {Kind: ForwardLocal, ListenPort: 1, Dest: "db"},
	} {
		if err := f.Validate(); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestForwardsRoundTrip(t *testing.T) {
	s := openTest(t)
	h, err := s.PutHost(Host{Name: "db-01", Addr: "10.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}

	saved, err := s.PutForward(Forward{
		HostID: h.ID, Kind: ForwardLocal,
		ListenPort: 5432, Dest: "localhost", DestPort: 5432,
	})
	if err != nil {
		t.Fatalf("PutForward: %v", err)
	}
	if saved.ID == "" {
		t.Fatal("no id was minted")
	}

	got, err := s.Forwards()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != saved.ID || got[0].Spec() != "5432:localhost:5432" {
		t.Fatalf("got %+v", got)
	}

	// Editing keeps the id, which is what the running tunnel is named after.
	saved.ListenPort = 15432
	if _, err := s.PutForward(saved); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Forwards()
	if len(got) != 1 || got[0].ID != saved.ID || got[0].ListenPort != 15432 {
		t.Fatalf("editing did not update in place: %+v", got)
	}

	if err := s.DeleteForward(saved.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ = s.Forwards(); len(got) != 0 {
		t.Fatalf("delete left %+v", got)
	}
}

// A rule that will not decode must not take the others with it, the same way
// a host does not.
func TestForwardsSurviveAnUnreadableRecord(t *testing.T) {
	s := openTest(t)
	if _, err := s.PutForward(Forward{
		HostID: "h1", Kind: ForwardLocal, ListenPort: 5432, Dest: "db", DestPort: 5432,
	}); err != nil {
		t.Fatal(err)
	}
	corrupt(t, s, bucketForwards, "broken")

	got, err := s.Forwards()
	if err == nil {
		t.Error("nothing was said about the unreadable record")
	} else if !strings.Contains(err.Error(), "broken") {
		t.Errorf("the complaint does not name the record: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("got %d rules, want the one that reads", len(got))
	}
}

// A rule names a host by id and means nothing without it, so it goes when the
// host does — otherwise the store keeps tunnels nothing can start or see.
func TestDeletingAHostTakesItsForwards(t *testing.T) {
	s := openTest(t)
	keep, _ := s.PutHost(Host{Name: "keep", Addr: "10.0.0.2"})
	doomed, _ := s.PutHost(Host{Name: "doomed", Addr: "10.0.0.1"})

	for _, hostID := range []string{doomed.ID, doomed.ID, keep.ID} {
		if _, err := s.PutForward(Forward{
			HostID: hostID, Kind: ForwardLocal,
			ListenPort: 5432, Dest: "db", DestPort: 5432,
		}); err != nil {
			t.Fatal(err)
		}
	}

	if err := s.DeleteHost(doomed.ID); err != nil {
		t.Fatal(err)
	}
	got, err := s.Forwards()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].HostID != keep.ID {
		t.Fatalf("got %+v, want only the rule belonging to the host that stayed", got)
	}
}

// A database written before forwards existed has no such bucket, and opening
// it must make one rather than failing on every read.
func TestAStoreWithoutTheForwardsBucketOpens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	// Back to what a store written before forwards existed looks like.
	err = s.write(func(tx *bolt.Tx) error { return tx.DeleteBucket(bucketForwards) })
	if err != nil {
		t.Fatal(err)
	}

	s, err = Open(path)
	if err != nil {
		t.Fatalf("reopening a store without the bucket: %v", err)
	}
	if got, err := s.Forwards(); err != nil || len(got) != 0 {
		t.Fatalf("Forwards() = %+v, %v", got, err)
	}
}
