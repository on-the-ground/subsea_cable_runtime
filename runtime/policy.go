package runtime

import (
	"fmt"

	"github.com/on-the-ground/subsea_cable_runtime/carousel"
	"github.com/on-the-ground/subsea_cable_runtime/diag"
	"github.com/on-the-ground/subsea_cable_runtime/host"
	"github.com/on-the-ground/subsea_cable_runtime/value"
)

// PolicyEventKind is an event delivered to a policy interpreter. An
// interpreter receives only events of the occurrence its policy directly
// targets.
type PolicyEventKind string

const (
	ScopeOpened          PolicyEventKind = "ScopeOpened"
	ChildScopeOutcome    PolicyEventKind = "ChildScopeOutcome" // composite targets only
	BeforeAttempt        PolicyEventKind = "BeforeAttempt"     // leaf targets only
	AttemptOutcome       PolicyEventKind = "AttemptOutcome"    // leaf targets only
	TimerFired           PolicyEventKind = "TimerFired"
	CancelRequested      PolicyEventKind = "CancelRequested"
	ScopeOutcomeProposed PolicyEventKind = "ScopeOutcomeProposed" // baseline outcome about to be finalized
)

// PolicyEvent is one delivery.
type PolicyEvent struct {
	Kind       PolicyEventKind
	Child      string
	ChildState ScopeState
	Outcome    *host.Outcome
	Attempt    int
	Token      string
	Proposed   ScopeState
}

// PolicyContext is the read-only view an interpreter gets.
type PolicyContext struct {
	Occurrence *carousel.Occurrence
	Args       []value.Value
	Now        int
	Attempts   int
}

// ActionKind is the closed set of policy actions.
type ActionKind string

const (
	Admit       ActionKind = "Admit"
	Hold        ActionKind = "Hold"        // leaf targets only in this POC
	StartTimer  ActionKind = "StartTimer"  // virtual ticks
	Reattempt   ActionKind = "Reattempt"   // leaf targets only (composite reattempt: Owner decision R4)
	CancelScope ActionKind = "CancelScope" // cancel in-flight work and withdraw undeduced demand under the target
	FailScope   ActionKind = "FailScope"
	Emit        ActionKind = "Emit"
)

// Action is one interpreter decision. No action can change topology,
// routing, aliases, or committed deductions.
type Action struct {
	Kind   ActionKind
	Ticks  int
	Token  string
	Diag   *diag.Diagnostic
	Fields map[string]string
}

// Interpreter implements one @policy identifier.
type Interpreter interface {
	ID() string
	Version() string
	Targets() []carousel.Kind
	Validate(args []value.Value) error
	Attach(ctx PolicyContext) any
	On(ev PolicyEvent, state any, ctx PolicyContext) []Action
}

// Registry maps policy identifiers to interpreters. It is empty by default:
// every authored policy is rejected until an interpreter is registered.
type Registry struct {
	interps map[string]Interpreter
	pairs   map[[2]string]bool
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{interps: map[string]Interpreter{}, pairs: map[[2]string]bool{}}
}

// Register adds an interpreter.
func (r *Registry) Register(i Interpreter) *Registry {
	r.interps[i.ID()] = i
	return r
}

// AllowPair declares that `@first @second` may be stacked on one target.
// Events are then delivered in source order and actions are concatenated.
// This composition rule is POC-only and is not language semantics.
func (r *Registry) AllowPair(first, second string) *Registry {
	r.pairs[[2]string{first, second}] = true
	return r
}

type boundPolicy struct {
	ref    carousel.PolicyRef
	interp Interpreter
	state  any
}

func (r *Registry) bind(o *carousel.Occurrence, now int) ([]*boundPolicy, *diag.Diagnostic) {
	var out []*boundPolicy
	for i, ref := range o.Policies {
		interp, ok := r.interps[ref.Ident]
		if !ok {
			return nil, policyDiag("UnknownPolicy", o, ref, "no interpreter is registered for @%s", ref.Ident)
		}
		supported := false
		for _, k := range interp.Targets() {
			supported = supported || k == o.Kind
		}
		if !supported {
			return nil, policyDiag("UnsupportedPolicyTarget", o, ref, "@%s does not support %s occurrences", ref.Ident, o.Kind)
		}
		if err := interp.Validate(ref.Args); err != nil {
			return nil, policyDiag("InvalidPolicyArguments", o, ref, "@%s: %v", ref.Ident, err)
		}
		if i > 0 && !r.pairs[[2]string{o.Policies[i-1].Ident, ref.Ident}] {
			return nil, policyDiag("PolicyConflict", o, ref, "no declared composition for @%s followed by @%s", o.Policies[i-1].Ident, ref.Ident)
		}
		b := &boundPolicy{ref: ref, interp: interp}
		b.state = interp.Attach(PolicyContext{Occurrence: o, Args: ref.Args, Now: now})
		out = append(out, b)
	}
	return out, nil
}

func policyDiag(kind string, o *carousel.Occurrence, ref carousel.PolicyRef, format string, args ...any) *diag.Diagnostic {
	d := diag.New(kind, diag.Policy, diag.At(ref.Span), format, args...)
	d.Occurrence = o.ID
	if o.Lineage != "" {
		d.Lineages = []string{o.Lineage}
	}
	d.Details = map[string]any{"policy": ref.Ident, "targetKind": string(o.Kind)}
	return d
}

func (b *boundPolicy) String() string { return fmt.Sprintf("@%s/%s", b.ref.Ident, b.interp.Version()) }
