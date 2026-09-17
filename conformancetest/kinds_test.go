package conformancetest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/on-the-ground/subsea_cable_runtime/sema"
	"github.com/on-the-ground/subsea_cable_runtime/syntax"
)

// TestSemanticCasesReportOnlyTheExpectedKind guards against noisy validators:
// every invalid-semantic fixture must report exactly its manifest kind.
func TestSemanticCasesReportOnlyTheExpectedKind(t *testing.T) {
	manifest, err := os.ReadFile(filepath.Join(corpus, "cases.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, line := range strings.Split(string(manifest), "\n") {
		cols := strings.Split(strings.TrimRight(line, "\r"), "\t")
		if len(cols) != 3 || !strings.HasPrefix(cols[2], "invalid-semantic/") {
			continue
		}
		want, file := cols[1], cols[2]
		n++
		src, err := os.ReadFile(filepath.Join(corpus, file))
		if err != nil {
			t.Fatal(err)
		}
		prog, err := syntax.Parse(src)
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		_, errs := sema.Validate(prog, nil)
		for _, d := range errs {
			if d.Kind != want {
				t.Errorf("%s: expected only %s, got extra %v", file, want, d)
			}
		}
	}
	if n == 0 {
		t.Fatal("no invalid-semantic cases in the manifest")
	}
}
