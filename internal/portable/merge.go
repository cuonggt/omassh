package portable

import (
	"fmt"
	"slices"
	"strings"

	"github.com/cuonggt/omassh/internal/store"
)

// Action is what an import does to one record.
type Action string

const (
	Add    Action = "add"
	Update Action = "update"
)

// Change describes one record an import would write.
type Change struct {
	Kind   string // "group" or "host"
	Name   string
	Action Action
}

func (c Change) String() string {
	return fmt.Sprintf("%-6s %-5s %s", c.Action, c.Kind, c.Name)
}

// Plan is everything an import would do: the records to write, with ids
// already assigned, and a line for each. Working it out separately from
// applying it is what makes a dry run exactly the real thing rather than a
// second implementation that predicts it.
type Plan struct {
	Groups    []store.Group
	Hosts     []store.Host
	Changes   []Change
	Unchanged int
}

func (p Plan) Empty() bool { return len(p.Changes) == 0 }

// Counts summarises the plan for a one-line report.
func (p Plan) Counts() (added, updated int) {
	for _, c := range p.Changes {
		if c.Action == Add {
			added++
		} else {
			updated++
		}
	}
	return added, updated
}

// Merge folds a document into what a store already holds.
//
// A record already present keeps its id, so importing does not orphan the
// session history hanging off it — the host you have connected to forty times
// is still that host afterwards.
//
// A field the document leaves empty keeps the value already in the store.
// Import fills in and corrects; it does not blank things. That is what lets
// ssh_config be imported twice without the second pass wiping a user or a tag
// added here in between, and the cost is that a field cannot be cleared by
// importing — which the interface does instead.
func Merge(d Document, groups []store.Group, hosts []store.Host) (Plan, error) {
	if err := d.validate(); err != nil {
		return Plan{}, err
	}

	byName := make(map[string]store.Group, len(groups))
	for _, g := range groups {
		byName[key(g.Name)] = g
	}
	var p Plan

	// accounted is which groups the report has already spoken for, so a group
	// declared once and named by ten hosts is counted once.
	accounted := map[string]bool{}

	// Every group the document names gets an id up front, so a parent still
	// resolves when the document lists the child first.
	fresh := map[string]bool{}
	ensure := func(name string) store.Group {
		k := key(name)
		if g, ok := byName[k]; ok {
			return g
		}
		g := store.Group{ID: store.NewID(), Name: name}
		byName[k], fresh[k] = g, true
		return g
	}
	for _, g := range d.Groups {
		ensure(g.Name)
		accounted[key(g.Name)] = true
	}

	for _, in := range d.Groups {
		k := key(in.Name)
		was := byName[k]
		now := was
		now.User = pick(in.User, was.User)
		now.Identity = pick(in.Identity, was.Identity)
		now.ProxyJump = pick(in.Jump, was.ProxyJump)
		if in.Parent != "" {
			parent, ok := byName[key(in.Parent)]
			if !ok {
				return Plan{}, fmt.Errorf("group %q names parent %q, which is neither in this document nor already here", in.Name, in.Parent)
			}
			now.ParentID = parent.ID
		}
		byName[k] = now
		if fresh[k] {
			p.addGroup(now, Add)
		} else if now != was {
			p.addGroup(now, Update)
		} else {
			p.Unchanged++
		}
	}

	seen := make(map[string]store.Host, len(hosts))
	for _, h := range hosts {
		seen[key(h.Name)] = h
	}

	for _, in := range d.Hosts {
		k := key(in.Name)
		was, exists := seen[k]
		now := was
		now.Name, now.Addr = in.Name, pick(in.Addr, was.Addr)
		now.Port = pickInt(in.Port, was.Port)
		now.User = pick(in.User, was.User)
		now.Identity = pick(in.Identity, was.Identity)
		now.ProxyJump = pick(in.Jump, was.ProxyJump)
		if len(in.Tags) > 0 {
			now.Tags = slices.Clone(in.Tags)
		}
		// A host naming a group nothing has heard of creates it, exactly as
		// typing an unknown group into the host form does.
		if in.Group != "" {
			g := ensure(in.Group)
			now.GroupID = g.ID
			if k := key(in.Group); !accounted[k] {
				accounted[k] = true
				if fresh[k] {
					p.addGroup(g, Add)
				} else {
					p.Unchanged++
				}
			}
		}
		switch {
		case !exists:
			now.ID = store.NewID()
			p.addHost(now, Add)
		case !sameHost(now, was):
			p.addHost(now, Update)
		default:
			p.Unchanged++
		}
		seen[k] = now
	}

	// PutGroup checks for cycles against the store, which cannot see groups
	// this plan has not written yet, so the document's own tree is checked
	// here instead of failing halfway through applying it.
	if err := acyclic(byName); err != nil {
		return Plan{}, err
	}
	return p, nil
}

// addGroup and addHost queue a record and the line that reports it.
func (p *Plan) addGroup(g store.Group, a Action) {
	p.Groups = append(p.Groups, g)
	p.Changes = append(p.Changes, Change{Kind: "group", Name: g.Name, Action: a})
}

func (p *Plan) addHost(h store.Host, a Action) {
	p.Hosts = append(p.Hosts, h)
	p.Changes = append(p.Changes, Change{Kind: "host", Name: h.Name, Action: a})
}

// acyclic rejects a group tree that loops, naming a group on the cycle.
func acyclic(byName map[string]store.Group) error {
	byID := make(map[string]store.Group, len(byName))
	for _, g := range byName {
		byID[g.ID] = g
	}
	for _, g := range byName {
		seen := map[string]bool{g.ID: true}
		for id := g.ParentID; id != ""; {
			if seen[id] {
				return fmt.Errorf("group %q is its own ancestor", g.Name)
			}
			seen[id] = true
			id = byID[id].ParentID
		}
	}
	return nil
}

// validate reports what would make a document unusable before any of it is
// applied, so an import is all or nothing rather than half done.
func (d Document) validate() error {
	if d.Version > Version {
		return fmt.Errorf("document is version %d, this omassh understands %d — upgrade omassh", d.Version, Version)
	}
	groups := map[string]bool{}
	for _, g := range d.Groups {
		if strings.TrimSpace(g.Name) == "" {
			return fmt.Errorf("a group has no name")
		}
		if groups[key(g.Name)] {
			return fmt.Errorf("group %q appears twice; names are how records are matched, so they have to be unique", g.Name)
		}
		groups[key(g.Name)] = true
	}
	hosts := map[string]bool{}
	for _, h := range d.Hosts {
		if strings.TrimSpace(h.Name) == "" {
			return fmt.Errorf("a host has no name")
		}
		if strings.TrimSpace(h.Addr) == "" {
			return fmt.Errorf("host %q has no address", h.Name)
		}
		if hosts[key(h.Name)] {
			return fmt.Errorf("host %q appears twice; names are how records are matched, so they have to be unique", h.Name)
		}
		hosts[key(h.Name)] = true
	}
	return nil
}

// key is how a name is matched: case-insensitively, as the group picker and
// the jump-host resolver already do.
func key(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

func pick(doc, existing string) string {
	if doc != "" {
		return doc
	}
	return existing
}

func pickInt(doc, existing int) int {
	if doc != 0 {
		return doc
	}
	return existing
}

// sameHost compares everything import writes. Host holds a slice, so it is
// not comparable, and Jump is resolved at connect time and never stored.
func sameHost(a, b store.Host) bool {
	a.Jump, b.Jump = nil, nil
	return a.ID == b.ID && a.Name == b.Name && a.Addr == b.Addr && a.Port == b.Port &&
		a.User == b.User && a.Identity == b.Identity && a.ProxyJump == b.ProxyJump &&
		a.GroupID == b.GroupID && slices.Equal(a.Tags, b.Tags)
}
