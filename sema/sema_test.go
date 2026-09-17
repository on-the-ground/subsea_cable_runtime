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
		{"bare uppercase value in composition is still a Goal", "X = 1\nA = [] -> $a\nR = [] -> [A, X]\nR[]", "GoalNotFound"},
		{"bare entry in a lookup map is a value", "m = {a: A}\nA = [] -> $a\nR = [k] -> m[k]\nR[\"a\"]", "InvalidStructuralContext"},
		{"nested serial gets no upstream value", "A = [] -> $a\nB = [x] -> $b(x)\nR = [] -> [A, [B, A]]\nR[]", "ArityMismatch"},
		{"nested serial first stage is /0", "A = [] -> $a\nB = [] -> $b\nC = [x] -> $c(x)\nR = [] -> [A, [B, C]]\nR[]", ""},
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

// SCP-0003: value-producing calls outside function leaves must be direct
// structural occurrences.
func TestValueProducingCallPositions(t *testing.T) {
	const pre = "C = (x) -> x + 1\nD = [y] -> $d(y)\n"
	cases := []struct {
		name, src, want string
	}{
		{"nested eager Goal argument", pre + "R = [x] -> D[C(x)]\nR[1]", "InvalidStructuralContext"},
		{"nested Anchor argument", pre + "R = [x] -> D[$f(x)]\nR[1]", "InvalidStructuralContext"},
		{"operator operand", pre + "R = [x] -> D[x + C(1)]\nR[1]", "InvalidStructuralContext"},
		{"short-circuit operand", pre + "R = [x] -> D[false && C(1)]\nR[1]", "InvalidStructuralContext"},
		{"policy argument", pre + "R = [x] -> @p(C(x)) D[x]\nR[1]", "InvalidStructuralContext"},
		{"lookup key", pre + "m = {a: 1}\nR = [x] -> D[m[C(x)]]\nR[1]", "InvalidStructuralContext"},
		{"value map", pre + "R = [x] -> D[{a: $f(x)}]\nR[1]", "InvalidStructuralContext"},
		{"top-level value", pre + "v = C(1)\nR = [] -> D[v]\nR[]", "InvalidStructuralContext"},
		{"Anchor argument", pre + "R = [] -> $emit(C(1))\nR[]", "InvalidStructuralContext"},
		{"Root argument", pre + "D[C(1)]", "InvalidStructuralContext"},
		{"direct body", pre + "R = [x] -> C(x)\nR[1]", ""},
		{"serial stage", pre + "R = [x] -> [C(x), [v] -> D[v]]\nR[1]", ""},
		{"parallel element", pre + "R = [x] -> {C(x), $f(x)}\nR[1]", ""},
		{"resolving-map branch", pre + "R = [x] -> {left: C(x), right: $f(x)}\nR[1]", ""},
		{"policy on a direct call", pre + "R = [x] -> [@p C(x), D]\nR[1]", ""},
		{"primitive argument", pre + "R = [x] -> D[x + 1]\nR[1]", ""},
		{"nested Anchor in a function leaf", "F = (x) -> $g($h(x))\nF[1]", ""},
		{"Goal call in a function leaf", pre + "F = (x) -> C(x)\nF[1]", "InvalidStructuralContext"},
		{"static destructure on a direct call", "values = {x: 1}\nLeaf = ({x, y}) -> x\nR = [] -> Leaf(values)\nR[]", "DestructureMismatch"},
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
			for _, d := range got {
				if d.Kind != c.want {
					t.Fatalf("want only %s, got %v", c.want, got)
				}
			}
			if !got.Has(c.want) {
				t.Fatalf("want %s, got %v", c.want, got.Kinds())
			}
		})
	}
}

func TestStructureLookupIsFlaggedAsUnsupported(t *testing.T) {
	u, err := sema.Check([]byte("routes = {a: A[]}\nA = [] -> $a\nR = [k] -> routes[k]\nR[\"a\"]"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(u.Unsupported) == 0 {
		t.Fatal("structure-valued lookup maps must be flagged for the POC")
	}
}
