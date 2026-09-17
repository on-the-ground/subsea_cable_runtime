// Package value is the POC Subsea value model: Boolean, Number, String, Map,
// and the internal NoOutput state carried by Output.
package value

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"
)

// Kind is the dynamic value class.
type Kind int

const (
	KBool Kind = iota + 1
	KNumber
	KString
	KMap
)

func (k Kind) String() string {
	switch k {
	case KBool:
		return "Boolean"
	case KNumber:
		return "Number"
	case KString:
		return "String"
	case KMap:
		return "Map"
	}
	return "Invalid"
}

// Value is an immutable Subsea value. The zero Value is invalid.
type Value struct {
	kind Kind
	b    bool
	n    *big.Rat // numeric model supplied by the POC Host profile
	s    string
	m    *Map
}

func Bool(b bool) Value       { return Value{kind: KBool, b: b} }
func String(s string) Value   { return Value{kind: KString, s: s} }
func Number(r *big.Rat) Value { return Value{kind: KNumber, n: new(big.Rat).Set(r)} }
func MapValue(m *Map) Value   { return Value{kind: KMap, m: m} }

// Int is a convenience constructor.
func Int(i int64) Value { return Number(new(big.Rat).SetInt64(i)) }

func (v Value) Kind() Kind       { return v.kind }
func (v Value) Valid() bool      { return v.kind != 0 }
func (v Value) AsBool() bool     { return v.b }
func (v Value) AsString() string { return v.s }
func (v Value) AsRat() *big.Rat  { return new(big.Rat).Set(v.n) }
func (v Value) AsMap() *Map      { return v.m }

// Canonical is a deterministic, prefix-free encoding (POC profile, not
// cross-runtime): atoms are length-framed and maps carry their entry count.
func (v Value) Canonical() string {
	var b strings.Builder
	v.writeCanonical(&b)
	return b.String()
}

func frame(b *strings.Builder, tag, s string) {
	fmt.Fprintf(b, "%s%d:%s", tag, len(s), s)
}

func (v Value) writeCanonical(b *strings.Builder) {
	switch v.kind {
	case KBool:
		frame(b, "b", strconv.FormatBool(v.b))
	case KNumber:
		frame(b, "n", RatKey(v.n))
	case KString:
		frame(b, "s", v.s)
	case KMap:
		v.m.writeCanonical(b)
	default:
		frame(b, "?", "invalid")
	}
}

func (v Value) String() string {
	switch v.kind {
	case KBool:
		return strconv.FormatBool(v.b)
	case KNumber:
		return RatKey(v.n)
	case KString:
		return strconv.Quote(v.s)
	case KMap:
		return v.m.String()
	}
	return "<invalid>"
}

// Digest is a short stable identity used for evaluation-instance material.
func Digest(vs []Value) string {
	h := sha256.New()
	fmt.Fprintf(h, "(args%d", len(vs))
	for _, v := range vs {
		h.Write([]byte(v.Canonical()))
	}
	h.Write([]byte(")"))
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// RatKey renders a rational as a normalized decimal when it terminates and as
// num/den otherwise. It is the POC's structural key for computed numbers.
func RatKey(r *big.Rat) string {
	if r.Sign() == 0 {
		return "0"
	}
	den := new(big.Int).Set(r.Denom())
	two, five := big.NewInt(2), big.NewInt(5)
	digits := 0
	for {
		q, m := new(big.Int).QuoRem(den, two, new(big.Int))
		if m.Sign() != 0 {
			break
		}
		den = q
		digits++
	}
	d5 := 0
	for {
		q, m := new(big.Int).QuoRem(den, five, new(big.Int))
		if m.Sign() != 0 {
			break
		}
		den = q
		d5++
	}
	if den.Cmp(big.NewInt(1)) != 0 {
		return r.Num().String() + "/" + r.Denom().String()
	}
	if d5 > digits {
		digits = d5
	}
	s := r.FloatString(digits)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	return s
}

// Key is a structural map key.
type Key struct {
	ID      string // "s:..", "n:..", "b:.."
	Display string
}

func StringKey(s string) Key     { return Key{ID: "s:" + s, Display: s} }
func BoolKey(b bool) Key         { t := strconv.FormatBool(b); return Key{ID: "b:" + t, Display: t} }
func NumberKeyText(t string) Key { return Key{ID: "n:" + t, Display: t} }

// KeyOf derives the structural key of a runtime value. Maps cannot be keys.
func KeyOf(v Value) (Key, error) {
	switch v.kind {
	case KString:
		return StringKey(v.s), nil
	case KBool:
		return BoolKey(v.b), nil
	case KNumber:
		return NumberKeyText(RatKey(v.n)), nil
	}
	return Key{}, fmt.Errorf("a %s value cannot be a map key", v.kind)
}

// Map is an immutable ordered map with structural keys.
type Map struct {
	keys []Key
	vals map[string]Value
}

// NewMap builds a map; later duplicates overwrite (callers validate first).
func NewMap() *Map { return &Map{vals: map[string]Value{}} }

// With returns a copy with k set.
func (m *Map) With(k Key, v Value) *Map {
	n := &Map{keys: append([]Key(nil), m.keys...), vals: make(map[string]Value, len(m.vals)+1)}
	for id, x := range m.vals {
		n.vals[id] = x
	}
	if _, ok := n.vals[k.ID]; !ok {
		n.keys = append(n.keys, k)
	}
	n.vals[k.ID] = v
	return n
}

func (m *Map) Get(k Key) (Value, bool) { v, ok := m.vals[k.ID]; return v, ok }
func (m *Map) Len() int                { return len(m.keys) }
func (m *Map) Keys() []Key             { return append([]Key(nil), m.keys...) }

func (m *Map) writeCanonical(b *strings.Builder) {
	ids := make([]string, 0, len(m.keys))
	for _, k := range m.keys {
		ids = append(ids, k.ID)
	}
	sort.Strings(ids)
	fmt.Fprintf(b, "(m%d", len(ids))
	for _, id := range ids {
		frame(b, "k", id)
		m.vals[id].writeCanonical(b)
	}
	b.WriteString(")")
}

func (m *Map) String() string {
	var b strings.Builder
	b.WriteString("{")
	for i, k := range m.keys {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(k.Display + ": " + m.vals[k.ID].String())
	}
	b.WriteString("}")
	return b.String()
}

// Output is the exported state of an occurrence: a value or NoOutput.
type Output struct {
	NoOutput bool
	Value    Value
}

// Of wraps a value.
func Of(v Value) Output { return Output{Value: v} }

// None is the NoOutput state.
var None = Output{NoOutput: true}

func (o Output) String() string {
	if o.NoOutput {
		return "NoOutput"
	}
	return o.Value.String()
}
