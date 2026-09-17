package conformancetest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/on-the-ground/subsea_cable_runtime/sema"
	"github.com/on-the-ground/subsea_cable_runtime/syntax"
)

// TestSemanticCasesReportOnlyTheExpectedKind guards against noisy validators:
// every invalid-semantic fixture must report exactly its manifest kind.
func TestSemanticCasesReportOnlyTheExpectedKind(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join(corpus, "invalid-semantic", "*.subc"))
	for _, f := range files {
		want := filepath.Base(f[:len(f)-len(".subc")])
		src, _ := os.ReadFile(f)
		prog, err := syntax.Parse(src)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		_, errs := sema.Validate(prog, nil)
		for _, d := range errs {
			if d.Kind != want {
				t.Errorf("%s: unexpected extra diagnostic %v", want, d)
			}
		}
	}
}
