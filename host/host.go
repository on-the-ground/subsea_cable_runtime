// Package host defines the Host Port used by the POC Runtime and a scripted,
// deterministic Host for tests and the CLI.
package host

import (
	"fmt"
	"sort"
	"strings"

	"github.com/on-the-ground/subsea_cable_runtime/diag"
	"github.com/on-the-ground/subsea_cable_runtime/expr"
	"github.com/on-the-ground/subsea_cable_runtime/syntax"
	"github.com/on-the-ground/subsea_cable_runtime/value"
)

// LeafContext is the grounded leaf envelope subset a Host needs.
type LeafContext struct {
	RunID      string
	Occurrence string
	Artifact   string
	Leaf       string // function Goal name or $Anchor identifier
	Lineages   []string
	AttemptID  string
	AttemptNo  int
	ArgsDigest string
}

// Status is a Host outcome class.
type Status int

const (
	Succeeded Status = iota
	Failed
	Cancelled
)

func (s Status) String() string {
	return [...]string{"Succeeded", "Failed", "Cancelled"}[s]
}

// Outcome is a typed Host result. Ticks is the virtual duration the POC
// Runtime waits before delivering the outcome (deterministic concurrency).
type Outcome struct {
	Status Status
	Output value.Output
	Diag   *diag.Diagnostic
	Ticks  int
}

// Host is the Host Port.
type Host interface {
	Primitives() expr.Primitives
	EvaluateFunction(ctx LeafContext, fn *syntax.FuncArrow, env expr.Env, gateway expr.AnchorCaller) Outcome
	InvokeAnchor(ctx LeafContext, name string, args []value.Value) Outcome
	Cancel(attemptID string)
}

// Call records one Anchor invocation.
type Call struct {
	Ctx    LeafContext
	Name   string
	Args   []value.Value
	Nested bool
}

// AnchorFunc implements one Anchor in the scripted Host.
type AnchorFunc func(c Call) (value.Output, error)

// Scripted is a deterministic recording Host.
type Scripted struct {
	Prim      expr.Primitives
	anchors   map[string]AnchorFunc
	failures  map[string]int
	ticks     map[string]int
	Fallback  AnchorFunc // used for unregistered anchors when non-nil
	Calls     []Call
	Functions []LeafContext
	Cancelled []string
}

// NewScripted returns a Host with the poc-rational/0 primitive profile.
func NewScripted() *Scripted {
	return &Scripted{Prim: RationalProfile{}, anchors: map[string]AnchorFunc{}, failures: map[string]int{}, ticks: map[string]int{}}
}

// Register installs an Anchor implementation.
func (h *Scripted) Register(name string, fn AnchorFunc) { h.anchors[name] = fn }

// FailNext makes the next n direct invocations of leaf fail.
func (h *Scripted) FailNext(leaf string, n int) { h.failures[leaf] = n }

// SetTicks sets the virtual duration of a leaf (function Goal name or Anchor).
func (h *Scripted) SetTicks(leaf string, n int) { h.ticks[leaf] = n }

func (h *Scripted) Primitives() expr.Primitives { return h.Prim }

func (h *Scripted) duration(leaf string) int {
	if t, ok := h.ticks[leaf]; ok {
		return t
	}
	return 1
}

func (h *Scripted) injected(ctx LeafContext) *Outcome {
	if h.failures[ctx.Leaf] > 0 {
		h.failures[ctx.Leaf]--
		return &Outcome{Status: Failed, Ticks: h.duration(ctx.Leaf),
			Diag: diag.New("InjectedFailure", diag.Host, nil, "scripted failure of %s (attempt %d)", ctx.Leaf, ctx.AttemptNo)}
	}
	return nil
}

// EvaluateFunction evaluates a validated function-leaf body.
func (h *Scripted) EvaluateFunction(ctx LeafContext, fn *syntax.FuncArrow, env expr.Env, gateway expr.AnchorCaller) Outcome {
	h.Functions = append(h.Functions, ctx)
	if o := h.injected(ctx); o != nil {
		return *o
	}
	ev := &expr.Evaluator{Prim: h.Prim, Anchors: gateway, Phase: diag.Host}
	var last value.Value
	for _, x := range fn.Body {
		v, err := ev.Eval(x, env)
		if err != nil {
			d, ok := diag.As(err)
			if !ok {
				d = diag.New("HostError", diag.Host, nil, "%v", err)
			}
			return Outcome{Status: Failed, Diag: d, Ticks: h.duration(ctx.Leaf)}
		}
		last = v
	}
	return Outcome{Status: Succeeded, Output: value.Of(last), Ticks: h.duration(ctx.Leaf)}
}

// InvokeAnchor resolves and invokes an Anchor.
func (h *Scripted) InvokeAnchor(ctx LeafContext, name string, args []value.Value) Outcome {
	nested := ctx.Leaf != name
	h.Calls = append(h.Calls, Call{Ctx: ctx, Name: name, Args: args, Nested: nested})
	if !nested {
		if o := h.injected(ctx); o != nil {
			return *o
		}
	}
	fn, ok := h.anchors[name]
	if !ok {
		fn = h.Fallback
	}
	if fn == nil {
		return Outcome{Status: Failed, Ticks: 1, Diag: diag.New("AnchorNotFound", diag.Host, nil, "no implementation for $%s", name)}
	}
	out, err := fn(Call{Ctx: ctx, Name: name, Args: args, Nested: nested})
	if err != nil {
		d, ok := diag.As(err)
		if !ok {
			d = diag.New("AnchorFailed", diag.Host, nil, "%v", err)
		}
		return Outcome{Status: Failed, Diag: d, Ticks: h.duration(name)}
	}
	return Outcome{Status: Succeeded, Output: out, Ticks: h.duration(name)}
}

// Cancel records a cooperative cancellation request.
func (h *Scripted) Cancel(attemptID string) { h.Cancelled = append(h.Cancelled, attemptID) }

// Echo is a fallback Anchor returning "name(arg, ...)" as a String.
func Echo(c Call) (value.Output, error) {
	parts := make([]string, len(c.Args))
	for i, a := range c.Args {
		if a.Kind() == value.KString {
			parts[i] = a.AsString()
		} else {
			parts[i] = a.String()
		}
	}
	return value.Of(value.String(fmt.Sprintf("%s(%s)", c.Name, strings.Join(parts, ", ")))), nil
}

// CallNames lists recorded direct and nested Anchor names in order.
func (h *Scripted) CallNames() []string {
	out := make([]string, len(h.Calls))
	for i, c := range h.Calls {
		out[i] = c.Name
	}
	return out
}

// SortedAnchors lists registered anchors (for CLI help).
func (h *Scripted) SortedAnchors() []string {
	out := make([]string, 0, len(h.anchors))
	for k := range h.anchors {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
