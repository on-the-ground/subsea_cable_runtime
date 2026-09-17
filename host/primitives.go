package host

import (
	"errors"
	"fmt"
	"math/big"

	"github.com/on-the-ground/subsea_cable_runtime/value"
)

// RationalProfile is the POC Host primitive-semantics profile "poc-rational/0".
//
// Numbers are exact rationals. `+` also concatenates two Strings. Equality of
// values of different kinds is false (not an error). `/` and `%` by zero, `%`
// on non-integers, and ordering across kinds are PrimitiveErrors. These are
// Host choices, not Subsea semantics.
type RationalProfile struct{}

func (RationalProfile) Profile() string { return "poc-rational/0" }

func (RationalProfile) Literal(text string) (value.Value, error) {
	r, ok := new(big.Rat).SetString(text)
	if !ok {
		return value.Value{}, fmt.Errorf("invalid number literal %q", text)
	}
	return value.Number(r), nil
}

func (RationalProfile) Unary(op string, x value.Value) (value.Value, error) {
	if op == "-" && x.Kind() == value.KNumber {
		return value.Number(new(big.Rat).Neg(x.AsRat())), nil
	}
	return value.Value{}, fmt.Errorf("unsupported operand for unary %s: %s", op, x.Kind())
}

func (RationalProfile) Binary(op string, l, r value.Value) (value.Value, error) {
	switch op {
	case "==":
		return value.Bool(l.Canonical() == r.Canonical()), nil
	case "!=":
		return value.Bool(l.Canonical() != r.Canonical()), nil
	}
	if op == "+" && l.Kind() == value.KString && r.Kind() == value.KString {
		return value.String(l.AsString() + r.AsString()), nil
	}
	if l.Kind() == value.KString && r.Kind() == value.KString {
		a, b := l.AsString(), r.AsString()
		switch op {
		case "<":
			return value.Bool(a < b), nil
		case "<=":
			return value.Bool(a <= b), nil
		case ">":
			return value.Bool(a > b), nil
		case ">=":
			return value.Bool(a >= b), nil
		}
	}
	if l.Kind() != value.KNumber || r.Kind() != value.KNumber {
		return value.Value{}, fmt.Errorf("unsupported operands for %s: %s and %s", op, l.Kind(), r.Kind())
	}
	a, b := l.AsRat(), r.AsRat()
	switch op {
	case "+":
		return value.Number(a.Add(a, b)), nil
	case "-":
		return value.Number(a.Sub(a, b)), nil
	case "*":
		return value.Number(a.Mul(a, b)), nil
	case "/":
		if b.Sign() == 0 {
			return value.Value{}, errors.New("division by zero")
		}
		return value.Number(a.Quo(a, b)), nil
	case "%":
		if b.Sign() == 0 {
			return value.Value{}, errors.New("modulo by zero")
		}
		if !a.IsInt() || !b.IsInt() {
			return value.Value{}, errors.New("% requires integers")
		}
		m := new(big.Int).Rem(a.Num(), b.Num())
		return value.Number(new(big.Rat).SetInt(m)), nil
	case "<":
		return value.Bool(a.Cmp(b) < 0), nil
	case "<=":
		return value.Bool(a.Cmp(b) <= 0), nil
	case ">":
		return value.Bool(a.Cmp(b) > 0), nil
	case ">=":
		return value.Bool(a.Cmp(b) >= 0), nil
	}
	return value.Value{}, fmt.Errorf("unsupported operator %s", op)
}
