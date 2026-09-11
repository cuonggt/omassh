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
		return err
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
