// Package sema performs structural validation of one parsed source unit and
// produces a prepared Unit. It covers the rules exercised by the conformance
// corpus plus the routing rules in README.md; see carousel/docs/POC_PROFILE.md
// for the known validation gaps of this POC.
package sema

import (
	"fmt"
	"sort"

	"github.com/on-the-ground/subsea_cable_runtime/diag"
	"github.com/on-the-ground/subsea_cable_runtime/expr"
	"github.com/on-the-ground/subsea_cable_runtime/syntax"
)

// PrefixMatch is one stored artifact matching a hash prefix.
type PrefixMatch struct {
	Hash  string
	Arity int
}

// Index is the read-only codebase view validation may consult.
type Index interface {
	Arities(name string) []int
	ResolvePrefix(name, prefix string) []PrefixMatch
}

// GoalDef is one named Goal definition.
type GoalDef struct {
	Name       string
	Arity      int
	Arrow      syntax.Expr // *syntax.GoalArrow or *syntax.FuncArrow
	IsFunction bool
	Span       diag.Span
}

// Unit is a validated, prepared source unit.
type Unit struct {
	Program *syntax.Program
	Goals   []*GoalDef
	Values  *expr.Unit
	Root    *syntax.Name
	// Unsupported lists statically found constructs this POC cannot run.
	Unsupported diag.List
}

// Check parses and validates src.
func Check(src []byte, idx Index) (*Unit, error) {
	prog, err := syntax.Parse(src)
	if err != nil {
		return nil, err
	}
	u, errs := Validate(prog, idx)
	if len(errs) > 0 {
		return nil, errs
	}
	return u, nil
}

type inputKind int

const (
	inNone inputKind = iota
	inValue
	inNoOutput
)

type scope struct {
	names  map[string]bool
	parent *scope
}

func (s *scope) has(n string) bool {
	for c := s; c != nil; c = c.parent {
		if c.names[n] {
			return true
		}
	}
	return false
}

type checker struct {
	idx         Index
	errs        diag.List
	unsupported diag.List
	goals       map[string]map[int]*GoalDef
	values      map[string]syntax.Expr
	owner       string
	edges       map[string]map[string]bool
}

func (c *checker) add(kind string, x syntax.Expr, format string, args ...any) {
	var sp *diag.Span
	if x != nil {
		sp = diag.At(x.Pos())
	}
	c.errs = append(c.errs, diag.New(kind, diag.Validation, sp, format, args...))
}

func (c *checker) unsupportedAt(x syntax.Expr, format string, args ...any) {
	c.unsupported = append(c.unsupported, diag.New("UnsupportedByProfile", diag.Profile, diag.At(x.Pos()), format, args...))
}

func (c *checker) edge(to string) {
	if c.owner == "" {
		return
	}
	if c.edges[c.owner] == nil {
		c.edges[c.owner] = map[string]bool{}
	}
	c.edges[c.owner][to] = true
}

// Validate checks prog and prepares a Unit. idx may be nil.
func Validate(prog *syntax.Program, idx Index) (*Unit, diag.List) {
	c := &checker{idx: idx, goals: map[string]map[int]*GoalDef{}, values: map[string]syntax.Expr{}, edges: map[string]map[string]bool{}}
	unit := &Unit{Program: prog}

	// Pass 1: classify bindings.
	for _, b := range prog.Bindings {
		upper, hasLetter := syntax.IsUpper(b.Name)
		var params syntax.Params
		isArrow, isFn := false, false
		switch a := b.Value.(type) {
		case *syntax.GoalArrow:
			isArrow, params = true, a.Params
		case *syntax.FuncArrow:
			isArrow, isFn, params = true, true, a.Params
		}
		switch {
		case isArrow && (!upper || !hasLetter):
			c.add("InvalidStructuralContext", b.Value, "Goal definition %s must start with an uppercase letter", b.Name)
		case isArrow:
			def := &GoalDef{Name: b.Name, Arity: params.Arity(), Arrow: b.Value, IsFunction: isFn, Span: b.Span}
			if c.goals[b.Name] == nil {
				c.goals[b.Name] = map[int]*GoalDef{}
			}
			if _, dup := c.goals[b.Name][def.Arity]; dup {
				c.add("DuplicateBinding", b.Value, "%s/%d is defined more than once", b.Name, def.Arity)
				continue
			}
			c.goals[b.Name][def.Arity] = def
			unit.Goals = append(unit.Goals, def)
		default:
			if _, isAnchor := b.Value.(*syntax.Anchor); isAnchor {
				c.add("InvalidStructuralContext", b.Value, "%s cannot be bound directly to an Anchor", b.Name)
				continue
			}
			if _, dup := c.values[b.Name]; dup {
				c.add("DuplicateBinding", b.Value, "%s is bound more than once", b.Name)
				continue
			}
			c.values[b.Name] = b.Value
		}
	}
	for name := range c.values {
		if c.goals[name] != nil {
			c.add("DuplicateBinding", c.values[name], "%s is both a Goal and a non-Goal binding", name)
		}
	}
	unit.Values = &expr.Unit{Values: c.values}

	top := &scope{names: map[string]bool{}}

	// Pass 2: check definitions.
	for _, def := range unit.Goals {
		c.owner = goalNode(def.Name, def.Arity)
		switch a := def.Arrow.(type) {
		case *syntax.GoalArrow:
			sc := c.paramScope(a.Params, top, a)
			c.checkBody(a.Body, sc)
		case *syntax.FuncArrow:
			sc := c.paramScope(a.Params, top, a)
			for _, x := range a.Body {
				c.checkValue(x, sc, true)
			}
		}
	}
	names := make([]string, 0, len(c.values))
	for n := range c.values {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		c.owner = "v:" + n
		x := c.values[n]
		switch v := x.(type) {
		case *syntax.MapLit:
			c.checkMapKeys(v)
			for _, en := range v.Entries {
				if c.structuralShaped(en.Value) {
					c.checkStructureDefinitionOnly(en.Value, top)
				} else {
					c.checkValue(en.Value, top, false)
				}
			}
		case *syntax.GoalArrow, *syntax.FuncArrow:
			// already reported
		default:
			c.checkValue(x, top, false)
		}
	}

	// Root.
	c.owner = ""
	switch len(prog.Roots) {
	case 0:
		c.add("InvalidRoot", nil, "the Program has no Root expression")
	case 1:
		r := prog.Roots[0]
		n, ok := r.(*syntax.Name)
		upper, _ := syntax.IsUpper(nameIdent(n))
		if !ok || !upper || n.Suffix != syntax.BracketSuffix {
			c.add("InvalidRoot", r, "the Root must be a deferred Goal reduction such as Build[input]")
		} else {
			c.owner = "root"
			c.goalRef(n, len(n.Args))
			for _, a := range n.Args {
				c.checkValue(a, top, false)
			}
			unit.Root = n
		}
	default:
		c.add("InvalidRoot", prog.Roots[1], "the Program has %d Root expressions; exactly one is required", len(prog.Roots))
	}

	c.detectCycles()
	unit.Unsupported = c.unsupported
	return unit, c.errs
}

func nameIdent(n *syntax.Name) string {
	if n == nil {
		return ""
	}
	return n.Ident
}

func goalNode(name string, arity int) string { return fmt.Sprintf("%s/%d", name, arity) }

func (c *checker) paramScope(p syntax.Params, parent *scope, at syntax.Expr) *scope {
	sc := &scope{names: map[string]bool{}, parent: parent}
	for _, n := range p.Names {
		if sc.names[n] {
			c.add("DuplicateParameter", at, "parameter %s is declared more than once", n)
		}
		sc.names[n] = true
	}
	return sc
}

func (c *checker) goalRef(n *syntax.Name, arity int) {
	if n.Hash != "" {
		var matches []PrefixMatch
		if c.idx != nil {
			matches = c.idx.ResolvePrefix(n.Ident, n.Hash)
		}
		switch len(matches) {
		case 0:
			c.add("HashNotFound", n, "no stored %s matches #%s", n.Ident, n.Hash)
		case 1:
			if matches[0].Arity != arity {
				c.add("ArityMismatch", n, "%s#%s has arity %d, used with %d", n.Ident, n.Hash, matches[0].Arity, arity)
			}
		default:
			c.add("AmbiguousHashPrefix", n, "#%s matches %d artifacts", n.Hash, len(matches))
		}
		return
	}
	if c.goals[n.Ident][arity] != nil {
		c.edge(goalNode(n.Ident, arity))
		return
	}
	var avail []int
	for a := range c.goals[n.Ident] {
		avail = append(avail, a)
	}
	if c.idx != nil {
		for _, a := range c.idx.Arities(n.Ident) {
			if a == arity {
				return
			}
			avail = append(avail, a)
		}
	}
	if len(avail) > 0 {
		sort.Ints(avail)
		c.errs = append(c.errs, &diag.Diagnostic{Kind: "ArityMismatch", Phase: diag.Validation, Span: diag.At(n.Span),
			Message: fmt.Sprintf("%s is not defined with arity %d", n.Ident, arity),
			Details: map[string]any{"available": avail}})
		return
	}
	c.add("GoalNotFound", n, "Goal %s/%d is not defined", n.Ident, arity)
}

func (c *checker) isGoalName(name string) bool {
	if c.goals[name] != nil {
		return true
	}
	return c.idx != nil && len(c.idx.Arities(name)) > 0
}

func (c *checker) structuralShaped(x syntax.Expr) bool {
	switch n := x.(type) {
	case *syntax.Policied, *syntax.Anchor, *syntax.Serial, *syntax.Parallel, *syntax.GoalArrow:
		return true
	case *syntax.Name:
		upper, _ := syntax.IsUpper(n.Ident)
		if !upper {
			return false
		}
		return n.Suffix != syntax.NoSuffix || c.isGoalName(n.Ident)
	}
	return false
}

// checkStructureDefinitionOnly checks a structure-valued lookup-map entry
// without routing context; routing is checked where the lookup is used.
func (c *checker) checkStructureDefinitionOnly(x syntax.Expr, sc *scope) {
	if n, ok := x.(*syntax.Name); ok && n.Suffix == syntax.NoSuffix {
		return
	}
	c.checkStructure(x, sc, inNone, true)
}

func (c *checker) checkMapKeys(m *syntax.MapLit) {
	seen := map[string]bool{}
	for _, en := range m.Entries {
		id := expr.StaticKey(en.Key).ID
		if seen[id] {
			c.add("DuplicateMapKey", m, "duplicate map key %s", en.Key.Text)
		}
		seen[id] = true
	}
}

func isResolvingBody(x syntax.Expr) (*syntax.MapLit, []syntax.Policy) {
	switch n := x.(type) {
	case *syntax.MapLit:
		return n, nil
	case *syntax.Policied:
		if m, ok := n.Target.(*syntax.MapLit); ok {
			return m, n.Policies
		}
	}
	return nil, nil
}

func (c *checker) checkPolicies(ps []syntax.Policy, sc *scope) {
	for _, p := range ps {
		for _, a := range p.Args {
			c.checkValue(a, sc, false)
		}
	}
}

// checkBody checks a Goal-arrow body and reports whether it exports a value.
func (c *checker) checkBody(body syntax.Expr, sc *scope) bool {
	if m, pols := isResolvingBody(body); m != nil {
		c.checkPolicies(pols, sc)
		c.checkMapKeys(m)
		if len(m.Entries) == 0 {
			c.add("InvalidStructuralContext", m, "an empty map is an ordinary value, not Goal structure")
		}
		for _, en := range m.Entries {
			if en.Key.Kind == syntax.KeyWildcard {
				c.add("InvalidStructuralContext", m, "a resolving map requires concrete keys")
			}
			if !c.checkStructure(en.Value, sc, inNone, true) {
				c.add("InvalidStructuralContext", en.Value, "resolving-map branch %s exports NoOutput", en.Key.Text)
			}
		}
		return true
	}
	return c.checkStructure(body, sc, inNone, false)
}

func bareGoal(x syntax.Expr) bool {
	if p, ok := x.(*syntax.Policied); ok {
		x = p.Target
	}
	n, ok := x.(*syntax.Name)
	if !ok || n.Suffix != syntax.NoSuffix {
		return false
	}
	upper, _ := syntax.IsUpper(n.Ident)
	return upper
}

// checkStructure checks x in Goal-structure context and reports whether it
// exports a value (false means NoOutput).
func (c *checker) checkStructure(x syntax.Expr, sc *scope, in inputKind, stage bool) bool {
	switch n := x.(type) {
	case *syntax.Policied:
		c.checkPolicies(n.Policies, sc)
		return c.checkStructure(n.Target, sc, in, stage)
	case *syntax.Name:
		upper, hasLetter := syntax.IsUpper(n.Ident)
		if !hasLetter {
			c.add("InvalidStructuralContext", n, "%s cannot be used as Goal structure", n.Ident)
			return true
		}
		if upper {
			switch n.Suffix {
			case syntax.BracketSuffix:
				c.goalRef(n, len(n.Args))
			case syntax.CallSuffix:
				c.goalRef(n, len(n.Args))
				c.unsupportedAt(n, "eager Goal call %s(...) (Owner decision R3)", n.Ident)
			default:
				if !stage {
					c.add("InvalidStructuralContext", n, "bare Goal %s is only valid as a composition stage", n.Ident)
					return true
				}
				switch in {
				case inNoOutput:
					c.add("InvalidStructuralContext", n, "bare Goal %s requires an upstream value, but the previous stage exports NoOutput", n.Ident)
				case inValue:
					c.goalRef(n, 1)
				default:
					c.goalRef(n, 0)
				}
				return true
			}
			for _, a := range n.Args {
				c.checkValue(a, sc, false)
			}
			return true
		}
		switch n.Suffix {
		case syntax.BracketSuffix:
			return c.checkLookupStructure(n, sc, in, stage)
		case syntax.CallSuffix:
			c.add("NotCallable", n, "%s is not callable", n.Ident)
		default:
			c.add("InvalidStructuralContext", n, "value %s cannot be used as Goal structure", n.Ident)
		}
		return true
	case *syntax.Anchor:
		for _, a := range n.Args {
			c.checkValue(a, sc, false)
		}
		return true
	case *syntax.GoalArrow:
		inner := c.paramScope(n.Params, sc, n)
		if stage {
			switch {
			case n.Params.Arity() > 1:
				c.add("InvalidStructuralContext", n, "an inline Goal-arrow stage receives at most one value")
			case n.Params.Arity() == 1 && in != inValue:
				c.add("InvalidStructuralContext", n, "the Goal-arrow stage expects a value, but none is routed to it")
			}
		}
		return c.checkBody(n.Body, inner)
	case *syntax.Serial:
		prev := inNone
		exports := true
		for i, s := range n.Stages {
			stageIn := inNone
			if i > 0 {
				stageIn = prev
			}
			exports = c.checkStructure(s, sc, stageIn, true)
			if exports {
				prev = inValue
			} else {
				prev = inNoOutput
			}
		}
		return exports
	case *syntax.Parallel:
		bare := 0
		for _, s := range n.Stages {
			if bareGoal(s) {
				bare++
			}
		}
		childIn := inNone
		if in == inValue {
			switch {
			case bare == len(n.Stages):
				childIn = inValue
			case bare > 0:
				c.add("InvalidStructuralContext", n, "implicit and explicit branches cannot be mixed in a downstream parallel group")
			}
		}
		if in == inNoOutput && bare > 0 {
			c.add("InvalidStructuralContext", n, "bare Goals cannot receive NoOutput")
			childIn = inNone
		}
		for _, s := range n.Stages {
			c.checkStructure(s, sc, childIn, true)
		}
		return false
	case *syntax.MapLit:
		c.add("InvalidStructuralContext", n, "a map is only Goal structure as the direct body of a Goal arrow")
		return true
	case *syntax.FuncArrow:
		c.add("InvalidStructuralContext", n, "a function arrow is never inline structure")
		return true
	}
	c.add("InvalidStructuralContext", x, "a value expression cannot be Goal structure")
	return true
}

func (c *checker) checkLookupStructure(n *syntax.Name, sc *scope, in inputKind, stage bool) bool {
	if len(n.Args) != 1 {
		c.add("InvalidStructuralContext", n, "map lookup takes exactly one key")
		return true
	}
	c.checkValue(n.Args[0], sc, false)
	m, ok := c.values[n.Ident].(*syntax.MapLit)
	if sc.has(n.Ident) || !ok {
		c.add("InvalidStructuralContext", n, "%s must be a top-level map whose entries are Goal structure", n.Ident)
		return true
	}
	c.edge("v:" + n.Ident)
	exports := true
	for _, en := range m.Entries {
		if !c.structuralShaped(en.Value) {
			c.add("InvalidStructuralContext", en.Value, "lookup entry %s of %s is not Goal structure", en.Key.Text, n.Ident)
			continue
		}
		if !c.checkStructure(en.Value, &scope{names: map[string]bool{}}, in, stage) {
			exports = false
		}
	}
	return exports
}

func (c *checker) checkValue(x syntax.Expr, sc *scope, fn bool) {
	switch n := x.(type) {
	case *syntax.Group:
		c.checkValue(n.X, sc, fn)
	case *syntax.NumberLit, *syntax.BoolLit:
	case *syntax.StringLit:
		if n.EscapeErr != "" {
			c.add("InvalidUnicodeEscape", n, "escape %s is not a Unicode scalar value", n.EscapeErr)
		}
	case *syntax.Unary:
		c.checkValue(n.X, sc, fn)
	case *syntax.Binary:
		c.checkValue(n.L, sc, fn)
		c.checkValue(n.R, sc, fn)
	case *syntax.MapLit:
		c.checkMapKeys(n)
		wild := 0
		for _, en := range n.Entries {
			if en.Key.Kind == syntax.KeyWildcard {
				wild++
			}
			c.checkValue(en.Value, sc, fn)
		}
		if wild > 1 {
			c.add("DuplicateMapKey", n, "a map may contain at most one wildcard")
		}
	case *syntax.Anchor:
		for _, a := range n.Args {
			c.checkValue(a, sc, fn)
		}
		if !fn {
			c.unsupportedAt(n, "value-position Anchor call $%s outside a function leaf (Owner decision R3)", n.Ident)
		}
	case *syntax.Name:
		c.checkValueName(n, sc, fn)
	default:
		c.add("InvalidStructuralContext", x, "Goal structure cannot be used as a value")
	}
}

func (c *checker) checkValueName(n *syntax.Name, sc *scope, fn bool) {
	upper, _ := syntax.IsUpper(n.Ident)
	switch n.Suffix {
	case syntax.NoSuffix:
		if sc.has(n.Ident) {
			return
		}
		if _, ok := c.values[n.Ident]; ok {
			c.edge("v:" + n.Ident)
			return
		}
		if c.isGoalName(n.Ident) {
			c.add("UnboundName", n, "%s is a Goal, not a value", n.Ident)
			return
		}
		c.add("UnboundName", n, "unbound name %s", n.Ident)
	case syntax.CallSuffix:
		for _, a := range n.Args {
			c.checkValue(a, sc, fn)
		}
		if !upper {
			c.add("NotCallable", n, "%s is not callable", n.Ident)
			return
		}
		if fn {
			c.add("InvalidStructuralContext", n, "a function leaf body cannot call Goal %s", n.Ident)
			return
		}
		c.goalRef(n, len(n.Args))
		c.staticDestructure(n, sc)
		c.unsupportedAt(n, "eager Goal call %s(...) (Owner decision R3)", n.Ident)
	default:
		if upper {
			c.add("InvalidStructuralContext", n, "deferred Goal %s[...] is not a value", n.Ident)
			return
		}
		if len(n.Args) != 1 {
			c.add("InvalidStructuralContext", n, "map lookup takes exactly one key")
			return
		}
		c.checkValue(n.Args[0], sc, fn)
		if !sc.has(n.Ident) {
			if _, ok := c.values[n.Ident]; !ok {
				c.add("UnboundName", n, "unbound name %s", n.Ident)
				return
			}
			c.edge("v:" + n.Ident)
			c.staticLookup(n)
		}
	}
}

// staticLookup reports statically provable KeyNotFound.
func (c *checker) staticLookup(n *syntax.Name) {
	m, ok := c.values[n.Ident].(*syntax.MapLit)
	if !ok {
		return
	}
	key, ok := literalKey(n.Args[0])
	if !ok {
		return
	}
	for _, en := range m.Entries {
		if en.Key.Kind == syntax.KeyWildcard || expr.StaticKey(en.Key).ID == key {
			return
		}
	}
	c.add("KeyNotFound", n, "%s has no entry for this key", n.Ident)
}

func literalKey(x syntax.Expr) (string, bool) {
	switch l := x.(type) {
	case *syntax.StringLit:
		return "s:" + l.Value, l.EscapeErr == ""
	case *syntax.NumberLit:
		return "n:" + syntax.NormalizeNumberText(l.Text), true
	case *syntax.BoolLit:
		return fmt.Sprintf("b:%t", l.Value), true
	}
	return "", false
}

// staticDestructure reports DestructureMismatch for an eager call whose
// target destructures a statically known argument that lacks a key.
func (c *checker) staticDestructure(n *syntax.Name, sc *scope) {
	def := c.goals[n.Ident][len(n.Args)]
	if def == nil || len(n.Args) != 1 {
		return
	}
	var params syntax.Params
	switch a := def.Arrow.(type) {
	case *syntax.GoalArrow:
		params = a.Params
	case *syntax.FuncArrow:
		params = a.Params
	}
	if !params.Destructure {
		return
	}
	arg := n.Args[0]
	if ref, ok := arg.(*syntax.Name); ok && ref.Suffix == syntax.NoSuffix && !sc.has(ref.Ident) {
		arg = c.values[ref.Ident]
	}
	switch a := arg.(type) {
	case *syntax.MapLit:
		keys := map[string]bool{}
		for _, en := range a.Entries {
			keys[expr.StaticKey(en.Key).ID] = true
		}
		for _, want := range params.Names {
			if !keys["s:"+want] {
				c.add("DestructureMismatch", n, "argument map has no key %q required by %s", want, n.Ident)
				return
			}
		}
	case *syntax.NumberLit, *syntax.StringLit, *syntax.BoolLit:
		c.add("DestructureMismatch", n, "%s destructures a map but receives a non-map literal", n.Ident)
	}
}

func (c *checker) detectCycles() {
	const (
		white = iota
		grey
		black
	)
	color := map[string]int{}
	nodes := make([]string, 0, len(c.edges))
	for k := range c.edges {
		nodes = append(nodes, k)
	}
	sort.Strings(nodes)
	var visit func(string) bool
	visit = func(n string) bool {
		color[n] = grey
		tos := make([]string, 0, len(c.edges[n]))
		for t := range c.edges[n] {
			tos = append(tos, t)
		}
		sort.Strings(tos)
		for _, t := range tos {
			switch color[t] {
			case grey:
				c.errs = append(c.errs, diag.New("CycleDetected", diag.Validation, nil, "cycle through %s and %s", n, t))
				return true
			case white:
				if visit(t) {
					return true
				}
			}
		}
		color[n] = black
		return false
	}
	for _, n := range nodes {
		if color[n] == white && visit(n) {
			return
		}
	}
}
