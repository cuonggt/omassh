package store

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"
)

var (
	bucketGroups = []byte("groups")
	bucketHosts  = []byte("hosts")
	bucketStats  = []byte("stats")
	// Forwards arrived after the first release, so a database made before
	// them has no such bucket. Open creates whatever is missing, which is why
	// nothing here has to ask which version of Omassh wrote the file.
	bucketForwards = []byte("forwards")
)

// Store is the on-disk database of locally-defined hosts and groups, plus
// session history for every host Omassh has connected to.
//
// It holds a path rather than an open database. bbolt takes the file
// exclusively, so holding it open for the life of the program meant a second
// Omassh could not start at all — which the default way to connect makes
// necessary, since handing the whole terminal to ssh leaves no interface to
// look the next host up in. Each operation opens the file for as long as it
// takes and no longer, which is how bbolt is meant to be shared.
type Store struct {
	path string
}

// lockWait is how long an operation waits for another one to finish with the
// file. Operations are short, so contention resolves in milliseconds; this is
// long enough to cover an import of a few thousand records.
const lockWait = 5 * time.Second

// DefaultPath returns the database path: ~/Library/Application Support/omassh
// /omassh.db on macOS, ~/.config/omassh/omassh.db on Linux. omassh -h prints
// the resolved path.
func DefaultPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "omassh", "omassh.db"), nil
}

// Open prepares the store at path, creating the file and its buckets if they
// are not already there, and checking that it can be read.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	s := &Store{path: path}
	// Made once, so every operation after this can take the buckets as given.
	err := s.write(func(tx *bolt.Tx) error {
		for _, b := range [][]byte{bucketGroups, bucketHosts, bucketStats, bucketForwards} {
			if _, err := tx.CreateBucketIfNotExists(b); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s, nil
}

// Close is here for callers that pair it with Open. Nothing is held open
// between operations, so there is nothing to release.
func (s *Store) Close() error { return nil }

// with opens the database for one operation and closes it again.
func (s *Store) with(fn func(*bolt.DB) error) error {
	db, err := bolt.Open(s.path, 0o600, &bolt.Options{Timeout: lockWait})
	if err != nil {
		// A timeout now means real contention rather than a second Omassh
		// merely being open, since the file is only held for the length of an
		// operation.
		if errors.Is(err, bolt.ErrTimeout) {
			return fmt.Errorf("%s is busy — another omassh has been writing to it for over %s", s.path, lockWait)
		}
		// bolt hands back the os error exactly as it came, and that already
		// names the file and what was attempted — so wrapping it whole said
		// both twice: "open …/omassh.db: open …/omassh.db: is a directory",
		// with the path long enough on its own to fill a line.
		var pe *os.PathError
		if errors.As(err, &pe) {
			return fmt.Errorf("open %s: %w", s.path, pe.Err)
		}
		return fmt.Errorf("open %s: %w", s.path, err)
	}
	defer db.Close()
	return fn(db)
}

func (s *Store) read(fn func(*bolt.Tx) error) error {
	return s.with(func(db *bolt.DB) error { return db.View(fn) })
}

func (s *Store) write(fn func(*bolt.Tx) error) error {
	return s.with(func(db *bolt.DB) error { return db.Update(fn) })
}

// decodeGroups and decodeHosts read a bucket, keeping what decodes and naming
// what does not. Reading inside a caller's transaction is what lets a write
// see the current state without opening the file a second time.
func decodeGroups(b *bolt.Bucket) (out []Group, bad []string) {
	b.ForEach(func(k, v []byte) error {
		var g Group
		if err := json.Unmarshal(v, &g); err != nil {
			bad = append(bad, string(k))
			return nil
		}
		out = append(out, g)
		return nil
	})
	return out, bad
}

func decodeHosts(b *bolt.Bucket) (out []Host, bad []string) {
	b.ForEach(func(k, v []byte) error {
		var h Host
		if err := json.Unmarshal(v, &h); err != nil {
			bad = append(bad, string(k))
			return nil
		}
		out = append(out, h)
		return nil
	})
	return out, bad
}

func decodeForwards(b *bolt.Bucket) (out []Forward, bad []string) {
	b.ForEach(func(k, v []byte) error {
		var f Forward
		if err := json.Unmarshal(v, &f); err != nil {
			bad = append(bad, string(k))
			return nil
		}
		out = append(out, f)
		return nil
	})
	return out, bad
}

func (s *Store) Groups() ([]Group, error) {
	var out []Group
	var bad []string
	err := s.read(func(tx *bolt.Tx) error {
		out, bad = decodeGroups(tx.Bucket(bucketGroups))
		return nil
	})
	sortGroups(out)
	if err == nil {
		err = unreadable("group", bad)
	}
	return out, err
}

func (s *Store) Hosts() ([]Host, error) {
	var out []Host
	var bad []string
	err := s.read(func(tx *bolt.Tx) error {
		out, bad = decodeHosts(tx.Bucket(bucketHosts))
		return nil
	})
	SortHosts(out)
	if err == nil {
		err = unreadable("host", bad)
	}
	return out, err
}

// Forwards lists every port-forwarding rule, ordered by the port it binds.
func (s *Store) Forwards() ([]Forward, error) {
	var out []Forward
	var bad []string
	err := s.read(func(tx *bolt.Tx) error {
		out, bad = decodeForwards(tx.Bucket(bucketForwards))
		return nil
	})
	SortForwards(out)
	if err == nil {
		err = unreadable("forward", bad)
	}
	return out, err
}

// unreadable names records that could not be decoded, so they are neither
// hidden nor allowed to hide anything else.
//
// A record that will not decode used to end the walk over its bucket, so one
// of them took every other with it: the list came back empty and the interface
// offered to add a host to a store that already held them, over an error from
// the JSON parser that named neither the database nor the record.
func unreadable(kind string, keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	noun := kind
	if len(keys) > 1 {
		noun += "s"
	}
	shown := keys
	if len(shown) > 3 {
		shown = shown[:3]
	}
	list := strings.Join(shown, ", ")
	if len(keys) > len(shown) {
		list += ", …"
	}
	return fmt.Errorf("skipped %d unreadable %s (%s) — everything else is here",
		len(keys), noun, list)
}

// PutGroup inserts or updates a group, assigning an id when absent.
func (s *Store) PutGroup(g Group) (Group, error) {
	update := g.ID != ""
	if !update {
		g.ID = NewID()
	}
	// Checked and written in one transaction: a group may not be its own
	// ancestor, or the resolver and the tree walk would both need to defend
	// against it at every read, and checking in a transaction of its own would
	// leave a gap for another Omassh to change the tree in between.
	err := s.write(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketGroups)
		// As for a host: a group deleted in another window must not come back
		// because this one still had it open. DeleteGroup re-parents what was
		// inside it before removing it, so what returned was an empty group
		// wearing the name of one that had held things.
		if update && b.Get([]byte(g.ID)) == nil {
			return ErrNoSuchGroup
		}
		if err := acyclicIn(b, []Group{g}); err != nil {
			return err
		}
		// A group supplies a jump host to every host under it, so editing one
		// can put a host into a loop without that host being written at all.
		if err := noNewJumpLoop(b, tx.Bucket(bucketHosts), []Group{g}, nil); err != nil {
			return err
		}
		enc, err := json.Marshal(g)
		if err != nil {
			return err
		}
		return b.Put([]byte(g.ID), enc)
	})
	return g, err
}

func (s *Store) PutHost(h Host) (Host, error) {
	// An id coming in means this is an edit of a record that was read out of
	// the store; without one it is a new host, and the id is minted here.
	update := h.ID != ""
	if !update {
		h.ID = NewID()
	}
	// Checked and written in one transaction, as a group's parent is, and for
	// the same reason: a loop is not something the resolver can report from
	// where it finds it.
	err := s.write(func(tx *bolt.Tx) error {
		hb := tx.Bucket(bucketHosts)
		// Another Omassh may have deleted this host while the form was open,
		// and writing it back would undo that — silently, and not even
		// faithfully. DeleteHost takes the host's forwarding rules with it, so
		// what returned was the host stripped of them, reported as an ordinary
		// save in the window that did it and invisible in the window that had
		// done the deleting. PutForward has checked exactly this, in exactly
		// this way, since a rule could be orphaned by the same race.
		if update && hb.Get([]byte(h.ID)) == nil {
			return ErrNoSuchHost
		}
		// And the group it names has to be there. A window whose list is older
		// than the store will offer a group another window has since deleted,
		// and a host written into one sits under no group at all — which is
		// where the interface reaches hosts from, so it could not be got at
		// again.
		if h.GroupID != "" && tx.Bucket(bucketGroups).Get([]byte(h.GroupID)) == nil {
			return ErrNoSuchGroup
		}
		if err := noNewJumpLoop(tx.Bucket(bucketGroups), hb, nil, []Host{h}); err != nil {
			return err
		}
		enc, err := json.Marshal(h)
		if err != nil {
			return err
		}
		return hb.Put([]byte(h.ID), enc)
	})
	return h, err
}

// noNewJumpLoop rejects a write that would leave a host jumping through
// itself, by way of the hosts it names or the groups it belongs to.
//
// A jump host is named rather than pointed at by id, and a name that is none
// of yours is left for ssh to interpret — so a chain is followed only while it
// stays among hosts this store holds, and ends the moment it leaves them.
// Inherited ones count: a host naming no jump host of its own takes its
// group's, and a loop made that way connects no better than one written out.
//
// Unchecked, a loop became a ProxyCommand nested until the resolver ran out of
// hops, and what came back was ssh's "Connection closed by UNKNOWN port 65535"
// — its proxy having died, with nothing anywhere saying that two of your own
// hosts point at each other. A host reading "via" the name it is itself called
// looked like every other host in the list.
//
// What is refused is a loop this write would *make*. A database already
// holding one — written before this was checked — stays editable, including
// the host at fault, which is where the loop has to be undone.
func noNewJumpLoop(gb, hb *bolt.Bucket, groups []Group, hosts []Host) error {
	storedGroups, _ := decodeGroups(gb)
	storedHosts, _ := decodeHosts(hb)

	before := loopingHosts(storedGroups, storedHosts)
	after := loopingHosts(mergedGroups(storedGroups, groups), mergedHosts(storedHosts, hosts))
	for _, id := range reportOrder(after, hosts) {
		if _, already := before[id]; !already {
			loop := after[id]
			return fmt.Errorf("host %q would jump through itself: %s", loop[0], strings.Join(loop, " → "))
		}
	}
	return nil
}

// reportOrder decides which host on a loop to complain about.
//
// One the write actually names comes first, because that is the record whose
// jump host was just chosen — the loop runs through two hosts or more, and
// naming the other one sends someone to look at a record they had not touched.
// Anything else follows by id, since a map is walked in no order at all: the
// same mistake came back as either host about half the time, and an error that
// will not say the same thing twice is one nobody can act on.
func reportOrder(loops map[string][]string, incoming []Host) []string {
	named := map[string]bool{}
	out := make([]string, 0, len(loops))
	for _, h := range incoming {
		if _, ok := loops[h.ID]; ok && !named[h.ID] {
			named[h.ID] = true
			out = append(out, h.ID)
		}
	}
	rest := make([]string, 0, len(loops))
	for id := range loops {
		if !named[id] {
			rest = append(rest, id)
		}
	}
	// By name rather than by id: an id is minted at random, so ordering by one
	// settles a single database without settling anything a person could
	// predict, and two machines holding the same hosts would disagree.
	sort.Slice(rest, func(i, j int) bool { return loops[rest[i]][0] < loops[rest[j]][0] })
	return append(out, rest...)
}

// mergedGroups and mergedHosts are what a bucket holds with an incoming set
// written over it, which is what the store will look like after this write.
func mergedGroups(stored, incoming []Group) []Group {
	byID := make(map[string]Group, len(stored)+len(incoming))
	for _, g := range stored {
		byID[g.ID] = g
	}
	for _, g := range incoming {
		byID[g.ID] = g
	}
	out := make([]Group, 0, len(byID))
	for _, g := range byID {
		out = append(out, g)
	}
	return out
}

func mergedHosts(stored, incoming []Host) []Host {
	byID := make(map[string]Host, len(stored)+len(incoming))
	for _, h := range stored {
		byID[h.ID] = h
	}
	for _, h := range incoming {
		byID[h.ID] = h
	}
	out := make([]Host, 0, len(byID))
	for _, h := range byID {
		out = append(out, h)
	}
	return out
}

// loopingHosts is every host whose jump hosts lead back to it, and the path
// each one takes to get there.
func loopingHosts(groups []Group, hosts []Host) map[string][]string {
	r := NewResolver(groups, hosts)
	out := map[string][]string{}
	for _, h := range hosts {
		if loop, ok := jumpChain(r, h); ok {
			out[h.ID] = append([]string{h.Name}, loop...)
		}
	}
	return out
}

// jumpChain follows a host's jump hosts and reports the names it went through
// if it arrives somewhere it has already been.
func jumpChain(r Resolver, start Host) ([]string, bool) {
	seen := map[string]bool{start.ID: true}
	var path []string
	cur := r.inherit(start).Host
	for range maxJumpHops {
		name := strings.TrimSpace(cur.ProxyJump)
		if name == "" {
			return nil, false
		}
		next, ok := r.byName[strings.ToLower(name)]
		if !ok {
			return nil, false // none of ours, so ssh's to make sense of
		}
		path = append(path, next.Name)
		if seen[next.ID] {
			return path, true
		}
		seen[next.ID] = true
		cur = r.inherit(next).Host
	}
	return nil, false
}

// ErrNoSuchHost is why a rule or an edit was refused: the host it is for is
// not there. ErrNoSuchGroup says the same of a group.
var (
	ErrNoSuchHost  = errors.New("that host no longer exists")
	ErrNoSuchGroup = errors.New("that group no longer exists")
)

// PutForward inserts or updates a forwarding rule, assigning an id when
// absent. The id is what the running tunnel is named after, so it is minted
// here and never changes again.
//
// The host is checked in the same transaction as the write. A rule names a
// host by id and means nothing without it — DeleteHost already takes a host's
// rules with it — and checking in a transaction of its own would leave a gap
// for another Omassh to delete the host in between. That gap was reachable:
// one window with a forwards view open on a host, another deleting it, and the
// rule the first then saved belonged to nothing, showing under no host and
// reachable by nothing that could remove it.
func (s *Store) PutForward(f Forward) (Forward, error) {
	if err := f.Validate(); err != nil {
		return f, err
	}
	if f.ID == "" {
		f.ID = NewID()
	}
	enc, err := json.Marshal(f)
	if err != nil {
		return f, err
	}
	return f, s.write(func(tx *bolt.Tx) error {
		if tx.Bucket(bucketHosts).Get([]byte(f.HostID)) == nil {
			return ErrNoSuchHost
		}
		return tx.Bucket(bucketForwards).Put([]byte(f.ID), enc)
	})
}

func (s *Store) DeleteForward(id string) error {
	return s.write(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketForwards).Delete([]byte(id))
	})
}

// PutAll writes groups and hosts together, in one transaction.
//
// Each record written on its own is a transaction of its own, and every
// transaction is a trip to the disk: importing five thousand hosts that way
// took the better part of a minute, saying nothing while it went. One
// transaction is also all-or-nothing, so a disk that fills up halfway leaves
// the store as it was rather than holding part of a list.
func (s *Store) PutAll(gs []Group, hs []Host, fs []Forward) error {
	for i := range gs {
		if gs[i].ID == "" {
			gs[i].ID = NewID()
		}
	}
	for i := range hs {
		if hs[i].ID == "" {
			hs[i].ID = NewID()
		}
	}
	for i := range fs {
		if fs[i].ID == "" {
			fs[i].ID = NewID()
		}
		if err := fs[i].Validate(); err != nil {
			return err
		}
	}

	return s.write(func(tx *bolt.Tx) error {
		gb, hb := tx.Bucket(bucketGroups), tx.Bucket(bucketHosts)
		// Checked once over the whole tree, rather than each group re-reading
		// every other one from a transaction of its own.
		if err := acyclicIn(gb, gs); err != nil {
			return err
		}
		if err := noNewJumpLoop(gb, hb, gs, hs); err != nil {
			return err
		}
		for _, g := range gs {
			b, err := json.Marshal(g)
			if err != nil {
				return err
			}
			if err := gb.Put([]byte(g.ID), b); err != nil {
				return fmt.Errorf("group %s: %w", g.Name, err)
			}
		}
		for _, h := range hs {
			b, err := json.Marshal(h)
			if err != nil {
				return err
			}
			if err := hb.Put([]byte(h.ID), b); err != nil {
				return fmt.Errorf("host %s: %w", h.Name, err)
			}
		}
		// After the hosts, so a rule for a host this same write is bringing
		// finds it. The check is the one PutForward makes, for the same
		// reason: a rule naming a host that is not there belongs to nothing.
		fwb := tx.Bucket(bucketForwards)
		for _, f := range fs {
			if hb.Get([]byte(f.HostID)) == nil {
				return fmt.Errorf("forward %s: %w", f.Label(), ErrNoSuchHost)
			}
			b, err := json.Marshal(f)
			if err != nil {
				return err
			}
			if err := fwb.Put([]byte(f.ID), b); err != nil {
				return fmt.Errorf("forward %s: %w", f.Label(), err)
			}
		}
		return nil
	})
}

// acyclicIn rejects an incoming set that would leave a group as its own
// ancestor, given what the bucket already holds.
func acyclicIn(b *bolt.Bucket, incoming []Group) error {
	byID := map[string]Group{}
	// A record that will not decode is no chain to follow.
	stored, _ := decodeGroups(b)
	for _, g := range stored {
		byID[g.ID] = g
	}
	for _, g := range incoming {
		byID[g.ID] = g
	}

	for _, g := range byID {
		seen := map[string]bool{g.ID: true}
		for id := g.ParentID; id != ""; {
			if seen[id] {
				return fmt.Errorf("group %q would be its own ancestor", g.Name)
			}
			seen[id] = true
			id = byID[id].ParentID
		}
	}
	return nil
}

func (s *Store) put(bucket []byte, id string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return s.write(func(tx *bolt.Tx) error {
		return tx.Bucket(bucket).Put([]byte(id), b)
	})
}

// DeleteHost removes a host and the forwarding rules that belong to it.
//
// A rule names a host by id and means nothing without it, so leaving them
// behind would keep a tunnel in the store that nothing could start, edit or
// see. Stopping one that is running is the interface's job, since the store
// knows nothing about tmux.
func (s *Store) DeleteHost(id string) error {
	return s.write(func(tx *bolt.Tx) error {
		fb := tx.Bucket(bucketForwards)
		forwards, _ := decodeForwards(fb)
		for _, f := range forwards {
			if f.HostID != id {
				continue
			}
			if err := fb.Delete([]byte(f.ID)); err != nil {
				return err
			}
		}
		return tx.Bucket(bucketHosts).Delete([]byte(id))
	})
}

// DeleteGroup removes a group and re-parents its child groups and hosts to
// the deleted group's parent. Hosts are never deleted as a side effect of
// deleting a group.
func (s *Store) DeleteGroup(id string) error {
	// One transaction for the whole thing: what is re-parented is decided from
	// the same state it is written into, which reading first and writing after
	// could not promise once another Omassh may be writing too.
	return s.write(func(tx *bolt.Tx) error {
		gb, hb := tx.Bucket(bucketGroups), tx.Bucket(bucketHosts)
		groups, _ := decodeGroups(gb)
		hosts, _ := decodeHosts(hb)

		var parent string
		for _, g := range groups {
			if g.ID == id {
				parent = g.ParentID
			}
		}
		for _, g := range groups {
			if g.ParentID != id {
				continue
			}
			g.ParentID = parent
			b, err := json.Marshal(g)
			if err != nil {
				return err
			}
			if err := gb.Put([]byte(g.ID), b); err != nil {
				return err
			}
		}
		for _, h := range hosts {
			if h.GroupID != id {
				continue
			}
			h.GroupID = parent
			b, err := json.Marshal(h)
			if err != nil {
				return err
			}
			if err := hb.Put([]byte(h.ID), b); err != nil {
				return err
			}
		}
		return gb.Delete([]byte(id))
	})
}

// Counts reports how many groups and hosts would be re-parented by deleting
// the given group, so the confirmation prompt can say so.
func (s *Store) Counts(groupID string) (groups, hosts int) {
	s.read(func(tx *bolt.Tx) error {
		gs, _ := decodeGroups(tx.Bucket(bucketGroups))
		hs, _ := decodeHosts(tx.Bucket(bucketHosts))
		for _, g := range gs {
			if g.ParentID == groupID {
				groups++
			}
		}
		for _, h := range hs {
			if h.GroupID == groupID {
				hosts++
			}
		}
		return nil
	})
	return groups, hosts
}

func (s *Store) Stats() (map[string]Stat, error) {
	out := map[string]Stat{}
	err := s.read(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketStats).ForEach(func(k, v []byte) error {
			var st Stat
			if err := json.Unmarshal(v, &st); err != nil {
				return nil // history for one host is not worth losing the rest
			}
			out[string(k)] = st
			return nil
		})
	})
	return out, err
}

// RecordSession bumps the session counter and last-seen time for a host key.
func (s *Store) RecordSession(key string, at time.Time) error {
	if key == "" {
		return nil
	}
	return s.write(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketStats)
		var st Stat
		if raw := b.Get([]byte(key)); raw != nil {
			if err := json.Unmarshal(raw, &st); err != nil {
				return err
			}
		}
		st.Count++
		st.LastSeen = at
		enc, err := json.Marshal(st)
		if err != nil {
			return err
		}
		return b.Put([]byte(key), enc)
	})
}

// NewID mints a record id. Import needs one before it writes, so that a
// host can name the group it is joining while both are still only planned.
func NewID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand does not fail in practice; a time-based fallback keeps
		// the store usable rather than taking the app down over an id.
		return fmt.Sprintf("t%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

func sortGroups(gs []Group) {
	sort.Slice(gs, func(i, j int) bool {
		return strings.ToLower(gs[i].Name) < strings.ToLower(gs[j].Name)
	})
}

// SortHosts orders hosts case-insensitively by name.
func SortHosts(hs []Host) {
	sort.Slice(hs, func(i, j int) bool {
		return strings.ToLower(hs[i].Name) < strings.ToLower(hs[j].Name)
	})
}
