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
