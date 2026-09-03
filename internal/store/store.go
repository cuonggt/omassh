package store

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
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
	bucketIdents = []byte("identities")
	bucketFwds   = []byte("forwards")
	bucketSnips  = []byte("snippets")
)

// Store is the on-disk database of locally-defined hosts and groups, plus
// session history for every host Omassh has connected to.
type Store struct {
	db *bolt.DB
}

// DefaultPath returns ~/.config/omassh/omassh.db.
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
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	err = db.Update(func(tx *bolt.Tx) error {
		for _, b := range [][]byte{bucketGroups, bucketHosts, bucketStats, bucketIdents, bucketFwds, bucketSnips} {
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
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketGroups).ForEach(func(_, v []byte) error {
			var g Group
			if err := json.Unmarshal(v, &g); err != nil {
				return err
			}
			out = append(out, g)
			return nil
		})
	})
	sortGroups(out)
	return out, err
}

func (s *Store) Hosts() ([]Host, error) {
	var out []Host
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketHosts).ForEach(func(_, v []byte) error {
			var h Host
			if err := json.Unmarshal(v, &h); err != nil {
				return err
			}
			out = append(out, h)
			return nil
		})
	})
	SortHosts(out)
	return out, err
}

// PutGroup inserts or updates a group, assigning an id when absent.
func (s *Store) PutGroup(g Group) (Group, error) {
	if g.ID == "" {
		g.ID = newID()
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
		h.ID = newID()
	}
	return h, s.put(bucketHosts, h.ID, h)
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

// DeleteHost removes a host and the forwarding rules that belong to it, which
// would otherwise linger with no way to reach them.
func (s *Store) DeleteHost(id string) error {
	fwds, err := s.Forwards()
	if err != nil {
		return err
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		for _, f := range fwds {
			if f.HostKey == id {
				if err := tx.Bucket(bucketFwds).Delete([]byte(f.ID)); err != nil {
					return err
				}
			}
		}
		return tx.Bucket(bucketHosts).Delete([]byte(id))
	})
}

func (s *Store) Forwards() ([]Forward, error) {
	var out []Forward
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketFwds).ForEach(func(_, v []byte) error {
			var f Forward
			if err := json.Unmarshal(v, &f); err != nil {
				return err
			}
			out = append(out, f)
			return nil
		})
	})
	sort.Slice(out, func(i, j int) bool {
		if out[i].HostKey != out[j].HostKey {
			return out[i].HostKey < out[j].HostKey
		}
		return out[i].ListenPort < out[j].ListenPort
	})
	return out, err
}

// ForwardsFor returns the rules attached to one host key.
func (s *Store) ForwardsFor(hostKey string) ([]Forward, error) {
	all, err := s.Forwards()
	if err != nil {
		return nil, err
	}
	var out []Forward
	for _, f := range all {
		if f.HostKey == hostKey {
			out = append(out, f)
		}
	}
	return out, nil
}

func (s *Store) PutForward(f Forward) (Forward, error) {
	if err := f.Validate(); err != nil {
		return f, err
	}
	if f.ID == "" {
		f.ID = newID()
	}
	return f, s.put(bucketFwds, f.ID, f)
}

func (s *Store) DeleteForward(id string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketFwds).Delete([]byte(id))
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

func (s *Store) Identities() ([]Identity, error) {
	var out []Identity
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketIdents).ForEach(func(_, v []byte) error {
			var i Identity
			if err := json.Unmarshal(v, &i); err != nil {
				return err
			}
			out = append(out, i)
			return nil
		})
	})
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, err
}

func (s *Store) PutIdentity(i Identity) (Identity, error) {
	if i.ID == "" {
		i.ID = newID()
	}
	return i, s.put(bucketIdents, i.ID, i)
}

// DeleteIdentity removes a credential and unbinds it from every host and group
// that referenced it, so nothing is left pointing at an id that no longer
// resolves. The secret itself is the caller's to remove from the vault.
func (s *Store) DeleteIdentity(id string) error {
	hosts, err := s.Hosts()
	if err != nil {
		return err
	}
	groups, err := s.Groups()
	if err != nil {
		return err
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		hb, gb := tx.Bucket(bucketHosts), tx.Bucket(bucketGroups)
		for _, h := range hosts {
			if h.IdentityID != id {
				continue
			}
			h.IdentityID = ""
			b, err := json.Marshal(h)
			if err != nil {
				return err
			}
			if err := hb.Put([]byte(h.ID), b); err != nil {
				return err
			}
		}
		for _, g := range groups {
			if g.IdentityID != id {
				continue
			}
			g.IdentityID = ""
			b, err := json.Marshal(g)
			if err != nil {
				return err
			}
			if err := gb.Put([]byte(g.ID), b); err != nil {
				return err
			}
		}
		return tx.Bucket(bucketIdents).Delete([]byte(id))
	})
}

// IdentityUsage counts the hosts and groups bound to a credential.
func (s *Store) IdentityUsage(id string) (hosts, groups int) {
	hs, _ := s.Hosts()
	gs, _ := s.Groups()
	for _, h := range hs {
		if h.IdentityID == id {
			hosts++
		}
	}
	for _, g := range gs {
		if g.IdentityID == id {
			groups++
		}
	}
	return hosts, groups
}

func (s *Store) Snippets() ([]Snippet, error) {
	var out []Snippet
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketSnips).ForEach(func(_, v []byte) error {
			var sn Snippet
			if err := json.Unmarshal(v, &sn); err != nil {
				return err
			}
			out = append(out, sn)
			return nil
		})
	})
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, err
}

func (s *Store) PutSnippet(sn Snippet) (Snippet, error) {
	if strings.TrimSpace(sn.Name) == "" {
		return sn, fmt.Errorf("a snippet needs a name")
	}
	if strings.TrimSpace(sn.Command) == "" {
		return sn, fmt.Errorf("a snippet needs a command")
	}
	if sn.ID == "" {
		sn.ID = newID()
	}
	return sn, s.put(bucketSnips, sn.ID, sn)
}

func (s *Store) DeleteSnippet(id string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketSnips).Delete([]byte(id))
	})
}

func (s *Store) Stats() (map[string]Stat, error) {
	out := map[string]Stat{}
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketStats).ForEach(func(k, v []byte) error {
			var st Stat
			if err := json.Unmarshal(v, &st); err != nil {
				return err
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

func newID() string {
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
