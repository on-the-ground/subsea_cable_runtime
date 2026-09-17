// Package expr evaluates Subsea value expressions.
//
// Subsea owns syntax, short-circuit selection, Boolean strictness, and
// structural map-key identity. Concrete operator meaning is delegated to a
// Primitives implementation supplied by the Host profile.
package expr

import (
	"fmt"

	"github.com/on-the-ground/subsea_cable_runtime/diag"
	"github.com/on-the-ground/subsea_cable_runtime/syntax"
	"github.com/on-the-ground/subsea_cable_runtime/value"
)

// Primitives is the Host primitive-semantics capability.
type Primitives interface {
	Profile() string
	Literal(text string) (value.Value, error)
	Unary(op string, x value.Value) (value.Value, error)
	Binary(op string, l, r value.Value) (value.Value, error)
}

// Binding is what a name resolves to.
type Binding struct {
	Value    value.Value
	HasValue bool
	// Pending names the occurrence whose output would supply this name but has
	// not resolved. Evaluation touching it fails with *PendingError.
	Pending string
	// Thunk is a top-level value binding evaluated on use.
	Thunk    syntax.Expr
	ThunkEnv Env
}

// Env resolves value names.
type Env interface {
	Lookup(name string) (Binding, bool)
}

// Scope is one lexical arrow scope.
type Scope struct {
	vars   map[string]Binding
	parent Env
}

// NewScope creates a child scope.
func NewScope(parent Env) *Scope { return &Scope{vars: map[string]Binding{}, parent: parent} }

func (s *Scope) Set(name string, v value.Value) {
	s.vars[name] = Binding{Value: v, HasValue: true}
}

func (s *Scope) Lookup(name string) (Binding, bool) {
	if b, ok := s.vars[name]; ok {
		return b, true
	}
	if s.parent != nil {
		return s.parent.Lookup(name)
	}
	return Binding{}, false
}

// Unit exposes a source unit's top-level non-Goal bindings.
type Unit struct {
	Values map[string]syntax.Expr
}

func (u *Unit) Lookup(name string) (Binding, bool) {
	if u == nil {
		return Binding{}, false
	}
	x, ok := u.Values[name]
	if !ok {
		return Binding{}, false
	}
	return Binding{Thunk: x, ThunkEnv: u}, true
}

// PendingError signals the conservative value barrier.
type PendingError struct{ Occurrence string }

func (e *PendingError) Error() string {
	return fmt.Sprintf("value from occurrence %s is not resolved", e.Occurrence)
}

// AnchorCaller invokes a nested $Anchor call from a function-leaf body.
type AnchorCaller func(call *syntax.Anchor, args []value.Value) (value.Output, error)

// Evaluator evaluates value expressions.
type Evaluator struct {
	Prim Primitives
	// Anchors is nil during deduction: value-position Anchor calls are then
	// refused (their staging is unresolved, see Owner decision R3).
	Anchors AnchorCaller
	Phase   diag.Phase
}

func (e *Evaluator) err(kind string, x syntax.Expr, format string, args ...any) error {
	return diag.New(kind, e.Phase, diag.At(x.Pos()), format, args...)
}

func (e *Evaluator) wrapPrim(x syntax.Expr, err error) error {
	if _, ok := err.(*diag.Diagnostic); ok {
		return err
	}
	d := diag.New("PrimitiveError", e.Phase, diag.At(x.Pos()), "%v", err)
	d.Details = map[string]any{"profile": e.Prim.Profile()}
	return d
}

// Eval evaluates x.
func (e *Evaluator) Eval(x syntax.Expr, env Env) (value.Value, error) {
	switch n := x.(type) {
	case *syntax.Group:
		return e.Eval(n.X, env)
	case *syntax.NumberLit:
		v, err := e.Prim.Literal(n.Text)
		if err != nil {
			return value.Value{}, e.wrapPrim(x, err)
		}
		return v, nil
	case *syntax.StringLit:
		if n.EscapeErr != "" {
			return value.Value{}, e.err("InvalidUnicodeEscape", x, "invalid escape %s", n.EscapeErr)
		}
		return value.String(n.Value), nil
	case *syntax.BoolLit:
		return value.Bool(n.Value), nil
	case *syntax.Unary:
		v, err := e.Eval(n.X, env)
		if err != nil {
			return v, err
		}
		if n.Op == "!" {
			if v.Kind() != value.KBool {
				return value.Value{}, e.err("PrimitiveError", x, "! requires a Boolean, got %s", v.Kind())
			}
			return value.Bool(!v.AsBool()), nil
		}
		r, perr := e.Prim.Unary(n.Op, v)
		if perr != nil {
			return value.Value{}, e.wrapPrim(x, perr)
		}
		return r, nil
	case *syntax.Binary:
		l, err := e.Eval(n.L, env)
		if err != nil {
			return l, err
		}
		if n.Op == "&&" || n.Op == "||" {
			if l.Kind() != value.KBool {
				return value.Value{}, e.err("PrimitiveError", x, "%s requires Booleans, got %s", n.Op, l.Kind())
			}
			if (n.Op == "&&" && !l.AsBool()) || (n.Op == "||" && l.AsBool()) {
				return l, nil // short-circuit: rhs is not selected
			}
			r, err := e.Eval(n.R, env)
			if err != nil {
				return r, err
			}
			if r.Kind() != value.KBool {
				return value.Value{}, e.err("PrimitiveError", x, "%s requires Booleans, got %s", n.Op, r.Kind())
			}
			return r, nil
		}
		r, err := e.Eval(n.R, env)
		if err != nil {
			return r, err
		}
		v, perr := e.Prim.Binary(n.Op, l, r)
		if perr != nil {
			return value.Value{}, e.wrapPrim(x, perr)
		}
		return v, nil
	case *syntax.MapLit:
		m := value.NewMap()
		for _, en := range n.Entries {
			v, err := e.Eval(en.Value, env)
			if err != nil {
				return v, err
			}
			m = m.With(StaticKey(en.Key), v)
		}
		return value.MapValue(m), nil
	case *syntax.Name:
		return e.evalName(n, env)
	case *syntax.Anchor:
		if e.Anchors == nil {
			return value.Value{}, e.err("UnsupportedByProfile", x,
				"value-position Anchor call $%s during deduction: staging is unresolved (Owner decision R3)", n.Ident)
		}
		args, err := e.EvalAll(n.Args, env)
		if err != nil {
			return value.Value{}, err
		}
		out, err := e.Anchors(n, args)
		if err != nil {
			return value.Value{}, err
		}
		if out.NoOutput {
			return value.Value{}, e.err("NoOutputAsValue", x, "$%s produced NoOutput where a value is required", n.Ident)
		}
		return out.Value, nil
	}
	return value.Value{}, e.err("InvalidStructuralContext", x, "%T is not a value expression", x)
}

// EvalAll evaluates xs left to right.
func (e *Evaluator) EvalAll(xs []syntax.Expr, env Env) ([]value.Value, error) {
	out := make([]value.Value, 0, len(xs))
	for _, x := range xs {
		v, err := e.Eval(x, env)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

func (e *Evaluator) resolve(n *syntax.Name, env Env) (value.Value, error) {
	b, ok := env.Lookup(n.Ident)
	if !ok {
		return value.Value{}, e.err("UnboundName", n, "unbound name %s", n.Ident)
	}
	switch {
	case b.HasValue:
		return b.Value, nil
	case b.Pending != "":
		return value.Value{}, &PendingError{Occurrence: b.Pending}
	default:
		return e.Eval(b.Thunk, b.ThunkEnv)
	}
}

func (e *Evaluator) evalName(n *syntax.Name, env Env) (value.Value, error) {
	upper, _ := syntax.IsUpper(n.Ident)
	switch n.Suffix {
	case syntax.NoSuffix:
		return e.resolve(n, env)
	case syntax.CallSuffix:
		if upper {
			return value.Value{}, e.err("UnsupportedByProfile", n,
				"eager Goal call %s(...) needs staging that is not specified yet (Owner decision R3)", n.Ident)
		}
		return value.Value{}, e.err("NotCallable", n, "%s is not callable", n.Ident)
	default:
		if upper {
			return value.Value{}, e.err("InvalidStructuralContext", n, "deferred Goal %s[...] is not a value", n.Ident)
		}
		if len(n.Args) != 1 {
			return value.Value{}, e.err("InvalidStructuralContext", n, "map lookup takes exactly one key")
		}
		key, err := e.Eval(n.Args[0], env)
		if err != nil {
			return key, err
		}
		entry, err := e.LookupEntry(n, env, key)
		if err != nil {
			return value.Value{}, err
		}
		if entry.HasValue {
			return entry.Value, nil
		}
		return e.Eval(entry.Thunk, entry.ThunkEnv)
	}
}

// LookupEntry performs selective map lookup: exact structural key, then `_`.
// For a top-level map literal the selected entry is returned unevaluated so
// that structure-valued entries can be expanded by the caller.
func (e *Evaluator) LookupEntry(n *syntax.Name, env Env, key value.Value) (Binding, error) {
	k, kerr := value.KeyOf(key)
	if kerr != nil {
		return Binding{}, e.err("KeyNotFound", n, "%v", kerr)
	}
	b, ok := env.Lookup(n.Ident)
	if !ok {
		return Binding{}, e.err("UnboundName", n, "unbound name %s", n.Ident)
	}
	if b.Pending != "" {
		return Binding{}, &PendingError{Occurrence: b.Pending}
	}
	if b.Thunk != nil {
		if lit, isLit := unwrapGroup(b.Thunk).(*syntax.MapLit); isLit {
			var wild *syntax.MapEntry
			for i, en := range lit.Entries {
				if en.Key.Kind == syntax.KeyWildcard {
					wild = &lit.Entries[i]
					continue
				}
				if StaticKey(en.Key).ID == k.ID {
					return Binding{Thunk: en.Value, ThunkEnv: b.ThunkEnv}, nil
				}
			}
			if wild != nil {
				return Binding{Thunk: wild.Value, ThunkEnv: b.ThunkEnv}, nil
			}
			return Binding{}, e.err("KeyNotFound", n, "key %s not found in %s", k.Display, n.Ident)
		}
	}
	mv, err := e.resolve(&syntax.Name{Ident: n.Ident, Span: n.Span}, env)
	if err != nil {
		return Binding{}, err
	}
	if mv.Kind() != value.KMap {
		return Binding{}, e.err("NotCallable", n, "%s is a %s, not a map", n.Ident, mv.Kind())
	}
	if v, ok := mv.AsMap().Get(k); ok {
		return Binding{Value: v, HasValue: true}, nil
	}
	if v, ok := mv.AsMap().Get(Wildcard); ok {
		return Binding{Value: v, HasValue: true}, nil
	}
	return Binding{}, e.err("KeyNotFound", n, "key %s not found in %s", k.Display, n.Ident)
}

// Wildcard is the fallback key of an ordinary lookup map.
var Wildcard = value.Key{ID: "_", Display: "_"}

// StaticKey converts a source key to its structural identity.
func StaticKey(k syntax.MapKey) value.Key {
	switch k.Kind {
	case syntax.KeyIdent, syntax.KeyString:
		return value.StringKey(k.Text)
	case syntax.KeyNumber:
		return value.NumberKeyText(syntax.NormalizeNumberText(k.Text))
	case syntax.KeyBool:
		return value.BoolKey(k.Text == "true")
	}
	return Wildcard
}

func unwrapGroup(x syntax.Expr) syntax.Expr {
	for {
		g, ok := x.(*syntax.Group)
		if !ok {
			return x
		}
		x = g.X
	}
}

// BindParams binds args to params in scope, enforcing arity and
// destructuring. Destructuring failures are DestructureMismatch.
func BindParams(p syntax.Params, args []value.Value, scope *Scope, phase diag.Phase, at syntax.Expr) error {
	if len(args) != p.Arity() {
		return diag.New("ArityMismatch", phase, diag.At(at.Pos()), "expected %d arguments, got %d", p.Arity(), len(args))
	}
	if !p.Destructure {
		for i, name := range p.Names {
			scope.Set(name, args[i])
		}
		return nil
	}
	arg := args[0]
	if arg.Kind() != value.KMap {
		return diag.New("DestructureMismatch", phase, diag.At(at.Pos()), "expected a map to destructure, got %s", arg.Kind())
	}
	for _, name := range p.Names {
		v, ok := arg.AsMap().Get(value.StringKey(name))
		if !ok {
			return diag.New("DestructureMismatch", phase, diag.At(at.Pos()), "map has no key %q", name)
		}
		scope.Set(name, v)
	}
	return nil
}
