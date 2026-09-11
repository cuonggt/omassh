package yamlerr

import (
	"errors"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

var vocab = Vocabulary{
	Field: func(name, typ string) string {
		if strings.HasSuffix(typ, "Palette") {
			return "\"" + name + "\" is not a palette colour"
		}
		return "\"" + name + "\" is not a setting"
	},
	Type: func(typ string) string {
		return map[string]string{"config.Config": "settings", "[]string": "a list"}[typ]
	},
}

func typeErr(lines ...string) error { return &yaml.TypeError{Errors: lines} }

func TestItSaysWhatTheCallerCallsThings(t *testing.T) {
	got := InWords(typeErr(
		"line 3: field bg not found in type theme.Palette",
		"line 7: field ssh_option not found in type config.Config",
		"line 9: cannot unmarshal !!str `x` into []string",
	), vocab).Error()

	for _, want := range []string{
		`line 3: "bg" is not a palette colour`,
		`line 7: "ssh_option" is not a setting`,
		"line 9: this should be a list, not text",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n  %s", want, got)
		}
	}
	for _, leak := range []string{"not found in type", "theme.Palette", "config.Config", "!!str", "unmarshal"} {
		if strings.Contains(got, leak) {
			t.Errorf("leaks %q at the user:\n  %s", leak, got)
		}
	}
}

// A shape the vocabulary has no word for is left exactly as yaml wrote it.
// Describing it from whichever half of the sentence was understood would be a
// confident wrong answer, which is worse than something merely technical.
func TestWhatTheVocabularyCannotSayIsLeftAlone(t *testing.T) {
	for _, e := range []string{
		"line 9: cannot unmarshal !!str into chan int",       // no word for the type
		"line 9: cannot unmarshal !!timestamp into []string", // no word for the tag
		`line 9: mapping key "a" already defined at line 8`,  // not a shape at all
		"something yaml has not said before",
	} {
		if got := InWords(typeErr(e), vocab).Error(); got != e {
			t.Errorf("rewrote\n  %q\ninto\n  %q", e, got)
		}
	}
}

// Anything that is not a type error belongs to whoever made it.
func TestAnErrorOfAnotherKindIsHandedBack(t *testing.T) {
	err := errors.New("the file is not there")
	if got := InWords(err, vocab); got != err {
		t.Errorf("InWords = %v, want the error it was given", got)
	}
}

// A caller with nothing to say says nothing, rather than crashing on a nil.
func TestAnEmptyVocabularyChangesNothing(t *testing.T) {
	const e = "line 1: field x not found in type foo.Bar"
	if got := InWords(typeErr(e), Vocabulary{}).Error(); got != e {
		t.Errorf("InWords = %q", got)
	}
}

// decode runs a document through yaml the way the callers do, so the errors
// under test are yaml's own rather than strings typed in here. A yaml whose
// wording moves on then shows up as these sentences disappearing, which is the
// point: a table that has quietly stopped matching says nothing at all.
func decode(doc string) error {
	var v struct {
		Hosts []struct {
			Name string   `yaml:"name"`
			Addr string   `yaml:"addr"`
			Tags []string `yaml:"tags"`
		} `yaml:"hosts"`
	}
	dec := yaml.NewDecoder(strings.NewReader(doc))
	dec.KnownFields(true)
	return dec.Decode(&v)
}

// A file that will not parse is what people hit first — it never gets as far
// as having the wrong type in it — and it was the one thing still answered in
// yaml's voice: "yaml: line 2: mapping values are not allowed in this
// context", which names the library, then a token, then a context, and nothing
// to change.
func TestAFileThatIsNotYamlSaysWhatToChange(t *testing.T) {
	for _, tc := range []struct{ name, doc, want string }{{
		"an item that does not line up",
		"hosts:\n  - name: web\n    addr: 1.2.3.4\n   addr2: x\n",
		"does not line up",
	}, {
		"a line indented under a value",
		"hosts:\n  - name: web\n    addr: 1.2.3.4\n      user: x\n",
		"check the indentation",
	}, {
		"a colon in a value nobody quoted",
		"hosts:\n  - name: web: two\n",
		"quote any value with a colon in it",
	}, {
		"a tab used to indent",
		"hosts:\n\t- name: web\n",
		"indent with spaces",
	}, {
		"a tab further in",
		"hosts:\n  - name: web\n\t    addr: 1.2.3.4\n",
		"YAML indents with spaces",
	}, {
		"a quote left open",
		"hosts:\n  - name: \"web\n    addr: 1.2.3.4\n",
		"the file ends before",
	}, {
		"a bracket left open",
		"hosts:\n  - name: web\n    tags: [a, b\n",
		"a [ list is never closed",
	}, {
		"a key with no colon",
		"hosts:\n  - name: web\n    addr 1.2.3.4\n",
		"no colon after it",
	}} {
		t.Run(tc.name, func(t *testing.T) {
			err := decode(tc.doc)
			if err == nil {
				t.Fatal("yaml read this file happily, so there is nothing to say about it")
			}
			got := InWords(err, vocab).Error()
			if !strings.Contains(got, tc.want) {
				t.Errorf("InWords = %q, want it to say %q", got, tc.want)
			}
			// The line is how anyone finds the place, and yaml's prefix is the
			// library introducing itself to someone editing a file.
			if !strings.HasPrefix(got, "line ") {
				t.Errorf("InWords = %q, want it to start with the line", got)
			}
			if strings.Contains(got, "yaml") && !strings.Contains(got, "YAML indents") {
				t.Errorf("InWords = %q, and names yaml at the person editing the file", got)
			}
		})
	}
}

// Something yaml complains about that has no sentence here keeps yaml's own
// words, behind a plain opening. A technical hint beats none, and inventing a
// cause beats neither.
func TestAComplaintWithNoSentenceKeepsYamlsWords(t *testing.T) {
	err := decode("hosts:\n  - name: web\n%bad\n")
	if err == nil {
		t.Fatal("yaml read this file happily")
	}
	got := InWords(err, vocab).Error()
	if want := "line 3: not readable as YAML — found unknown directive name"; got != want {
		t.Errorf("InWords = %q, want %q", got, want)
	}
}
