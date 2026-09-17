package syntax_test

import (
	"testing"

	"github.com/on-the-ground/subsea_cable_runtime/diag"
	"github.com/on-the-ground/subsea_cable_runtime/syntax"
)

// SubseaCable.g4 (`program : TERMINATOR? statementList EOF`, where
// statementList needs at least one statement) and the EBNF both reject an
// empty source, as ANTLR confirms (`mismatched input '<EOF>'`). The POC follows
// the pinned grammar. Accepting empty programs would be a grammar change.
func TestEmptySourceFollowsTheGrammar(t *testing.T) {
	for _, src := range []string{"", "\n\n", "// only a comment\n"} {
		_, err := syntax.Parse([]byte(src))
		d, ok := diag.As(err)
		if !ok || d.Kind != "SyntaxError" {
			t.Fatalf("%q: expected SyntaxError, got %v", src, err)
		}
	}
}

func TestInvalidUTF8(t *testing.T) {
	_, err := syntax.Parse([]byte{0xff, 'A'})
	if d, ok := diag.As(err); !ok || d.Kind != "InvalidSourceEncoding" {
		t.Fatalf("got %v", err)
	}
}
