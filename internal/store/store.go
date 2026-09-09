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
		for _, b := range [][]byte{bucketGroups, bucketHosts, bucketStats} {
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
	if g.ID == "" {
		g.ID = NewID()
	}
	// Checked and written in one transaction: a group may not be its own
	// ancestor, or the resolver and the tree walk would both need to defend
	// against it at every read, and checking in a transaction of its own would
	// leave a gap for another Omassh to change the tree in between.
	err := s.write(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketGroups)
		if err := acyclicIn(b, []Group{g}); err != nil {
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
	if h.ID == "" {
		h.ID = NewID()
	}
	return h, s.put(bucketHosts, h.ID, h)
}

// PutAll writes groups and hosts together, in one transaction.
//
// Each record written on its own is a transaction of its own, and every
// transaction is a trip to the disk: importing five thousand hosts that way
// took the better part of a minute, saying nothing while it went. One
// transaction is also all-or-nothing, so a disk that fills up halfway leaves
// the store as it was rather than holding part of a list.
func (s *Store) PutAll(gs []Group, hs []Host) error {
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

	return s.write(func(tx *bolt.Tx) error {
		gb, hb := tx.Bucket(bucketGroups), tx.Bucket(bucketHosts)
		// Checked once over the whole tree, rather than each group re-reading
		// every other one from a transaction of its own.
		if err := acyclicIn(gb, gs); err != nil {
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

func (s *Store) DeleteHost(id string) error {
	return s.write(func(tx *bolt.Tx) error {
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
