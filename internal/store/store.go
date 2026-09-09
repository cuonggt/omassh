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
type Store struct {
	db *bolt.DB
}

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

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: 3 * time.Second})
	if err != nil {
		// bbolt takes an exclusive lock, so a second instance fails with a
		// bare "timeout" that says nothing about the cause. Name it: the
		// usual reason is another Omassh already running.
		if errors.Is(err, bolt.ErrTimeout) {
			return nil, fmt.Errorf("%s is already in use by another omassh — close it, or pass -db to use a different database", path)
		}
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	err = db.Update(func(tx *bolt.Tx) error {
		for _, b := range [][]byte{bucketGroups, bucketHosts, bucketStats} {
			if _, err := tx.CreateBucketIfNotExists(b); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Groups() ([]Group, error) {
	var out []Group
	var bad []string
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketGroups).ForEach(func(k, v []byte) error {
			var g Group
			if err := json.Unmarshal(v, &g); err != nil {
				bad = append(bad, string(k))
				return nil
			}
			out = append(out, g)
			return nil
		})
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
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketHosts).ForEach(func(k, v []byte) error {
			var h Host
			if err := json.Unmarshal(v, &h); err != nil {
				bad = append(bad, string(k))
				return nil
			}
			out = append(out, h)
			return nil
		})
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
	// A group may not be its own ancestor, or the resolver and the tree walk
	// would both need to defend against it at every read.
	if err := s.checkAcyclic(g); err != nil {
		return g, err
	}
	return g, s.put(bucketGroups, g.ID, g)
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

	return s.db.Update(func(tx *bolt.Tx) error {
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
	err := b.ForEach(func(_, v []byte) error {
		var g Group
		if err := json.Unmarshal(v, &g); err == nil {
			byID[g.ID] = g
		}
		return nil // a record that will not decode is no chain to follow
	})
	if err != nil {
		return err
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
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucket).Put([]byte(id), b)
	})
}

func (s *Store) DeleteHost(id string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketHosts).Delete([]byte(id))
	})
}

// DeleteGroup removes a group and re-parents its child groups and hosts to
// the deleted group's parent. Hosts are never deleted as a side effect of
// deleting a group.
func (s *Store) DeleteGroup(id string) error {
	groups, err := s.Groups()
	if err != nil {
		return err
	}
	var parent string
	for _, g := range groups {
		if g.ID == id {
			parent = g.ParentID
		}
	}

	hosts, err := s.Hosts()
	if err != nil {
		return err
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		gb, hb := tx.Bucket(bucketGroups), tx.Bucket(bucketHosts)
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
	gs, _ := s.Groups()
	hs, _ := s.Hosts()
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
	return groups, hosts
}

func (s *Store) Stats() (map[string]Stat, error) {
	out := map[string]Stat{}
	err := s.db.View(func(tx *bolt.Tx) error {
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
	return s.db.Update(func(tx *bolt.Tx) error {
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

// checkAcyclic rejects a group whose parent chain would loop back to itself.
func (s *Store) checkAcyclic(g Group) error {
	if g.ParentID == "" {
		return nil
	}
	if g.ParentID == g.ID {
		return fmt.Errorf("a group cannot be its own parent")
	}
	groups, err := s.Groups()
	if err != nil {
		return err
	}
	byID := make(map[string]Group, len(groups))
	for _, x := range groups {
		byID[x.ID] = x
	}
	seen := map[string]bool{g.ID: true}
	for id := g.ParentID; id != ""; {
		if seen[id] {
			return fmt.Errorf("that parent would create a cycle")
		}
		seen[id] = true
		id = byID[id].ParentID
	}
	return nil
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
