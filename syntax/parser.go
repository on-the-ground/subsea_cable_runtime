package syntax

import (
	"strconv"
	"strings"

	"github.com/on-the-ground/subsea_cable_runtime/diag"
)

// Parse decodes, preprocesses, and parses one source unit.
func Parse(src []byte) (*Program, error) {
	toks, err := Tokenize(src)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks}
	return p.program()
}

type parser struct {
	toks []Token
	pos  int
}

type syntaxError struct{ d *diag.Diagnostic }

func (p *parser) peek() Token        { return p.toks[p.pos] }
func (p *parser) peekAt(n int) Token { return p.toks[min(p.pos+n, len(p.toks)-1)] }
func (p *parser) next() Token {
	t := p.toks[p.pos]
	if p.pos < len(p.toks)-1 {
		p.pos++
	}
	return t
}

func (p *parser) fail(format string, args ...any) {
	t := p.peek()
	d := diag.New("SyntaxError", diag.Source, diag.At(t.Span), format, args...)
	d.Details = map[string]any{"found": t.String()}
	panic(syntaxError{d})
}

func (p *parser) expect(punct string) Token {
	if !p.peek().Is(punct) {
		p.fail("expected %q, found %s", punct, p.peek())
	}
	return p.next()
}

func (p *parser) accept(punct string) bool {
	if p.peek().Is(punct) {
		p.next()
		return true
	}
	return false
}

func (p *parser) program() (prog *Program, err error) {
	defer func() {
		if r := recover(); r != nil {
			se, ok := r.(syntaxError)
			if !ok {
				panic(r)
			}
			prog, err = nil, se.d
		}
	}()
	prog = &Program{}
	if p.peek().Kind == Terminator {
		p.next()
	}
	for {
		p.statement(prog)
		if p.peek().Kind == Terminator {
			p.next()
			if p.peek().Kind == EOF {
				break
			}
			continue
		}
		if p.peek().Kind == EOF {
			break
		}
		p.fail("expected newline or end of input, found %s", p.peek())
	}
	return prog, nil
}

func (p *parser) statement(prog *Program) {
	t := p.peek()
	if t.Kind == Ident && p.peekAt(1).Is("=") {
		p.next()
		p.next()
		var rhs Expr
		if p.peek().Is("(") && p.arrowAfterGroup("(", ")") {
			rhs = p.funcArrow()
		} else {
			rhs = p.expression()
		}
		prog.Bindings = append(prog.Bindings, &Binding{Name: t.Text, Value: rhs, Span: t.Span})
		return
	}
	prog.Roots = append(prog.Roots, p.expression())
}

// arrowAfterGroup reports whether the delimited group starting at the current
// token is immediately followed by `->`.
func (p *parser) arrowAfterGroup(open, close string) bool {
	depth := 0
	for i := p.pos; i < len(p.toks); i++ {
		t := p.toks[i]
		if t.Kind == EOF {
			return false
		}
		if t.Kind != Punct {
			continue
		}
		switch t.Text {
		case "(", "[", "{":
			depth++
		case ")", "]", "}":
			depth--
			if depth == 0 {
				return i+1 < len(p.toks) && p.toks[i+1].Is("->")
			}
		}
	}
	return false
}

func (p *parser) params(open, close string) Params {
	start := p.expect(open)
	ps := Params{Span: start.Span}
	if p.accept(close) {
		return ps
	}
	if p.peek().Is("{") {
		p.next()
		ps.Destructure = true
		for !p.peek().Is("}") {
			ps.Names = append(ps.Names, p.ident())
			if !p.accept(",") {
				break
			}
		}
		p.expect("}")
		p.expect(close)
		return ps
	}
	for {
		ps.Names = append(ps.Names, p.ident())
		if !p.accept(",") || p.peek().Is(close) {
			break
		}
	}
	p.expect(close)
	return ps
}

func (p *parser) ident() string {
	t := p.peek()
	if t.Kind != Ident {
		p.fail("expected identifier, found %s", t)
	}
	p.next()
	return t.Text
}

func (p *parser) funcArrow() *FuncArrow {
	span := p.peek().Span
	params := p.params("(", ")")
	p.expect("->")
	fa := &FuncArrow{Params: params, Span: span}
	if p.peek().Is("{") && p.hasTopLevelSemicolon() {
		p.next()
		fa.Body = append(fa.Body, p.expression())
		p.expect(";")
		fa.Body = append(fa.Body, p.expression())
		for p.accept(";") {
			if p.peek().Is("}") {
				break
			}
			fa.Body = append(fa.Body, p.expression())
		}
		p.expect("}")
		return fa
	}
	fa.Body = []Expr{p.expression()}
	return fa
}

func (p *parser) hasTopLevelSemicolon() bool {
	depth := 0
	for i := p.pos; i < len(p.toks); i++ {
		t := p.toks[i]
		if t.Kind == EOF {
			return false
		}
		if t.Kind != Punct {
			continue
		}
		switch t.Text {
		case "(", "[", "{":
			depth++
		case ")", "]", "}":
			depth--
			if depth == 0 {
				return false
			}
		case ";":
			if depth == 1 {
				return true
			}
		}
	}
	return false
}

func (p *parser) expression() Expr {
	if p.peek().Is("[") && p.arrowAfterGroup("[", "]") {
		return p.goalArrow()
	}
	return p.logicalOr()
}

func (p *parser) goalArrow() *GoalArrow {
	span := p.peek().Span
	params := p.params("[", "]")
	p.expect("->")
	return &GoalArrow{Params: params, Body: p.goalBody(), Span: span}
}

func (p *parser) policies() []Policy {
	var out []Policy
	for p.peek().Is("@") {
		at := p.next()
		pol := Policy{Ident: p.ident(), Span: at.Span}
		if p.peek().Is("(") {
			pol.HasArgs = true
			pol.Args = p.argList("(", ")")
		}
		out = append(out, pol)
	}
	return out
}

func wrapPolicies(pols []Policy, target Expr) Expr {
	if len(pols) == 0 {
		return target
	}
	return &Policied{Policies: pols, Target: target, Span: pols[0].Span}
}

func (p *parser) goalBody() Expr {
	pols := p.policies()
	t := p.peek()
	var core Expr
	switch {
	case t.Kind == Ident:
		n := p.goalName()
		if n.Suffix == NoSuffix {
			p.fail("a Goal-arrow body reference requires [...] or (...)")
		}
		core = n
	case t.Is("$"):
		core = p.anchor()
	case t.Is("["):
		if p.arrowAfterGroup("[", "]") {
			core = p.goalArrow()
		} else {
			core = p.serial()
		}
	case t.Is("{"):
		if p.braceIsMap() {
			core = p.mapLit(true)
		} else {
			core = p.parallel()
		}
	default:
		p.fail("expected Goal structure, found %s", t)
	}
	return wrapPolicies(pols, core)
}

func (p *parser) goalStage() Expr {
	pols := p.policies()
	t := p.peek()
	var core Expr
	switch {
	case t.Kind == Ident:
		core = p.goalName()
	case t.Is("$"):
		core = p.anchor()
	case t.Is("["):
		if p.arrowAfterGroup("[", "]") {
			core = p.goalArrow()
		} else {
			core = p.serial()
		}
	case t.Is("{"):
		if p.braceIsMap() {
			p.fail("a map cannot be a composition stage; wrap it in a Goal arrow")
		}
		core = p.parallel()
	default:
		p.fail("expected a Goal stage, found %s", t)
	}
	return wrapPolicies(pols, core)
}

// braceIsMap decides between a map and an unkeyed parallel composition.
func (p *parser) braceIsMap() bool {
	a, b := p.peekAt(1), p.peekAt(2)
	if a.Is("}") {
		return true
	}
	switch {
	case a.Is("-") && b.Kind == Number:
		return p.peekAt(3).Is(":")
	case a.Kind == Ident || a.Kind == String || a.Kind == Number || a.Kind == Bool || a.Kind == Wildcard:
		return b.Is(":")
	}
	return false
}

func (p *parser) goalName() *Name {
	t := p.next()
	if t.Kind != Ident {
		p.pos--
		p.fail("expected identifier, found %s", t)
	}
	n := &Name{Ident: t.Text, Span: t.Span}
	if p.peek().Kind == HashQual {
		n.Hash = p.next().Text
	}
	switch {
	case p.peek().Is("["):
		n.Suffix = BracketSuffix
		n.Args = p.argList("[", "]")
	case p.peek().Is("("):
		n.Suffix = CallSuffix
		n.Args = p.argList("(", ")")
	}
	return n
}

func (p *parser) anchor() *Anchor {
	d := p.expect("$")
	a := &Anchor{Ident: p.ident(), Span: d.Span}
	if p.peek().Is("(") {
		a.Called = true
		a.Args = p.argList("(", ")")
	}
	return a
}

func (p *parser) argList(open, close string) []Expr {
	p.expect(open)
	var out []Expr
	for !p.peek().Is(close) {
		out = append(out, p.expression())
		if !p.accept(",") {
			break
		}
	}
	p.expect(close)
	return out
}

func (p *parser) serial() *Serial {
	start := p.expect("[")
	s := &Serial{Span: start.Span}
	s.Stages = append(s.Stages, p.goalStage())
	p.expect(",")
	s.Stages = append(s.Stages, p.goalStage())
	for p.accept(",") {
		if p.peek().Is("]") {
			break
		}
		s.Stages = append(s.Stages, p.goalStage())
	}
	p.expect("]")
	return s
}

func (p *parser) parallel() *Parallel {
	start := p.expect("{")
	s := &Parallel{Span: start.Span}
	s.Stages = append(s.Stages, p.goalStage())
	p.expect(",")
	s.Stages = append(s.Stages, p.goalStage())
	for p.accept(",") {
		if p.peek().Is("}") {
			break
		}
		s.Stages = append(s.Stages, p.goalStage())
	}
	p.expect("}")
	return s
}

func (p *parser) mapKey() MapKey {
	t := p.peek()
	switch {
	case t.Is("-"):
		p.next()
		n := p.peek()
		if n.Kind != Number {
			p.fail("expected number after '-' in map key")
		}
		p.next()
		return MapKey{Kind: KeyNumber, Text: "-" + n.Text, Span: t.Span}
	case t.Kind == Number:
		p.next()
		return MapKey{Kind: KeyNumber, Text: t.Text, Span: t.Span}
	case t.Kind == Bool:
		p.next()
		return MapKey{Kind: KeyBool, Text: t.Text, Span: t.Span}
	case t.Kind == Wildcard:
		p.next()
		return MapKey{Kind: KeyWildcard, Text: "_", Span: t.Span}
	case t.Kind == String:
		p.next()
		s := decodeString(t)
		return MapKey{Kind: KeyString, Text: s.Value, Span: t.Span}
	case t.Kind == Ident:
		p.next()
		return MapKey{Kind: KeyIdent, Text: t.Text, Span: t.Span}
	}
	p.fail("expected map key, found %s", t)
	return MapKey{}
}

// mapLit parses a map; resolving selects goal-stage values.
func (p *parser) mapLit(resolving bool) *MapLit {
	start := p.expect("{")
	m := &MapLit{Span: start.Span}
	for !p.peek().Is("}") {
		k := p.mapKey()
		p.expect(":")
		var v Expr
		if resolving {
			v = p.goalStage()
		} else {
			v = p.expression()
		}
		m.Entries = append(m.Entries, MapEntry{Key: k, Value: v})
		if !p.accept(",") {
			break
		}
	}
	p.expect("}")
	return m
}

func (p *parser) logicalOr() Expr {
	x := p.logicalAnd()
	for p.peek().Is("||") {
		op := p.next()
		x = &Binary{Op: "||", L: x, R: p.logicalAnd(), Span: op.Span}
	}
	return x
}

func (p *parser) logicalAnd() Expr {
	x := p.equality()
	for p.peek().Is("&&") {
		op := p.next()
		x = &Binary{Op: "&&", L: x, R: p.equality(), Span: op.Span}
	}
	return x
}

func (p *parser) equality() Expr {
	x := p.comparison()
	if t := p.peek(); t.Is("==") || t.Is("!=") {
		p.next()
		x = &Binary{Op: t.Text, L: x, R: p.comparison(), Span: t.Span}
	}
	return x
}

func (p *parser) comparison() Expr {
	x := p.additive()
	if t := p.peek(); t.Is("<=") || t.Is(">=") || t.Is("<") || t.Is(">") {
		p.next()
		x = &Binary{Op: t.Text, L: x, R: p.additive(), Span: t.Span}
	}
	return x
}

func (p *parser) additive() Expr {
	x := p.multiplicative()
	for t := p.peek(); t.Is("+") || t.Is("-"); t = p.peek() {
		p.next()
		x = &Binary{Op: t.Text, L: x, R: p.multiplicative(), Span: t.Span}
	}
	return x
}

func (p *parser) multiplicative() Expr {
	x := p.unary()
	for t := p.peek(); t.Is("*") || t.Is("/") || t.Is("%"); t = p.peek() {
		p.next()
		x = &Binary{Op: t.Text, L: x, R: p.unary(), Span: t.Span}
	}
	return x
}

func (p *parser) unary() Expr {
	t := p.peek()
	switch {
	case t.Is("!") || t.Is("-"):
		p.next()
		return &Unary{Op: t.Text, X: p.unary(), Span: t.Span}
	case t.Is("@"):
		pols := p.policies()
		var target Expr
		if p.peek().Is("$") {
			target = p.anchor()
		} else {
			n := p.goalName()
			if n.Suffix == NoSuffix {
				p.fail("a policy in value position must target a suffixed Goal or an Anchor")
			}
			target = n
		}
		return wrapPolicies(pols, target)
	}
	return p.postfix()
}

func (p *parser) postfix() Expr {
	t := p.peek()
	switch {
	case t.Kind == Ident:
		return p.goalName()
	case t.Is("$"):
		return p.anchor()
	case t.Is("["):
		return p.serial()
	case t.Is("{"):
		if p.braceIsMap() {
			return p.mapLit(false)
		}
		return p.parallel()
	case t.Is("("):
		p.next()
		x := p.expression()
		p.expect(")")
		return &Group{X: x, Span: t.Span}
	case t.Kind == Number:
		p.next()
		return &NumberLit{Text: t.Text, Span: t.Span}
	case t.Kind == Bool:
		p.next()
		return &BoolLit{Value: t.Text == "true", Span: t.Span}
	case t.Kind == String:
		p.next()
		return decodeString(t)
	}
	p.fail("expected expression, found %s", t)
	return nil
}

func decodeString(t Token) *StringLit {
	raw := t.Text[1 : len(t.Text)-1]
	s := &StringLit{Raw: t.Text, Span: t.Span}
	var b strings.Builder
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if c != '\\' {
			b.WriteByte(c)
			continue
		}
		i++
		switch raw[i] {
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case '"', '\\':
			b.WriteByte(raw[i])
		case 'u':
			end := strings.IndexByte(raw[i:], '}') + i
			hex := raw[i+2 : end]
			v, _ := strconv.ParseUint(hex, 16, 32)
			if v > 0x10FFFF || (v >= 0xD800 && v <= 0xDFFF) {
				if s.EscapeErr == "" {
					s.EscapeErr = `\u{` + hex + `}`
				}
			} else {
				b.WriteRune(rune(v))
			}
			i = end
		}
	}
	s.Value = b.String()
	return s
}
