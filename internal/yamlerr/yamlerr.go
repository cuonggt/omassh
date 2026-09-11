// Package yamlerr says what yaml complained about, in omassh's words.
package yamlerr

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Vocabulary is what a caller knows that this package does not: what its own
// keys and types are called in the file people write.
type Vocabulary struct {
	// Field says what an unknown key is, given the key and the Go type it was
	// offered to. Returning "" leaves yaml's own words.
	Field func(name, typ string) string
	// Type puts a Go type into the words the file uses — "a host", "a
	// palette" — or returns "" where there is no such word, in which case the
	// whole complaint is left as yaml made it.
	Type func(typ string) string
}

// InWords rewrites each complaint in a yaml type error, or hands back what it
// was given.
//
// yaml names Go types and YAML tags: "field jump_host not found in type
// portable.Host", "cannot unmarshal !!str into []portable.Host". Both are
// addressed to whoever is editing a file by hand, and neither tells them
// anything they can act on. The line numbers are yaml's and worth keeping.
//
// Anything the vocabulary has no word for is left exactly as yaml wrote it: a
// shape yaml grows later would otherwise be described from whichever half of
// the sentence was understood, and a confident wrong answer is worse than
// something merely technical.
//
// It is one package because it was written for the config file, and the file
// people move their hosts in had the same problem and none of the fix.
func InWords(err error, v Vocabulary) error {
	var te *yaml.TypeError
	if !errors.As(err, &te) {
		return unreadable(err)
	}
	out := make([]string, 0, len(te.Errors))
	for _, e := range te.Errors {
		out = append(out, rewrite(e, v))
	}
	return errors.New(strings.Join(out, "; "))
}

func rewrite(e string, v Vocabulary) string {
	if out, ok := rewriteField(e, v); ok {
		return out
	}
	if out, ok := rewriteShape(e, v); ok {
		return out
	}
	return e
}

// fieldRE matches yaml's complaint about a key a struct does not have.
var fieldRE = regexp.MustCompile(`^(line \d+: )?field (\S+) not found in type (\S+)$`)

func rewriteField(e string, v Vocabulary) (string, bool) {
	m := fieldRE.FindStringSubmatch(e)
	if m == nil || v.Field == nil {
		return "", false
	}
	said := v.Field(m[2], m[3])
	if said == "" {
		return "", false
	}
	return m[1] + said, true
}

// shapeRE matches yaml's complaint about a value of the wrong shape.
//
// The offending value is in there too, backquoted and sometimes cut short to
// "Connect...", which reads like damage rather than like quoting. The line
// number says where to look, so the value is dropped.
var shapeRE = regexp.MustCompile("^(line \\d+: )?cannot unmarshal (!!\\w+)(?: `.*`)? into (.+)$")

// found names what yaml met. These are YAML's own tags rather than anything a
// caller knows, so they live here.
//
// No entry for !!null: an empty value decodes to the zero value at every
// position, so it is never the wrong shape for anything.
var found = map[string]string{
	"!!str":   "text",
	"!!int":   "a number",
	"!!float": "a number",
	"!!bool":  "true or false",
	"!!seq":   "a list",
	"!!map":   "a block of key: value lines",
}

func rewriteShape(e string, v Vocabulary) (string, bool) {
	m := shapeRE.FindStringSubmatch(e)
	if m == nil || v.Type == nil {
		return "", false
	}
	got, ok := found[m[2]]
	if !ok {
		return "", false
	}
	want := v.Type(m[3])
	if want == "" {
		return "", false
	}
	return fmt.Sprintf("%sthis should be %s, not %s", m[1], want, got), true
}

// A file that is not YAML at all never reached any of the above: yaml reports
// that from its scanner, as a plain error rather than a *yaml.TypeError, so it
// was handed straight to the person editing the file with yaml's own prefix
// still on it — "yaml: line 2: mapping values are not allowed in this context".
// Which names the library, then a token, then a context, and says nothing
// about what to change. The type errors beside it had already been put into
// words; these are what people hit first, because a file that will not parse
// never gets as far as having the wrong type in it.

// prefix is what yaml puts in front of everything it says.
const prefix = "yaml: "

// lineRE separates the line yaml is pointing at from the problem it found.
var lineRE = regexp.MustCompile(`^(line \d+: )(.*)$`)

// unreadable says what a file that will not parse got wrong.
//
// The wording is careful about *where*, because yaml's line is not always the
// line at fault: for a construct left open or an item that does not line up it
// points at where that construct began, which can be several lines above the
// mistake. So each sentence below is phrased for the line yaml actually names
// — "the list starting here", not "this line" — and the ones it cannot be
// sure of say nothing about position at all.
//
// Anything not listed keeps yaml's own words after a plain opening, since a
// technical hint is better than none and inventing a cause is worse than
// either.
func unreadable(err error) error {
	msg, ok := strings.CutPrefix(err.Error(), prefix)
	if !ok {
		return err
	}
	where := ""
	if m := lineRE.FindStringSubmatch(msg); m != nil {
		where, msg = m[1], m[2]
	}
	if said, ok := plainly[msg]; ok {
		return errors.New(where + said)
	}
	return errors.New(where + "not readable as YAML — " + msg)
}

var plainly = map[string]string{
	// Pointing at where the list or block began. The line that does not line
	// up with it is somewhere below.
	"did not find expected '-' indicator": "an item of the list starting here does not line up with the rest",
	"did not find expected key":           "something in the block starting here is not a key: value line — check the indentation",

	// Pointing at the line itself, but two ordinary mistakes end up here: a
	// line indented under a value rather than beside it, and a value with a
	// colon in it that was never quoted.
	"mapping values are not allowed in this context": "a key: value line cannot go here — check the indentation, and quote any value with a colon in it",
	"could not find expected ':'":                    "a key here has no colon after it",

	// A tab where spaces belong, or a value starting with one of the
	// characters YAML keeps for itself — @ and ` are the ones people hit.
	"found character that cannot start any token":                  "a character here cannot start a value — quote it, or indent with spaces if it is a tab",
	"found a tab character that violates indentation":              "a tab is used to indent — YAML indents with spaces",
	"found a tab character where an indentation space is expected": "a tab is used to indent — YAML indents with spaces",

	// Pointing at where the quote opened.
	"found unexpected end of stream": "the file ends before something opened here is closed — usually a quote",

	// Pointing at the item, which is not always the line holding the bracket,
	// so these say only what was left open.
	"did not find expected ',' or ']'": "a [ list is never closed",
	"did not find expected ',' or '}'": "a { block is never closed",

	"unknown problem parsing YAML content": "not readable as YAML",
}
