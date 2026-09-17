// Package conformancetest runs the language conformance corpus
// (../conformance/cases.tsv) against the POC frontend and validator.
package conformancetest

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/on-the-ground/subsea_cable_runtime/diag"
	"github.com/on-the-ground/subsea_cable_runtime/sema"
	"github.com/on-the-ground/subsea_cable_runtime/syntax"
)

// corpus is the pinned language repository's conformance directory. The
// language is vendored as the `language` git submodule; SUBSEA_LANGUAGE_DIR
// overrides it (the drift workflow points it at the language main branch).
var corpus = func() string {
	if dir := os.Getenv("SUBSEA_LANGUAGE_DIR"); dir != "" {
		return filepath.Join(dir, "conformance")
	}
	return filepath.Join("..", "language", "conformance")
}()

func TestConformanceCorpus(t *testing.T) {
	f, err := os.Open(filepath.Join(corpus, "cases.tsv"))
	if err != nil {
		t.Fatalf("%v (run `git submodule update --init` or set SUBSEA_LANGUAGE_DIR)", err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Scan() // header
	n := 0
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if line == "" {
			continue
		}
		cols := strings.Split(line, "\t")
		phase, expected, file := cols[0], cols[1], cols[2]
		n++
		t.Run(file, func(t *testing.T) {
			src, err := os.ReadFile(filepath.Join(corpus, file))
			if err != nil {
				t.Fatal(err)
			}
			prog, perr := syntax.Parse(src)
			if phase == "syntax" && expected == "SyntaxError" {
				d, ok := diag.As(perr)
				if !ok || d.Kind != "SyntaxError" {
					t.Fatalf("expected SyntaxError, got %v", perr)
				}
				return
			}
			if perr != nil {
				t.Fatalf("expected the input to parse, got %v", perr)
			}
			_, errs := sema.Validate(prog, nil)
			switch {
			case expected == "accept":
				if len(errs) > 0 {
					t.Fatalf("expected a valid program, got %v", errs)
				}
			default:
				if !errs.Has(expected) {
					t.Fatalf("expected %s, got %v", expected, errs.Kinds())
				}
				for _, d := range errs {
					if d.Phase != diag.Validation {
						t.Fatalf("expected validation phase, got %s", d.Phase)
					}
				}
			}
		})
	}
	if n == 0 {
		t.Fatal("no cases found")
	}
}
