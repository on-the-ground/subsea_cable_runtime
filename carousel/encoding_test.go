package carousel_test

import (
	"testing"

	"github.com/on-the-ground/subsea_cable_runtime/codebase"
	"github.com/on-the-ground/subsea_cable_runtime/host"
	"github.com/on-the-ground/subsea_cable_runtime/value"
)

// Review #4: canonical encodings must be collision-free.
func TestCanonicalEncodingHasNoKeyCollision(t *testing.T) {
	cb := codebase.New()
	a := store(t, cb, "Pair = [] -> $emit({a: 1, b: 2})\nPair[]", "Pair", 0)
	b := store(t, cb, "Pair = [] -> $emit({\"a=n:1 s:b\": 2})\nPair[]", "Pair", 0)
	if a.Hash == b.Hash || a.StructureHash == b.StructureHash {
		t.Fatal("distinct artifacts share a hash")
	}

	m1 := value.MapValue(value.NewMap().With(value.StringKey("a"), value.Int(1)).With(value.StringKey("b"), value.Int(2)))
	m2 := value.MapValue(value.NewMap().With(value.StringKey("a=n:1 s:b"), value.Int(2)))
	m3 := value.MapValue(value.NewMap().With(value.StringKey("a"), value.String("1,s:b=2")))
	m4 := value.MapValue(value.NewMap().With(value.StringKey("a"), value.String("1")).With(value.StringKey("b"), value.Int(2)))
	vals := []value.Value{m1, m2, m3, m4, value.String("n:1"), value.Int(1)}
	seen := map[string]int{}
	for i, v := range vals {
		if j, dup := seen[v.Canonical()]; dup {
			t.Fatalf("values %d and %d share canonical form %q", j, i, v.Canonical())
		}
		seen[v.Canonical()] = i
	}
	eq, err := host.RationalProfile{}.Binary("==", m1, m2)
	if err != nil || eq.AsBool() {
		t.Fatal("distinct maps compared equal")
	}
	if value.Digest([]value.Value{value.String("a"), value.String("b")}) == value.Digest([]value.Value{value.String("a\x00b")}) ||
		value.Digest([]value.Value{m1}) == value.Digest([]value.Value{m2}) {
		t.Fatal("argument digests collide")
	}
}
