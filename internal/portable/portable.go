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

// Version is the highest document format this omassh understands. It is
// checked on import, so a document from a later omassh is refused with
// something better than a field that quietly did nothing.
//
// 1 is the original. 2 added a host's forwarding rules, and an omassh that
// only knows 1 rejects an unknown field outright — with the name of a Go type
// and no hint that upgrading is the answer, which is exactly what this number
// is for.
const Version = 2

// versionFor is the lowest version that can read a document.
//
// A document says what it needs rather than what wrote it, so a list with no
// forwarding rules in it goes on being readable by an older omassh. Only one
// that would genuinely be misread announces the newer format.
func versionFor(d Document) int {
	for _, h := range d.Hosts {
		if len(h.Forwards) > 0 {
			return 2
		}
	}
	return 1
}

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

	// Forwards are nested rather than listed apart, because a rule belongs to
	// exactly one host and has no name of its own — nesting is what says
	// which host it is for, without inventing a second way to point at one.
	Forwards []Forward `yaml:"forwards,omitempty"`
}

// Forward is a port-forwarding rule as text, written the way ssh writes it
// and the way the form takes it: a kind, the end that is bound, and the end
// it comes out at.
//
// A rule travels because it says how you work with a host rather than
// anything about this machine — "the database is on 5432 through there" is as
// true on a laptop as on a desktop. Session history is the opposite, and stays
// behind for that reason.
type Forward struct {
	Kind   string `yaml:"kind"`
	Listen string `yaml:"listen"`
	Dest   string `yaml:"dest,omitempty"`
}

// parse turns the text form into a rule, which is also how the document is
// checked: the same two functions the form uses, so a file and a form cannot
// disagree about what "5432" or "[::1]:8080" means.
func (f Forward) parse(hostID string) (store.Forward, error) {
	out := store.Forward{HostID: hostID, Kind: store.ForwardKind(strings.ToLower(strings.TrimSpace(f.Kind)))}
	var err error
	if out.Listen, out.ListenPort, err = store.ParseListen(f.Listen); err != nil {
		return out, err
	}
	if out.Kind != store.ForwardDynamic {
		if out.Dest, out.DestPort, err = store.ParseDest(f.Dest); err != nil {
			return out, err
		}
	}
	return out, out.Validate()
}

// asText is a stored rule written back out.
func asText(f store.Forward) Forward {
	t := Forward{Kind: string(f.Kind), Listen: f.ListenText()}
	if f.Kind != store.ForwardDynamic {
		t.Dest = f.DestText()
	}
	return t
}

// Export renders a store's contents as a document, ids left behind.
//
// Session history is deliberately absent. "Last connected two hours ago" is a
// fact about the machine that connected, and carrying it across would let a
// laptop's history overwrite a desktop's on every import.
func Export(gs []store.Group, hs []store.Host, fs []store.Forward) Document {
	name := make(map[string]string, len(gs))
	for _, g := range gs {
		name[g.ID] = g.Name
	}
	rules := map[string][]store.Forward{}
	for _, f := range fs {
		rules[f.HostID] = append(rules[f.HostID], f)
	}

	var d Document
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
		out := Host{
			Name:     h.Name,
			Addr:     h.Addr,
			Port:     h.Port,
			User:     h.User,
			Identity: h.Identity,
			Jump:     h.ProxyJump,
			Group:    name[h.GroupID],
			Tags:     h.Tags,
		}
		mine := rules[h.ID]
		store.SortForwards(mine)
		for _, f := range mine {
			out.Forwards = append(out.Forwards, asText(f))
		}
		d.Hosts = append(d.Hosts, out)
	}
	// Sorted so re-exporting an unchanged store produces an identical file,
	// which is what makes the document reviewable in a diff.
	sort.Slice(d.Groups, func(i, j int) bool { return less(d.Groups[i].Name, d.Groups[j].Name) })
	sort.Slice(d.Hosts, func(i, j int) bool { return less(d.Hosts[i].Name, d.Hosts[j].Name) })
	d.Version = versionFor(d)
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
	// The version comes first, and loosely. A document from a later omassh
	// carries fields this one has never heard of, and the strict decode below
	// rejects an unknown field before anything looks at the version — so the
	// number meant to say "upgrade omassh" was answered with the name of a Go
	// type and a line number instead.
	var probe struct {
		Version int `yaml:"version"`
	}
	if err := yaml.Unmarshal(raw, &probe); err == nil && probe.Version > Version {
		return Document{}, fmt.Errorf("document is version %d, this omassh understands %d — upgrade omassh", probe.Version, Version)
	}

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
	// Decode reads one document, and a stream can hold several. Importing the
	// first and skipping the rest is the same silent half-success as dropping
	// a key, so refuse the file rather than import part of it. A document may
	// still open with the marker; only a second --- makes a second document.
	if err := dec.Decode(new(Document)); !errors.Is(err, io.EOF) {
		return Document{}, errors.New("this is more than one YAML document — a host list is a single document, and anything after the --- would be skipped")
	}
	if d.Version > Version {
		return d, fmt.Errorf("document is version %d, this omassh understands %d — upgrade omassh", d.Version, Version)
	}
	return d, nil
}
