// Package carousel is the POC Carousel deduction engine.
//
// It owns demand-time alias resolution, atomic deduction commits, occurrence
// and frontier tracking, lineage propagation, and the Touchdown window. It
// never evaluates a grounded leaf and never interprets @policy metadata.
//
// Demand comes only from the caller (the Scheduler). Dependency readiness
// never creates demand here. Prefetch may deduce additional undeduced
// occurrences to keep the requested number of published, unconsumed
// Touchdowns. The conservative value barrier holds: an occurrence whose
// routed input is unresolved never commits a deduction.
package carousel

import (
	"fmt"
	"sort"
	"strings"

	"github.com/on-the-ground/subsea_cable_runtime/codebase"
	"github.com/on-the-ground/subsea_cable_runtime/diag"
	"github.com/on-the-ground/subsea_cable_runtime/expr"
	"github.com/on-the-ground/subsea_cable_runtime/sema"
	"github.com/on-the-ground/subsea_cable_runtime/syntax"
	"github.com/on-the-ground/subsea_cable_runtime/value"
)

// Kind is an occurrence kind.
type Kind string

const (
	KindGoal     Kind = "Goal"
	KindArrow    Kind = "goal-arrow-stage" // POC: inline [p] -> body stage
	KindSerial   Kind = "serial"
	KindParallel Kind = "parallel"
	KindMap      Kind = "resolving-map"
	KindFunction Kind = "function-leaf"
	KindAnchor   Kind = "anchor"
)

// State is the deduction state of an occurrence.
type State string

const (
	Undeduced State = "undeduced"
	Committed State = "committed"
	Failed    State = "failed"
	Withdrawn State = "withdrawn"
)

// PolicyRef is one @policy authored directly on an occurrence.
type PolicyRef struct {
	Ident string
	Args  []value.Value
	Span  diag.Span
}

// Occurrence is one exposed structural node.
type Occurrence struct {
	ID       string
	Kind     Kind
	ParentID string
	Children []string
	Keys     []string // resolving-map keys, parallel to Children
	Deps     []string // direct serial predecessors
	Lineage  string
	Policies []PolicyRef
	State    State
	Seq      int

	// Deducible occurrences (Goal and inline arrow).
	RefKind      string
	Name         string
	Arity        int
	PinnedHash   string
	ArtifactHash string
	GoalNodeID   string
	Revision     int
	Input        string // occurrence whose output is routed in, if any
	argExprs     []syntax.Expr
	env          expr.Env
	arrow        *syntax.GoalArrow
	ctx          *codebase.Artifact

	// Leaves.
	Leaf     string
	Args     []value.Value
	Fn       *syntax.FuncArrow
	FnEnv    expr.Env
	Artifact string

	Failure *diag.Diagnostic
}

// Deducible reports whether the occurrence is resolved by deduction.
func (o *Occurrence) Deducible() bool { return o.Kind == KindGoal || o.Kind == KindArrow }

// IsLeaf reports whether the occurrence is a grounded leaf.
func (o *Occurrence) IsLeaf() bool { return o.Kind == KindFunction || o.Kind == KindAnchor }

// Label is a short human-readable description.
func (o *Occurrence) Label() string {
	switch o.Kind {
	case KindGoal:
		return fmt.Sprintf("%s/%d", o.Name, o.Arity)
	case KindArrow:
		return fmt.Sprintf("[%d] -> ...", o.Arity)
	case KindAnchor:
		return "$" + o.Leaf
	case KindFunction:
		return "fn " + o.Leaf
	}
	return string(o.Kind)
}

// EventKind names a Carousel event.
type EventKind string

const (
	OccurrenceExposed       EventKind = "OccurrenceExposed"
	AliasResolved           EventKind = "AliasResolved"
	DeductionCommitted      EventKind = "DeductionCommitted"
	DeductionFailed         EventKind = "DeductionFailed"
	DeductionBlocked        EventKind = "DeductionBlocked"
	TouchdownPublished      EventKind = "TouchdownPublished"
	TouchdownConsumed       EventKind = "TouchdownConsumed"
	TouchdownDiscarded      EventKind = "TouchdownDiscarded"
	DemandObserved          EventKind = "DemandObserved"
	DemandWithdrawn         EventKind = "DemandWithdrawn"
	PrefetchReconfigured    EventKind = "PrefetchReconfigured"
	PrefetchTargetReached   EventKind = "PrefetchTargetReached"
	PrefetchBlocked         EventKind = "PrefetchBlocked"
	PrefetchExhausted       EventKind = "PrefetchExhausted"
	AtomicPrefetchOvershoot EventKind = "AtomicPrefetchOvershoot"
	// DemandedTouchdownOverTarget: an explicitly demanded deduction published
	// at least one leaf and the window count after it exceeds the target
	// (SCP-0001).
	DemandedTouchdownOverTarget EventKind = "DemandedTouchdownOverTarget"
)

// Event is one Carousel observation.
type Event struct {
	Kind   EventKind
	Occ    string
	Record *codebase.DeductionRecord
	Diag   *diag.Diagnostic
	Reason string
	Count  int
	// Window is the window count after the event (Touchdown events).
	Window int
	// Touchdown acknowledgement identity (SCP-0001).
	EvaluationInstance string
	Attempt            string
}

// ValueSource exposes resolved occurrence outputs (owned by the Runtime).
type ValueSource interface {
	Output(occ string) (value.Output, bool)
}

// Config configures a Carousel.
type Config struct {
	RunID      string
	Codebase   *codebase.Codebase
	Primitives expr.Primitives
	Values     ValueSource
	Prefetch   int
}

// Carousel is single-threaded; the Runtime serializes calls.
type Carousel struct {
	cfg        Config
	occs       map[string]*Occurrence
	order      []*Occurrence
	seq        int
	demanded   map[string]bool
	window     map[string]bool
	touchdowns map[string]*touchdown
	prefetch   int
	events     []Event
	blocked    map[string]string
	lastPrefix string
}

// New creates a Carousel.
func New(cfg Config) *Carousel {
	return &Carousel{cfg: cfg, occs: map[string]*Occurrence{}, demanded: map[string]bool{},
		window: map[string]bool{}, touchdowns: map[string]*touchdown{}, prefetch: cfg.Prefetch, blocked: map[string]string{}}
}

func (c *Carousel) emit(e Event) { c.events = append(c.events, e) }

// Drain returns and clears pending events.
func (c *Carousel) Drain() []Event {
	ev := c.events
	c.events = nil
	return ev
}

// OpenRoot exposes the prepared Root occurrence of a validated unit.
func (c *Carousel) OpenRoot(u *sema.Unit) (*Occurrence, error) {
	r := u.Root
	o := &Occurrence{ID: "r", Kind: KindGoal, State: Undeduced, Name: r.Ident, Arity: len(r.Args),
		RefKind: "unqualified", argExprs: r.Args, env: u.Values, Lineage: fmt.Sprintf("%s/%d", r.Ident, len(r.Args))}
	if r.Hash != "" {
		m := c.cfg.Codebase.ResolvePrefix(r.Ident, r.Hash)
		if len(m) != 1 {
			return nil, diag.New("HashNotFound", diag.Validation, diag.At(r.Span), "Root %s#%s matches %d artifacts", r.Ident, r.Hash, len(m))
		}
		o.RefKind, o.PinnedHash = "hash-qualified", m[0].Hash
	}
	c.register(o)
	return o, nil
}

func (c *Carousel) register(o *Occurrence) {
	c.seq++
	o.Seq = c.seq
	c.occs[o.ID] = o
	c.order = append(c.order, o)
	c.emit(Event{Kind: OccurrenceExposed, Occ: o.ID})
	if o.IsLeaf() {
		c.window[o.ID] = true
		c.touchdowns[o.ID] = &touchdown{}
		c.emit(Event{Kind: TouchdownPublished, Occ: o.ID, Window: c.Window(),
			EvaluationInstance: c.EvaluationInstanceID(o)})
	}
}

// Occurrence returns an exposed occurrence.
func (c *Carousel) Occurrence(id string) *Occurrence { return c.occs[id] }

// Occurrences returns every exposed occurrence in exposure order.
func (c *Carousel) Occurrences() []*Occurrence { return append([]*Occurrence(nil), c.order...) }

// Frontier returns exposed, undeduced, non-withdrawn occurrences.
func (c *Carousel) Frontier() []*Occurrence {
	var out []*Occurrence
	for _, o := range c.order {
		if o.Deducible() && o.State == Undeduced {
			out = append(out, o)
		}
	}
	return out
}

// DemandResult tells the Scheduler what happened to a demand.
type DemandResult string

const (
	DemandAccepted         DemandResult = "accepted"
	DemandAlreadyPending   DemandResult = "already-demanded"
	DemandAlreadyDeduced   DemandResult = "already-committed"
	DemandFailedEarlier    DemandResult = "failed" // the occurrence failed to deduce; see Occurrence.Failure
	DemandWithdrawnEarlier DemandResult = "withdrawn"
	DemandNotDeducible     DemandResult = "not-deducible"
	DemandUnknown          DemandResult = "unknown"
)

// Demand records explicit Scheduler demand for an occurrence. Only an
// undeduced occurrence accepts demand; every other state is reported so the
// Scheduler can act on it (for example, surface a failure that speculative
// prefetch found earlier).
func (c *Carousel) Demand(id string) DemandResult {
	o := c.occs[id]
	switch {
	case o == nil:
		return DemandUnknown
	case !o.Deducible():
		return DemandNotDeducible
	case o.State == Committed:
		return DemandAlreadyDeduced
	case o.State == Failed:
		return DemandFailedEarlier
	case o.State == Withdrawn:
		return DemandWithdrawnEarlier
	case c.demanded[id]:
		return DemandAlreadyPending
	}
	c.demanded[id] = true
	c.emit(Event{Kind: DemandObserved, Occ: id})
	return DemandAccepted
}

// IsDemanded reports whether id carries Scheduler demand.
func (c *Carousel) IsDemanded(id string) bool { return c.demanded[id] }

// Withdraw removes an undeduced occurrence from all future deduction.
// Committed deductions are never affected.
func (c *Carousel) Withdraw(id string) {
	o := c.occs[id]
	if o == nil || !o.Deducible() || o.State != Undeduced {
		return
	}
	o.State = Withdrawn
	delete(c.demanded, id)
	c.emit(Event{Kind: DemandWithdrawn, Occ: id})
}

// SetPrefetch changes the requested number of buffered Touchdowns.
func (c *Carousel) SetPrefetch(n int) {
	if n < 0 {
		n = 0
	}
	c.prefetch = n
	c.lastPrefix = ""
	c.emit(Event{Kind: PrefetchReconfigured, Count: n})
}

// Prefetch returns the requested window target.
func (c *Carousel) Prefetch() int { return c.prefetch }

// Window returns the number of published, unconsumed Touchdowns.
func (c *Carousel) Window() int { return len(c.window) }

// InWindow reports whether a Touchdown is still buffered.
func (c *Carousel) InWindow(id string) bool { return c.window[id] }

// AckResult is the outcome of a Touchdown acknowledgement (SCP-0001).
type AckResult string

const (
	AckConsumed AckResult = "consumed"
	// AckConsumedReplay: the same attempt already consumed this Touchdown.
	// It re-confirms that attempt's authorization and emits no event.
	AckConsumedReplay AckResult = "consumed-replayed"
	// AckAlreadyConsumed: a different attempt consumed it first. It
	// authorizes nothing.
	AckAlreadyConsumed  AckResult = "already-consumed"
	AckInvalidAttempt   AckResult = "invalid-attempt"
	AckDiscarded        AckResult = "discarded"
	AckAlreadyDiscarded AckResult = "already-discarded"
	AckMismatch         AckResult = "mismatch"
	AckUnknown          AckResult = "unknown"
)

type touchdownState int

const (
	tdBuffered touchdownState = iota
	tdConsumed
	tdDiscarded
)

type touchdown struct {
	state      touchdownState
	consumedBy string // attempt that consumed it, when state == tdConsumed
}

// Authorizes reports whether an acknowledgement result lets the Scheduler
// invoke the Host for the attempt that issued it.
func (a AckResult) Authorizes() bool { return a == AckConsumed || a == AckConsumedReplay }

// EvaluationInstanceID derives the evaluation-instance identity of a grounded
// leaf: run, occurrence, and its resolved argument tuple.
func (c *Carousel) EvaluationInstanceID(o *Occurrence) string {
	return fmt.Sprintf("%s/%s/%s", c.cfg.RunID, o.ID, value.Digest(o.Args))
}

// ConsumeTouchdown applies the Scheduler's first-dispatch acknowledgement
// atomically. Only AckConsumed and AckConsumedReplay (see Authorizes) permit
// invoking the Host. The second return value is the attempt that consumed the
// Touchdown, when there is one.
func (c *Carousel) ConsumeTouchdown(occ, evaluationInstance, attempt string) (AckResult, string) {
	o, td := c.occs[occ], c.touchdowns[occ]
	switch {
	case attempt == "":
		return AckInvalidAttempt, ""
	case o == nil || td == nil:
		return AckUnknown, ""
	case evaluationInstance != c.EvaluationInstanceID(o):
		return AckMismatch, ""
	case td.state == tdDiscarded:
		return AckDiscarded, ""
	case td.state == tdConsumed && td.consumedBy == attempt:
		return AckConsumedReplay, attempt
	case td.state == tdConsumed:
		return AckAlreadyConsumed, td.consumedBy
	}
	td.state, td.consumedBy = tdConsumed, attempt
	delete(c.window, occ)
	c.lastPrefix = ""
	c.emit(Event{Kind: TouchdownConsumed, Occ: occ, Window: c.Window(), EvaluationInstance: evaluationInstance, Attempt: attempt})
	return AckConsumed, attempt
}

// DiscardTouchdown applies the Scheduler's acknowledgement that a buffered
// Touchdown will never be attempted.
func (c *Carousel) DiscardTouchdown(occ, reason string) (AckResult, string) {
	o, td := c.occs[occ], c.touchdowns[occ]
	switch {
	case o == nil || td == nil:
		return AckUnknown, ""
	case td.state == tdDiscarded:
		return AckAlreadyDiscarded, ""
	case td.state == tdConsumed:
		return AckAlreadyConsumed, td.consumedBy
	}
	td.state = tdDiscarded
	delete(c.window, occ)
	c.lastPrefix = ""
	c.emit(Event{Kind: TouchdownDiscarded, Occ: occ, Reason: reason, Window: c.Window(), EvaluationInstance: c.EvaluationInstanceID(o)})
	return AckDiscarded, ""
}

// blockReason returns why an undeduced occurrence cannot deduce now.
func (c *Carousel) blockReason(o *Occurrence) string {
	if o.Input == "" {
		return ""
	}
	if _, ok := c.cfg.Values.Output(o.Input); !ok {
		return "pendingValue:" + o.Input
	}
	return ""
}

// Replenish performs one deduction pass: every demanded occurrence that can
// deduce is deduced, then prefetch candidates are deduced until the window
// target is reached or no candidate can progress.
func (c *Carousel) Replenish() []Event {
	for progress := true; progress; {
		progress = false
		for _, o := range c.order {
			if !c.demanded[o.ID] || o.State != Undeduced {
				continue
			}
			if r := c.blockReason(o); r != "" {
				c.reportBlocked(o, r)
				continue
			}
			c.deduce(o, "demand")
			progress = true
		}
	}
	did := false
	for c.Window() < c.prefetch {
		var cand *Occurrence
		var reasons []string
		for _, o := range c.order {
			if !o.Deducible() || o.State != Undeduced || c.demanded[o.ID] {
				continue
			}
			if r := c.blockReason(o); r != "" {
				reasons = append(reasons, o.ID+"="+r)
				continue
			}
			cand = o
			break
		}
		if cand == nil {
			kind := PrefetchExhausted
			reason := "no undeduced candidate"
			if len(reasons) > 0 {
				kind, reason = PrefetchBlocked, strings.Join(reasons, ",")
			}
			key := string(kind) + reason + fmt.Sprint(c.Window())
			if key != c.lastPrefix {
				c.lastPrefix = key
				c.emit(Event{Kind: kind, Reason: reason, Count: c.prefetch - c.Window()})
			}
			return c.Drain()
		}
		before := c.Window()
		if c.deduce(cand, "prefetch") {
			did = true
			if after := c.Window(); before < c.prefetch && after > c.prefetch {
				c.emit(Event{Kind: AtomicPrefetchOvershoot, Occ: cand.ID, Count: after - c.prefetch})
			}
		}
	}
	if did {
		c.lastPrefix = ""
		c.emit(Event{Kind: PrefetchTargetReached, Count: c.Window()})
	}
	return c.Drain()
}

func (c *Carousel) reportBlocked(o *Occurrence, reason string) {
	if c.blocked[o.ID] == reason {
		return
	}
	c.blocked[o.ID] = reason
	c.emit(Event{Kind: DeductionBlocked, Occ: o.ID, Reason: reason})
}

func (c *Carousel) evaluator() *expr.Evaluator {
	return &expr.Evaluator{Prim: c.cfg.Primitives, Phase: diag.Deduction}
}

func (c *Carousel) fail(o *Occurrence, cause string, err error) {
	d, ok := diag.As(err)
	if !ok {
		d = diag.New("DeductionError", diag.Deduction, nil, "%v", err)
	}
	d.Occurrence = o.ID
	if o.Lineage != "" {
		d.Lineages = []string{o.Lineage}
	}
	o.State = Failed
	o.Failure = d
	delete(c.demanded, o.ID)
	c.emit(Event{Kind: DeductionFailed, Occ: o.ID, Diag: d, Reason: cause})
}

// staging collects occurrences created by one deduction so that nothing is
// exposed unless the whole deduction commits.
type staging struct {
	occs []*Occurrence
}

func (s *staging) add(o *Occurrence) *Occurrence {
	s.occs = append(s.occs, o)
	return o
}

func join(lineage, seg string) string {
	if lineage == "" {
		return seg
	}
	return lineage + "." + seg
}

// deduce applies one reduction atomically. It returns true on commit.
func (c *Carousel) deduce(o *Occurrence, cause string) bool {
	ev := c.evaluator()
	st := &staging{}
	rec := codebase.DeductionRecord{RunID: c.cfg.RunID, Occurrence: o.ID, ReferenceKind: o.RefKind, RequestedArity: o.Arity,
		PrimitiveProfile: c.cfg.Primitives.Profile(), Cause: cause}
	var args []value.Value
	if o.Input != "" {
		out, ok := c.cfg.Values.Output(o.Input)
		if !ok {
			c.reportBlocked(o, "pendingValue:"+o.Input)
			return false
		}
		if out.NoOutput {
			c.fail(o, cause, diag.New("InvalidStructuralContext", diag.Deduction, nil, "NoOutput cannot be routed into %s", o.Label()))
			return false
		}
		args = []value.Value{out.Value}
	} else {
		var err error
		args, err = ev.EvalAll(o.argExprs, o.env)
		if err != nil {
			if pe, ok := err.(*expr.PendingError); ok {
				c.reportBlocked(o, "pendingValue:"+pe.Occurrence)
				return false
			}
			c.fail(o, cause, err)
			return false
		}
	}
	for _, a := range args {
		rec.Arguments = append(rec.Arguments, a.Canonical())
	}

	var art *codebase.Artifact
	var bodyChild *Occurrence
	var err error
	switch o.Kind {
	case KindGoal:
		rec.RequestedName = o.Name
		lineage := o.Lineage
		if o.PinnedHash != "" {
			a, ok := c.cfg.Codebase.Get(o.PinnedHash)
			if !ok {
				c.fail(o, cause, diag.New("HashNotFound", diag.Deduction, nil, "pinned artifact %s is missing", o.PinnedHash))
				return false
			}
			art, rec.CodebaseRevision = a, c.cfg.Codebase.Revision()
		} else {
			a, rev, ok := c.cfg.Codebase.ResolveCurrent(o.Name, o.Arity)
			if !ok {
				kind := "GoalNotFound"
				if len(c.cfg.Codebase.Arities(o.Name)) > 0 {
					kind = "ArityMismatch"
				}
				c.fail(o, cause, diag.New(kind, diag.Deduction, nil, "alias %s/%d is not bound at revision %d", o.Name, o.Arity, rev))
				return false
			}
			art, rec.CodebaseRevision = a, rev
		}
		if c.cyclic(o, art.GoalNodeID()) {
			c.fail(o, cause, diag.New("CycleDetected", diag.Deduction, nil, "%s would re-enter its own Goal node", o.Label()))
			return false
		}
		rec.ArtifactHash, rec.GoalNodeID = art.Hash, art.GoalNodeID()
		scope := expr.NewScope(art.Values)
		switch def := art.Def.Arrow.(type) {
		case *syntax.FuncArrow:
			if err = expr.BindParams(def.Params, args, scope, diag.Deduction, def); err == nil {
				bodyChild = st.add(&Occurrence{ID: o.ID + ".d", Kind: KindFunction, ParentID: o.ID, State: Committed,
					Lineage: lineage, Leaf: art.Name, Args: args, Fn: def, FnEnv: scope, Artifact: art.Hash})
			}
		case *syntax.GoalArrow:
			if err = expr.BindParams(def.Params, args, scope, diag.Deduction, def); err == nil {
				bodyChild, err = c.expandBody(st, def.Body, scope, o.ID+".d", o.ID, lineage, art)
			}
		}
	case KindArrow:
		rec.ReferenceKind = "inline-arrow"
		scope := expr.NewScope(o.env)
		if err = expr.BindParams(o.arrow.Params, args, scope, diag.Deduction, o.arrow); err == nil {
			bodyChild, err = c.expandBody(st, o.arrow.Body, scope, o.ID+".d", o.ID, o.Lineage, o.ctx)
		}
	}
	if err != nil {
		c.fail(o, cause, err)
		return false
	}
	rec.Lineages = []string{o.Lineage}
	rec.Result = c.describe(st, bodyChild)
	committed, aerr := c.cfg.Codebase.AppendDeduction(rec)
	if aerr != nil {
		c.fail(o, cause, aerr)
		return false
	}
	o.State = Committed
	o.Children = []string{bodyChild.ID}
	delete(c.demanded, o.ID)
	delete(c.blocked, o.ID)
	if art != nil {
		o.ArtifactHash, o.GoalNodeID, o.Revision = art.Hash, art.GoalNodeID(), committed.CodebaseRevision
		if o.RefKind == "unqualified" {
			c.emit(Event{Kind: AliasResolved, Occ: o.ID, Reason: fmt.Sprintf("%s/%d->%s@rev%d", o.Name, o.Arity, art.Short(), committed.CodebaseRevision)})
		}
	}
	c.emit(Event{Kind: DeductionCommitted, Occ: o.ID, Record: &committed, Reason: cause})
	leaves := 0
	for _, s := range st.occs {
		c.register(s)
		if s.IsLeaf() {
			leaves++
		}
	}
	if cause == "demand" && leaves > 0 && c.Window() > c.prefetch {
		c.emit(Event{Kind: DemandedTouchdownOverTarget, Occ: o.ID, Count: c.prefetch, Window: c.Window()})
	}
	return true
}

func (c *Carousel) cyclic(o *Occurrence, node string) bool {
	for p := c.occs[o.ParentID]; p != nil; p = c.occs[p.ParentID] {
		if p.Kind == KindGoal && p.GoalNodeID == node {
			return true
		}
	}
	return false
}

func (c *Carousel) describe(st *staging, root *Occurrence) string {
	byID := map[string]*Occurrence{}
	for _, s := range st.occs {
		byID[s.ID] = s
	}
	var walk func(o *Occurrence) string
	walk = func(o *Occurrence) string {
		var b strings.Builder
		for _, p := range o.Policies {
			b.WriteString("@" + p.Ident + " ")
		}
		switch o.Kind {
		case KindSerial, KindParallel, KindMap:
			open, close := "[", "]"
			if o.Kind != KindSerial {
				open, close = "{", "}"
			}
			b.WriteString(open)
			for i, ch := range o.Children {
				if i > 0 {
					b.WriteString(", ")
				}
				if o.Kind == KindMap {
					b.WriteString(o.Keys[i] + ": ")
				}
				b.WriteString(walk(byID[ch]))
			}
			b.WriteString(close)
		default:
			b.WriteString(o.Label())
		}
		return b.String()
	}
	return walk(root)
}

func policyTarget(x syntax.Expr) (syntax.Expr, []syntax.Policy) {
	if p, ok := x.(*syntax.Policied); ok {
		return p.Target, p.Policies
	}
	return x, nil
}

func (c *Carousel) policies(ps []syntax.Policy, env expr.Env) ([]PolicyRef, error) {
	var out []PolicyRef
	for _, p := range ps {
		args, err := c.evaluator().EvalAll(p.Args, env)
		if err != nil {
			return nil, err
		}
		out = append(out, PolicyRef{Ident: p.Ident, Args: args, Span: p.Span})
	}
	return out, nil
}

func (c *Carousel) expandBody(st *staging, body syntax.Expr, env expr.Env, id, parent, lineage string, art *codebase.Artifact) (*Occurrence, error) {
	target, pols := policyTarget(body)
	m, ok := target.(*syntax.MapLit)
	if !ok {
		return c.expand(st, body, env, "", id, parent, lineage, art)
	}
	refs, err := c.policies(pols, env)
	if err != nil {
		return nil, err
	}
	occ := &Occurrence{ID: id, Kind: KindMap, ParentID: parent, State: Committed, Lineage: lineage, Policies: refs}
	for i, en := range m.Entries {
		ch, err := c.expand(st, en.Value, env, "", fmt.Sprintf("%s.%d", id, i), id, lineage, art)
		if err != nil {
			return nil, err
		}
		occ.Children = append(occ.Children, ch.ID)
		occ.Keys = append(occ.Keys, expr.StaticKey(en.Key).Display)
	}
	st.occs = append([]*Occurrence{occ}, st.occs...)
	return occ, nil
}

func isBareGoal(x syntax.Expr) bool {
	x, _ = policyTarget(x)
	n, ok := x.(*syntax.Name)
	if !ok || n.Suffix != syntax.NoSuffix {
		return false
	}
	up, _ := syntax.IsUpper(n.Ident)
	return up
}

// expand materializes one structural expression. in names the occurrence
// whose output is offered to this stage ("" for none).
func (c *Carousel) expand(st *staging, x syntax.Expr, env expr.Env, in, id, parent, lineage string, art *codebase.Artifact) (*Occurrence, error) {
	target, pols := policyTarget(x)
	refs, err := c.policies(pols, env)
	if err != nil {
		return nil, err
	}
	var occ *Occurrence
	switch n := target.(type) {
	case *syntax.Name:
		upper, _ := syntax.IsUpper(n.Ident)
		switch {
		case upper && n.Suffix == syntax.CallSuffix:
			return nil, diag.New("UnsupportedByProfile", diag.Profile, diag.At(n.Span),
				"eager Goal call %s(...) needs staging that is not specified yet (Owner decision R3)", n.Ident)
		case upper:
			occ = &Occurrence{ID: id, Kind: KindGoal, ParentID: parent, State: Undeduced, Name: n.Ident,
				RefKind: "unqualified", env: env, ctx: art}
			if n.Suffix == syntax.BracketSuffix {
				occ.Arity, occ.argExprs = len(n.Args), n.Args
			} else if in != "" {
				occ.Arity, occ.Input = 1, in
			}
			if n.Hash != "" {
				occ.RefKind = "hash-qualified"
				if art != nil {
					occ.PinnedHash = art.Pinned[n]
				}
			}
		case n.Suffix == syntax.BracketSuffix:
			return nil, diag.New("UnsupportedByProfile", diag.Profile, diag.At(n.Span),
				"structure-valued lookup %s[...] is blocked pending a language decision (runtime ADR 0003)", n.Ident)
		default:
			return nil, diag.New("InvalidStructuralContext", diag.Deduction, diag.At(n.Span), "%s is not Goal structure", n.Ident)
		}
	case *syntax.Anchor:
		args, err := c.evaluator().EvalAll(n.Args, env)
		if err != nil {
			return nil, err
		}
		occ = &Occurrence{ID: id, Kind: KindAnchor, ParentID: parent, State: Committed, Leaf: n.Ident, Args: args}
		if art != nil {
			occ.Artifact = art.Hash
		}
	case *syntax.GoalArrow:
		occ = &Occurrence{ID: id, Kind: KindArrow, ParentID: parent, State: Undeduced, RefKind: "inline-arrow",
			Arity: n.Params.Arity(), arrow: n, env: env, ctx: art}
		if occ.Arity == 1 {
			occ.Input = in
		}
	case *syntax.Serial:
		occ = &Occurrence{ID: id, Kind: KindSerial, ParentID: parent, State: Committed}
		st.add(occ)
		prev := ""
		for i, s := range n.Stages {
			ch, err := c.expand(st, s, env, prev, fmt.Sprintf("%s.%d", id, i), id, lineage, art)
			if err != nil {
				return nil, err
			}
			if prev != "" {
				ch.Deps = []string{prev}
			}
			occ.Children = append(occ.Children, ch.ID)
			prev = ch.ID
		}
		occ.Lineage, occ.Policies = lineage, refs
		return occ, nil
	case *syntax.Parallel:
		occ = &Occurrence{ID: id, Kind: KindParallel, ParentID: parent, State: Committed}
		st.add(occ)
		fan := ""
		allBare := true
		for _, s := range n.Stages {
			allBare = allBare && isBareGoal(s)
		}
		if allBare {
			fan = in
		}
		for i, s := range n.Stages {
			ch, err := c.expand(st, s, env, fan, fmt.Sprintf("%s.%d", id, i), id, lineage, art)
			if err != nil {
				return nil, err
			}
			occ.Children = append(occ.Children, ch.ID)
		}
		occ.Lineage, occ.Policies = lineage, refs
		return occ, nil
	default:
		return nil, diag.New("InvalidStructuralContext", diag.Deduction, diag.At(x.Pos()), "%T is not Goal structure", target)
	}
	occ.Lineage, occ.Policies = lineage, refs
	if occ.Kind == KindGoal {
		occ.Lineage = join(lineage, fmt.Sprintf("%s/%d", occ.Name, occ.Arity))
	}
	st.add(occ)
	return occ, nil
}

// SortedIDs is a helper for deterministic iteration in callers and tests.
func SortedIDs(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
