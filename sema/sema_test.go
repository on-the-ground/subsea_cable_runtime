package sema_test

import (
	"testing"

	"github.com/on-the-ground/subsea_cable_runtime/diag"
	"github.com/on-the-ground/subsea_cable_runtime/sema"
)

func kinds(t *testing.T, src string) diag.List {
	t.Helper()
	_, err := sema.Check([]byte(src), nil)
	if err == nil {
		return nil
	}
	if l, ok := err.(diag.List); ok {
		return l
	}
	d, _ := diag.As(err)
	return diag.List{d}
}

func TestRoutingRules(t *testing.T) {
	cases := []struct {
		name, src, want string
	}{
		{"serial shorthand", "A = [] -> $a\nB = [x] -> $b(x)\nR = [] -> [A, B]\nR[]", ""},
		{"explicit args ignore upstream", "A = [] -> $a\nB = [y] -> $b(y)\nR = [y] -> [A, B[y]]\nR[1]", ""},
		{"bare after parallel", "A = [] -> $a\nC = [x] -> $c(x)\nR = [] -> [{A, A}, C]\nR[]", "InvalidStructuralContext"},
		{"arrow after parallel", "A = [] -> $a\nC = [x] -> $c(x)\nR = [] -> [{A, A}, [x] -> C[x]]\nR[]", "InvalidStructuralContext"},
		{"dependency only after parallel", "A = [] -> $a\nC = [] -> $c\nR = [] -> [{A, A}, C[]]\nR[]", ""},
		{"fan-out", "A = [] -> $a\nB = [x] -> $b(x)\nR = [] -> [A, {B, B}]\nR[]", ""},
		{"mixed fan-out", "A = [] -> $a\nB = [x] -> $b(x)\nR = [y] -> [A, {B, B[y]}]\nR[1]", "InvalidStructuralContext"},
		{"resolving map", "A = [] -> $a\nC = [x, y] -> $c(x, y)\nR = [] -> [[] -> {x: A, y: A}, [{x, y}] -> C[x, y]]\nR[]", ""},
		{"wildcard resolving key", "A = [] -> $a\nR = [] -> {_: A}\nR[]", "InvalidStructuralContext"},
		{"primitive body", "R = [x] -> 42\nR[1]", "SyntaxError"},
		{"anchor binding", "a = $host\nR = [] -> $b\nR[]", "InvalidStructuralContext"},
		{"leaf disguised as goal", "Add = $plus\nR = [] -> $b\nR[]", "InvalidStructuralContext"},
		{"duplicate goal", "A = [] -> $a\nA = [] -> $b\nA[]", "DuplicateBinding"},
		{"overload by arity", "A = [] -> $a\nA = [x] -> $b(x)\nA[]", ""},
		{"duplicate parameter", "A = [x, x] -> $a(x)\nA[1, 2]", "DuplicateParameter"},
		{"goal is not a value", "A = [] -> $a\nR = [] -> $b(A)\nR[]", "UnboundName"},
		{"static key not found", "m = {a: 1}\nR = [] -> $b(m[\"z\"])\nR[]", "KeyNotFound"},
		{"two roots", "A = [] -> $a\nA[]\nA[]", "InvalidRoot"},
		{"eager root", "A = [] -> $a\nA()", "InvalidRoot"},
		{"forward reference", "R = [] -> A[]\nA = [] -> $a\nR[]", ""},
		{"continuation", "A = [x] ->\n  $a(\n    x +\n    1\n  )\nA[1]", ""},
		{"number key identity", "m = {1: \"a\", 1.0: \"b\"}\nR = [] -> $b(m)\nR[]", "DuplicateMapKey"},
		{"bool vs string key", "m = {true: 1, \"true\": 2}\nR = [] -> $b(m)\nR[]", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := kinds(t, c.src)
			if c.want == "" {
				if len(got) > 0 {
					t.Fatalf("unexpected %v", got)
				}
				return
			}
			if !got.Has(c.want) {
				t.Fatalf("want %s, got %v", c.want, got.Kinds())
			}
		})
	}
}

func TestUnsupportedConstructsAreFlagged(t *testing.T) {
	u, err := sema.Check([]byte("C = (x) -> x\nR = [] -> $emit(C(1))\nR[]"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(u.Unsupported) == 0 {
		t.Fatal("eager calls and value-position Anchors must be flagged for the POC")
	}
}
