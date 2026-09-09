// Package portable moves a host list between machines as text.
//
// The store is bbolt: one binary file that can be copied but not diffed,
// reviewed or merged, and whose ids are minted per machine — so two machines
// that each gained a host cannot be reconciled by copying it, in either
// direction. This package is the text form instead: a YAML document a
// dotfiles repo can hold and a person can read, plus the merge that folds one
// back into a store without disturbing what is already there.
//
// Records match by name, never by id. An id is random and local, so the same
// host added on a laptop and on a desktop has two of them and nothing could
// pair them up; the name is the only identity the two machines share. The
// interface already works this way — typing a group name in a host form finds
// the group by name — so this follows it rather than inventing a second rule.
package portable

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/cuonggt/omassh/internal/store"
)

// Version is the document format. It is written on export and checked on
// import, so a document from a later omassh is refused with something better
// than a field that quietly did nothing.
const Version = 1

// Document is the whole exchange format.
type Document struct {
	Version int     `yaml:"version"`
	Groups  []Group `yaml:"groups,omitempty"`
	Hosts   []Host  `yaml:"hosts,omitempty"`
}

// Group is a group as text. Parent names another group rather than pointing
// at an id, for the same reason everything else here is by name.
type Group struct {
	Name     string `yaml:"name"`
	Parent   string `yaml:"parent,omitempty"`
	User     string `yaml:"user,omitempty"`
	Identity string `yaml:"identity,omitempty"`
	Jump     string `yaml:"jump,omitempty"`
}

// Host is a host as text. The field is Jump rather than ProxyJump because
// that is what the form and the detail pane call it.
type Host struct {
	Name     string   `yaml:"name"`
	Addr     string   `yaml:"addr"`
	Port     int      `yaml:"port,omitempty"`
	User     string   `yaml:"user,omitempty"`
	Identity string   `yaml:"identity,omitempty"`
	Jump     string   `yaml:"jump,omitempty"`
	Group    string   `yaml:"group,omitempty"`
	Tags     []string `yaml:"tags,omitempty,flow"`
}

// Export renders a store's contents as a document, ids left behind.
//
// Session history is deliberately absent. "Last connected two hours ago" is a
// fact about the machine that connected, and carrying it across would let a
// laptop's history overwrite a desktop's on every import.
func Export(gs []store.Group, hs []store.Host) Document {
	name := make(map[string]string, len(gs))
	for _, g := range gs {
		name[g.ID] = g.Name
	}

	d := Document{Version: Version}
	for _, g := range gs {
		d.Groups = append(d.Groups, Group{
			Name:     g.Name,
			Parent:   name[g.ParentID],
			User:     g.User,
			Identity: g.Identity,
			Jump:     g.ProxyJump,
		})
	}
	for _, h := range hs {
		d.Hosts = append(d.Hosts, Host{
			Name:     h.Name,
			Addr:     h.Addr,
			Port:     h.Port,
			User:     h.User,
			Identity: h.Identity,
			Jump:     h.ProxyJump,
			Group:    name[h.GroupID],
			Tags:     h.Tags,
		})
	}
	// Sorted so re-exporting an unchanged store produces an identical file,
	// which is what makes the document reviewable in a diff.
	sort.Slice(d.Groups, func(i, j int) bool { return less(d.Groups[i].Name, d.Groups[j].Name) })
	sort.Slice(d.Hosts, func(i, j int) bool { return less(d.Hosts[i].Name, d.Hosts[j].Name) })
	return d
}

func less(a, b string) bool { return strings.ToLower(a) < strings.ToLower(b) }

// YAML renders the document with a header saying what it is, since the file
// will be found later by someone who did not create it.
func (d Document) YAML() ([]byte, error) {
	var body bytes.Buffer
	enc := yaml.NewEncoder(&body)
	enc.SetIndent(2)
	if err := enc.Encode(d); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	const header = "# omassh host list. Fold it into another machine with:\n" +
		"#   omassh import <this file>\n" +
		"# Records match by name, so ids differing between machines does not matter.\n" +
		"# No secrets here: identity is a path to a key, never the key.\n"
	return append([]byte(header), body.Bytes()...), nil
}

// Parse reads a document, rejecting one this version cannot honour.
func Parse(raw []byte) (Document, error) {
	var d Document
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	// A key that is not a field is an error, for the reason config.Load
	// gives: skipping it looks exactly like the file not being read at all.
	// Here it is worse, because the import reports the same tidy "2 added"
	// either way — jump_host where the field is jump leaves the host with no
	// proxy and nothing on screen to say a line was ignored.
	dec.KnownFields(true)

	// A document with nothing in it — empty, or only comments — decodes to
	// EOF. The caller has a better complaint about that than anything this
	// could say about YAML, so leave it to say it.
	if err := dec.Decode(&d); err != nil && !errors.Is(err, io.EOF) {
		return Document{}, err
	}
	if d.Version > Version {
		return d, fmt.Errorf("document is version %d, this omassh understands %d — upgrade omassh", d.Version, Version)
	}
	return d, nil
}
