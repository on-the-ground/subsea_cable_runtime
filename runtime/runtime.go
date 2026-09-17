// Package runtime is the POC Subsea Cable Runtime: it coordinates the
// Carousel, the Host, and @policy interpreters from the Program's prepared
// Root occurrence to the Root outcome.
//
// Execution is a single serialized reactor with a virtual clock, so every run
// is deterministic. See carousel/docs/POC_PROFILE.md for every profile choice
// that is not language semantics.
package runtime

import (
	"container/heap"
	"fmt"
	"sort"
	"strings"

	"github.com/on-the-ground/subsea_cable_runtime/carousel"
	"github.com/on-the-ground/subsea_cable_runtime/codebase"
	"github.com/on-the-ground/subsea_cable_runtime/diag"
	"github.com/on-the-ground/subsea_cable_runtime/expr"
	"github.com/on-the-ground/subsea_cable_runtime/host"
	"github.com/on-the-ground/subsea_cable_runtime/sema"
	"github.com/on-the-ground/subsea_cable_runtime/syntax"
	"github.com/on-the-ground/subsea_cable_runtime/value"
)

// SchedulerProfile names the POC baseline Scheduler. It is not language
// semantics.
const SchedulerProfile = "poc-baseline/0"

// ConsumePoint selects when a Touchdown leaves the window. The portable
// choice is Owner decision R10 (Carousel decision 6); this knob only lets the
// POC show the difference.
type ConsumePoint string

const (
	ConsumeAtDispatch   ConsumePoint = "dispatch"
	ConsumeAtCompletion ConsumePoint = "completion"
)

// DemandMode selects how the baseline Scheduler issues demand.
type DemandMode string

const (
	// DemandReady: the Scheduler explicitly demands every exposed, undeduced
	// occurrence whose structural predecessors are satisfied. This is a
	// Scheduler profile decision, not a rule the Carousel applies by itself.
	DemandReady DemandMode = "ready"
	// DemandManual: only the Root is demanded automatically; callers issue
	// further demand with Run.Demand.
	DemandManual DemandMode = "manual"
)

// Config configures a run.
type Config struct {
	RunID       string
	Prefetch    int
	Concurrency int
	ConsumeAt   ConsumePoint
	Demand      DemandMode
	Policies    *Registry
	Host        host.Host
	Codebase    *codebase.Codebase
	MaxSteps    int
}

func (c *Config) defaults() {
	if c.Concurrency <= 0 {
		c.Concurrency = 4
	}
	if c.ConsumeAt == "" {
		c.ConsumeAt = ConsumeAtDispatch
	}
	if c.Demand == "" {
		c.Demand = DemandReady
	}
	if c.Policies == nil {
		c.Policies = NewRegistry()
	}
	if c.Codebase == nil {
		c.Codebase = codebase.New()
	}
	if c.MaxSteps <= 0 {
		c.MaxSteps = 100000
	}
}

// ScopeState is the runtime state of an exposed occurrence.
type ScopeState string

const (
	ScopeUndeduced ScopeState = "Undeduced"
	ScopeOpen      ScopeState = "Open"
	ScopeSatisfied ScopeState = "Satisfied"
	ScopeFailed    ScopeState = "Failed"
	ScopeCancelled ScopeState = "Cancelled"
	ScopeWithdrawn ScopeState = "Withdrawn"
)

func (s ScopeState) terminal() bool {
	return s == ScopeSatisfied || s == ScopeFailed || s == ScopeCancelled || s == ScopeWithdrawn
}

// LeafPhase refines an Open leaf scope.
type LeafPhase string

const (
	LeafGrounded LeafPhase = "Grounded"
	LeafWaiting  LeafPhase = "Waiting"
	LeafEligible LeafPhase = "Eligible"
	LeafInFlight LeafPhase = "InFlight"
)

// Scope is created only when an occurrence is exposed.
type Scope struct {
	ID          string
	State       ScopeState
	Leaf        LeafPhase
	WaitReasons []string
	Output      value.Output
	Diag        *diag.Diagnostic
	Attempts    int
	notBefore   int
	policies    []*boundPolicy
	opened      bool
	// deferred holds a deduction failure found by speculative prefetch. It is
	// surfaced only when the Scheduler would demand the occurrence, so that
	// prefetch does not change which outcomes a run observes.
	deferred *diag.Diagnostic
}

// Result is the run outcome.
type Result struct {
	Status host.Status
	Output value.Output
	Diag   *diag.Diagnostic
	Steps  int
	Time   int
}

type attempt struct {
	id        string
	scope     string
	no        int
	cancelled bool
}

type timelineItem struct {
	at      int
	seq     int
	attempt string
	outcome host.Outcome
	scope   string
	token   string
	kind    string // completion | timer | backoff
}

type timeline []*timelineItem

func (t timeline) Len() int { return len(t) }
func (t timeline) Less(i, j int) bool {
	if t[i].at != t[j].at {
		return t[i].at < t[j].at
	}
	return t[i].seq < t[j].seq
}
func (t timeline) Swap(i, j int) { t[i], t[j] = t[j], t[i] }
func (t *timeline) Push(x any)   { *t = append(*t, x.(*timelineItem)) }
func (t *timeline) Pop() any {
	old := *t
	it := old[len(old)-1]
	*t = old[:len(old)-1]
	return it
}

// Run is one Program execution.
type Run struct {
	cfg       Config
	car       *carousel.Carousel
	unit      *sema.Unit
	scopes    map[string]*Scope
	values    map[string]value.Output
	trace     *Trace
	now       int
	tl        timeline
	tlSeq     int
	inflight  map[string]*attempt
	result    *Result
	steps     int
	callSites int
}

// values implements carousel.ValueSource (the Outcome & Value Store).
type valueStore struct{ r *Run }

func (v valueStore) Output(occ string) (value.Output, bool) {
	o, ok := v.r.values[occ]
	return o, ok
}

// Start validates nothing itself: it requires a validated unit, commits it to
// the Codebase, and prepares the Program's Root occurrence as initial demand.
func Start(cfg Config, unit *sema.Unit) (*Run, error) {
	cfg.defaults()
	if cfg.Host == nil {
		return nil, fmt.Errorf("runtime: a Host is required")
	}
	if len(unit.Unsupported) > 0 {
		return nil, unit.Unsupported
	}
	runID, err := cfg.Codebase.RegisterRun(cfg.RunID)
	if err != nil {
		return nil, err
	}
	cfg.RunID = runID
	r := &Run{cfg: cfg, unit: unit, scopes: map[string]*Scope{}, values: map[string]value.Output{},
		trace: &Trace{}, inflight: map[string]*attempt{}}
	arts, rev, err := cfg.Codebase.CommitUnit(unit)
	if err != nil {
		return nil, err
	}
	r.trace.add(r.now, "SourceValidated", "", fmt.Sprintf("goals=%d", len(unit.Goals)))
	for _, a := range arts {
		r.trace.add(r.now, "ArtifactCommitted", "", fmt.Sprintf("%s/%d=%s@rev%d", a.Name, a.Arity, a.Short(), rev))
	}
	r.car = carousel.New(carousel.Config{RunID: cfg.RunID, Codebase: cfg.Codebase, Primitives: cfg.Host.Primitives(),
		Values: valueStore{r}, Prefetch: cfg.Prefetch})
	root, err := r.car.OpenRoot(unit)
	if err != nil {
		return nil, err
	}
	r.trace.add(r.now, "RunRequested", root.ID, fmt.Sprintf("root=%s prefetch=%d scheduler=%s consumeAt=%s demand=%s primitives=%s",
		root.Label(), cfg.Prefetch, SchedulerProfile, cfg.ConsumeAt, cfg.Demand, cfg.Host.Primitives().Profile()))
	r.handle(r.car.Drain())
	r.demand(root.ID)
	return r, nil
}

// ID returns the run identity.
func (r *Run) ID() string { return r.cfg.RunID }

// Ledger returns this run's deduction records.
func (r *Run) Ledger() []codebase.DeductionRecord { return r.cfg.Codebase.RunLedger(r.cfg.RunID) }

// Carousel exposes the engine for inspection.
func (r *Run) Carousel() *carousel.Carousel { return r.car }

// Trace returns the normalized trace.
func (r *Run) Trace() *Trace { return r.trace }

// Scope returns the runtime scope of an exposed occurrence.
func (r *Run) Scope(id string) *Scope { return r.scopes[id] }

// Result returns the outcome once the run has finished.
func (r *Run) Result() *Result { return r.result }

// Now returns the virtual clock.
func (r *Run) Now() int { return r.now }

// Demand issues explicit Scheduler demand (DemandManual).
func (r *Run) Demand(id string) carousel.DemandResult { return r.demand(id) }

// demand is the single path through which the Scheduler demands work. A
// failure that speculative prefetch found earlier is surfaced here, and only
// here, because only now does the Scheduler actually require the occurrence.
func (r *Run) demand(id string) carousel.DemandResult {
	res := r.car.Demand(id)
	if res == carousel.DemandFailedEarlier {
		if s := r.scopes[id]; s != nil && s.deferred != nil && !s.State.terminal() {
			r.trace.add(r.now, "DeductionFailureSurfaced", id, s.deferred.Kind)
			r.settle(s, ScopeFailed, value.None, s.deferred, "deduction")
		}
	}
	return res
}

// SetPrefetch reconfigures the Touchdown window target.
func (r *Run) SetPrefetch(n int) { r.car.SetPrefetch(n) }

// Cancel cancels the whole run.
func (r *Run) Cancel(reason string) {
	if r.result != nil {
		return
	}
	root := r.scopes["r"]
	r.settle(root, ScopeCancelled, value.None, diag.New("RunCancelled", diag.Policy, nil, "%s", reason), "cancel")
}

// Advance performs demand, deduction, and dispatch without delivering any
// timeline event (completions, timers). It never declares a run stuck, which
// lets callers issue manual demand between passes.
func (r *Run) Advance() {
	r.stabilize()
}

// RunToCompletion steps until the run finishes.
func (r *Run) RunToCompletion() *Result {
	for r.Step() {
	}
	return r.result
}

// Step performs one reactor iteration. It returns false once finished.
func (r *Run) Step() bool {
	if r.result != nil {
		return false
	}
	r.steps++
	if r.steps > r.cfg.MaxSteps {
		r.finish(host.Failed, value.None, diag.New("StepBudgetExceeded", diag.Policy, nil, "run exceeded %d steps", r.cfg.MaxSteps))
		return false
	}
	// Before time advances, keep demanding, deducing, and dispatching until
	// nothing changes. Dispatch can consume Touchdowns, so the prefetch window
	// must be refilled before the next completion is delivered.
	r.stabilize()
	if r.result != nil {
		return false
	}
	if r.tl.Len() > 0 {
		it := heap.Pop(&r.tl).(*timelineItem)
		if it.at > r.now {
			r.now = it.at
		}
		r.deliver(it)
		return r.result == nil
	}
	if r.result == nil {
		reasons := r.blockingReasons()
		d := diag.New("RunStuck", diag.Policy, nil, "no work can progress")
		d.Details = map[string]any{"blocking": reasons}
		r.trace.add(r.now, "RunStuck", "", strings.Join(reasons, "; "))
		r.finish(host.Failed, value.None, d)
	}
	return r.result == nil
}

func (r *Run) finish(st host.Status, out value.Output, d *diag.Diagnostic) {
	if r.result != nil {
		return
	}
	r.result = &Result{Status: st, Output: out, Diag: d, Steps: r.steps, Time: r.now}
	detail := st.String()
	if st == host.Succeeded {
		detail += " " + out.String()
	}
	r.trace.addDiag(r.now, "RunCompleted", "r", detail, d)
}

// ---- demand and deduction ----

// stabilize alternates demand/deduction and dispatch until a pass changes
// nothing observable.
func (r *Run) stabilize() {
	for i := 0; i < 10000 && r.result == nil; i++ {
		before := len(r.trace.Events)
		r.pump()
		if r.result != nil {
			return
		}
		r.dispatch()
		if len(r.trace.Events) == before {
			return
		}
	}
}

func (r *Run) pump() {
	for i := 0; i < 10000 && r.result == nil; i++ {
		demanded := r.schedulerDemand()
		evs := r.car.Replenish()
		r.handle(evs)
		if len(evs) == 0 && !demanded {
			return
		}
	}
}

// schedulerDemand is the baseline Scheduler's explicit demand decision.
func (r *Run) schedulerDemand() bool {
	if r.cfg.Demand != DemandReady {
		return false
	}
	any := false
	for _, o := range r.car.Occurrences() {
		if !o.Deducible() || r.car.IsDemanded(o.ID) {
			continue
		}
		s := r.scopes[o.ID]
		if s == nil || s.State.terminal() {
			continue
		}
		pendingFailure := o.State == carousel.Failed && s.deferred != nil
		if o.State != carousel.Undeduced && !pendingFailure {
			continue
		}
		if ok, _ := r.structurallyReady(o); ok {
			if res := r.demand(o.ID); res == carousel.DemandAccepted || res == carousel.DemandFailedEarlier {
				any = true
			}
		}
	}
	return any
}

func (r *Run) parent(o *carousel.Occurrence) *carousel.Occurrence {
	return r.car.Occurrence(o.ParentID)
}

// structurallyReady reports whether every serial predecessor of o and of its
// ancestors is satisfied and no ancestor is terminal.
func (r *Run) structurallyReady(o *carousel.Occurrence) (bool, []string) {
	var reasons []string
	for cur := o; cur != nil; cur = r.parent(cur) {
		for _, d := range cur.Deps {
			if s := r.scopes[d]; s == nil || s.State != ScopeSatisfied {
				reasons = append(reasons, "dependsOn:"+d)
			}
		}
		if cur != o {
			if s := r.scopes[cur.ID]; s != nil && s.State.terminal() {
				reasons = append(reasons, "ancestorSettled:"+cur.ID)
			}
		}
	}
	return len(reasons) == 0, reasons
}

func (r *Run) ensureScope(id string) *Scope {
	if s := r.scopes[id]; s != nil {
		return s
	}
	o := r.car.Occurrence(id)
	s := &Scope{ID: id, State: ScopeOpen}
	if o.Deducible() {
		s.State = ScopeUndeduced
	}
	if o.IsLeaf() {
		s.Leaf = LeafGrounded
	}
	r.scopes[id] = s
	return s
}

func (r *Run) handle(evs []carousel.Event) {
	for _, e := range evs {
		detail := e.Reason
		if e.Count != 0 {
			detail = strings.TrimSpace(fmt.Sprintf("%s count=%d", detail, e.Count))
		}
		switch e.Kind {
		case carousel.OccurrenceExposed:
			o := r.car.Occurrence(e.Occ)
			r.trace.add(r.now, string(e.Kind), e.Occ, describeOcc(o))
			s := r.ensureScope(e.Occ)
			if s.State.terminal() {
				continue
			}
			if len(o.Policies) > 0 {
				bound, d := r.cfg.Policies.bind(o, r.now)
				if d != nil {
					r.trace.addDiag(r.now, "PolicyRejected", o.ID, d.Kind, d)
					r.settle(s, ScopeFailed, value.None, d, "policy")
					continue
				}
				s.policies = bound
				for _, b := range bound {
					r.trace.add(r.now, "PolicyAttached", o.ID, b.String())
				}
			}
			if s.State == ScopeOpen {
				r.open(s)
			}
		case carousel.DeductionCommitted:
			r.trace.addRecord(r.now, string(e.Kind), e.Occ, e.Reason, e.Record)
			if s := r.scopes[e.Occ]; s != nil && !s.State.terminal() {
				s.State = ScopeOpen
				r.open(s)
			}
		case carousel.DeductionFailed:
			r.trace.addDiag(r.now, string(e.Kind), e.Occ, e.Diag.Kind+" cause="+e.Reason, e.Diag)
			s := r.scopes[e.Occ]
			if s == nil {
				continue
			}
			if e.Reason == "prefetch" {
				// Speculative work the Scheduler has not demanded never fails
				// the run by itself; the failure waits for explicit demand.
				s.deferred = e.Diag
				r.trace.add(r.now, "DeductionFailureDeferred", e.Occ, "surfaced when demanded")
				continue
			}
			r.settle(s, ScopeFailed, value.None, e.Diag, "deduction")
		case carousel.DemandWithdrawn:
			r.trace.add(r.now, string(e.Kind), e.Occ, detail)
			if s := r.scopes[e.Occ]; s != nil && !s.State.terminal() {
				s.State = ScopeWithdrawn
			}
		default:
			r.trace.add(r.now, string(e.Kind), e.Occ, detail)
		}
	}
}

func describeOcc(o *carousel.Occurrence) string {
	var b strings.Builder
	b.WriteString(string(o.Kind) + " " + o.Label())
	if o.Lineage != "" {
		b.WriteString(" lineage=" + o.Lineage)
	}
	if len(o.Deps) > 0 {
		b.WriteString(" deps=" + strings.Join(o.Deps, ","))
	}
	if o.Input != "" {
		b.WriteString(" input=" + o.Input)
	}
	for _, p := range o.Policies {
		b.WriteString(" @" + p.Ident)
	}
	if o.IsLeaf() {
		args := make([]string, len(o.Args))
		for i, a := range o.Args {
			args[i] = a.String()
		}
		b.WriteString(" args=(" + strings.Join(args, ", ") + ")")
	}
	return b.String()
}

func (r *Run) open(s *Scope) {
	if s.opened {
		return
	}
	s.opened = true
	r.apply(s, r.notify(s, PolicyEvent{Kind: ScopeOpened}))
}

// ---- policies ----

func (r *Run) notify(s *Scope, ev PolicyEvent) []Action {
	if len(s.policies) == 0 {
		return nil
	}
	o := r.car.Occurrence(s.ID)
	var all []Action
	for _, b := range s.policies {
		ctx := PolicyContext{Occurrence: o, Args: b.ref.Args, Now: r.now, Attempts: s.Attempts}
		acts := b.interp.On(ev, b.state, ctx)
		for _, a := range acts {
			r.trace.add(r.now, "PolicyAction", s.ID, fmt.Sprintf("%s on %s -> %s", b, ev.Kind, a.Kind))
		}
		all = append(all, acts...)
	}
	return all
}

// apply executes non-attempt actions and reports whether the scope settled.
func (r *Run) apply(s *Scope, acts []Action) bool {
	o := r.car.Occurrence(s.ID)
	for _, a := range acts {
		if s.State.terminal() {
			return true
		}
		switch a.Kind {
		case Admit, Emit:
		case StartTimer:
			r.push(&timelineItem{at: r.now + a.Ticks, kind: "timer", scope: s.ID, token: a.Token})
			r.trace.add(r.now, "TimerStarted", s.ID, fmt.Sprintf("token=%s due=%d", a.Token, r.now+a.Ticks))
		case Hold:
			if !o.IsLeaf() {
				r.rejectAction(s, a, "Hold is only supported on leaf targets in this POC")
				return true
			}
			if r.now+a.Ticks > s.notBefore {
				s.notBefore = r.now + a.Ticks
				r.push(&timelineItem{at: s.notBefore, kind: "backoff", scope: s.ID})
			}
		case Reattempt:
			if !o.IsLeaf() {
				r.rejectAction(s, a, "Reattempt of a composite scope is undefined (Owner decision R4)")
				return true
			}
		case CancelScope:
			d := a.Diag
			if d == nil {
				d = diag.New("CancelledByPolicy", diag.Policy, nil, "scope cancelled by policy")
			}
			r.settle(s, ScopeCancelled, value.None, d, "policy")
			return true
		case FailScope:
			d := a.Diag
			if d == nil {
				d = diag.New("FailedByPolicy", diag.Policy, nil, "scope failed by policy")
			}
			d.Occurrence = s.ID
			r.settle(s, ScopeFailed, value.None, d, "policy")
			return true
		default:
			r.rejectAction(s, a, "unknown action")
			return true
		}
	}
	return false
}

func (r *Run) rejectAction(s *Scope, a Action, msg string) {
	d := diag.New("UnsupportedPolicyTarget", diag.Policy, nil, "%s: %s", a.Kind, msg)
	d.Occurrence = s.ID
	r.trace.addDiag(r.now, "PolicyRejected", s.ID, d.Kind, d)
	r.settle(s, ScopeFailed, value.None, d, "policy")
}

// ---- evaluation ----

func (r *Run) push(it *timelineItem) {
	r.tlSeq++
	it.seq = r.tlSeq
	heap.Push(&r.tl, it)
}

func (r *Run) leafScopes() []*Scope {
	var out []*Scope
	for _, o := range r.car.Occurrences() {
		if !o.IsLeaf() {
			continue
		}
		if s := r.scopes[o.ID]; s != nil && s.State == ScopeOpen && s.Leaf != LeafInFlight {
			out = append(out, s)
		}
	}
	return out
}

func (r *Run) dispatch() {
	for _, s := range r.leafScopes() {
		o := r.car.Occurrence(s.ID)
		ok, reasons := r.structurallyReady(o)
		if s.notBefore > r.now {
			ok = false
			reasons = append(reasons, fmt.Sprintf("notBefore:%d", s.notBefore))
		}
		if !ok {
			if s.Leaf != LeafWaiting || strings.Join(s.WaitReasons, ",") != strings.Join(reasons, ",") {
				s.Leaf, s.WaitReasons = LeafWaiting, reasons
				r.trace.add(r.now, "EvaluationWaiting", s.ID, strings.Join(reasons, ","))
			}
			continue
		}
		if s.Leaf != LeafEligible {
			s.Leaf, s.WaitReasons = LeafEligible, nil
			r.trace.add(r.now, "EvaluationEligible", s.ID, "")
		}
		if len(r.inflight) >= r.cfg.Concurrency {
			continue
		}
		if r.apply(s, r.notify(s, PolicyEvent{Kind: BeforeAttempt, Attempt: s.Attempts + 1})) {
			continue
		}
		if s.notBefore > r.now {
			continue // held by a policy
		}
		r.start(s, o)
		if r.result != nil {
			return
		}
	}
}

func (r *Run) start(s *Scope, o *carousel.Occurrence) {
	s.Attempts++
	at := &attempt{id: fmt.Sprintf("%s/%s#%d", r.cfg.RunID, o.ID, s.Attempts), scope: s.ID, no: s.Attempts}
	r.inflight[at.id] = at
	s.Leaf = LeafInFlight
	ctx := host.LeafContext{RunID: r.cfg.RunID, Occurrence: o.ID, Artifact: o.Artifact, Leaf: o.Leaf,
		Lineages: []string{o.Lineage}, AttemptID: at.id, AttemptNo: at.no, ArgsDigest: value.Digest(o.Args)}
	if r.cfg.ConsumeAt == ConsumeAtDispatch {
		r.car.Consume(o.ID)
		r.handle(r.car.Drain())
	}
	r.trace.add(r.now, "EvaluationStarted", o.ID, fmt.Sprintf("attempt=%s leaf=%s", at.id, o.Label()))
	var out host.Outcome
	if o.Kind == carousel.KindFunction {
		out = r.cfg.Host.EvaluateFunction(ctx, o.Fn, o.FnEnv, r.gateway(ctx))
	} else {
		out = r.cfg.Host.InvokeAnchor(ctx, o.Leaf, o.Args)
	}
	if out.Ticks < 0 {
		out.Ticks = 0
	}
	r.push(&timelineItem{at: r.now + out.Ticks, kind: "completion", attempt: at.id, outcome: out, scope: s.ID})
}

// gateway routes nested $Anchor calls from a function-leaf body through the
// Runtime so they are traced. They are not occurrences and carry no policy.
func (r *Run) gateway(ctx host.LeafContext) expr.AnchorCaller {
	return func(call *syntax.Anchor, args []value.Value) (value.Output, error) {
		r.callSites++
		site := fmt.Sprintf("%s@%s#%d", ctx.Occurrence, call.Span, r.callSites)
		r.trace.add(r.now, "AnchorGatewayCall", ctx.Occurrence, fmt.Sprintf("site=%s $%s", site, call.Ident))
		out := r.cfg.Host.InvokeAnchor(ctx, call.Ident, args)
		if out.Status != host.Succeeded {
			if out.Diag != nil {
				return value.Output{}, out.Diag
			}
			return value.Output{}, diag.New("AnchorFailed", diag.Host, diag.At(call.Span), "$%s did not succeed", call.Ident)
		}
		return out.Output, nil
	}
}

func (r *Run) deliver(it *timelineItem) {
	switch it.kind {
	case "timer":
		s := r.scopes[it.scope]
		if s == nil || s.State.terminal() {
			r.trace.add(r.now, "TimerIgnored", it.scope, it.token)
			return
		}
		r.trace.add(r.now, "TimerFired", it.scope, it.token)
		r.apply(s, r.notify(s, PolicyEvent{Kind: TimerFired, Token: it.token}))
	case "completion":
		at := r.inflight[it.attempt]
		delete(r.inflight, it.attempt)
		if at == nil || at.cancelled {
			r.trace.add(r.now, "LateCompletionIgnored", it.scope, fmt.Sprintf("attempt=%s status=%s", it.attempt, it.outcome.Status))
			return
		}
		s := r.scopes[it.scope]
		out := it.outcome
		kind := "Evaluation" + out.Status.String()
		detail := fmt.Sprintf("attempt=%s", at.id)
		if out.Status == host.Succeeded {
			detail += " value=" + out.Output.String()
		}
		r.trace.addDiag(r.now, kind, s.ID, detail, out.Diag)
		acts := r.notify(s, PolicyEvent{Kind: AttemptOutcome, Outcome: &out, Attempt: at.no})
		for _, a := range acts {
			if a.Kind == Reattempt && out.Status == host.Failed {
				s.Leaf = LeafWaiting
				s.notBefore = r.now + a.Ticks
				r.trace.add(r.now, "ReattemptScheduled", s.ID, fmt.Sprintf("next=%d notBefore=%d", s.Attempts+1, s.notBefore))
				if a.Ticks > 0 {
					r.push(&timelineItem{at: s.notBefore, kind: "backoff", scope: s.ID})
				}
				return
			}
		}
		if r.apply(s, acts) {
			return
		}
		switch out.Status {
		case host.Succeeded:
			r.settle(s, ScopeSatisfied, out.Output, nil, "attempt")
		case host.Cancelled:
			r.settle(s, ScopeCancelled, value.None, out.Diag, "attempt")
		default:
			d := out.Diag
			if d == nil {
				d = diag.New("LeafFailed", diag.Host, nil, "leaf failed")
			}
			d.Occurrence = s.ID
			r.settle(s, ScopeFailed, value.None, d, "attempt")
		}
	case "backoff":
		r.trace.add(r.now, "BackoffElapsed", it.scope, "")
	}
}

// ---- outcomes ----

func (r *Run) settle(s *Scope, st ScopeState, out value.Output, d *diag.Diagnostic, rule string) {
	if s == nil || s.State.terminal() {
		return
	}
	o := r.car.Occurrence(s.ID)
	s.State, s.Output, s.Diag = st, out, d
	if o.IsLeaf() {
		if st == ScopeSatisfied {
			r.car.Consume(o.ID)
		} else {
			r.car.Discard(o.ID, string(st))
		}
		r.handle(r.car.Drain())
	}
	if o.Deducible() && r.car.Occurrence(o.ID).State == carousel.Undeduced {
		r.car.Withdraw(o.ID)
	}
	detail := fmt.Sprintf("%s rule=%s", st, rule)
	if st == ScopeSatisfied {
		r.values[s.ID] = out
		detail += " output=" + out.String()
	}
	r.trace.addDiag(r.now, "ScopeOutcome", s.ID, detail, d)
	if st == ScopeSatisfied {
		r.trace.add(r.now, "ValueResolved", s.ID, out.String())
	} else {
		r.cancelSubtree(o)
	}
	p := r.parent(o)
	if p == nil {
		switch st {
		case ScopeSatisfied:
			r.finish(host.Succeeded, out, nil)
		case ScopeCancelled:
			r.finish(host.Cancelled, value.None, d)
		default:
			r.finish(host.Failed, value.None, d)
		}
		return
	}
	ps := r.ensureScope(p.ID)
	if ps.State.terminal() {
		return
	}
	if r.apply(ps, r.notify(ps, PolicyEvent{Kind: ChildScopeOutcome, Child: s.ID, ChildState: st})) {
		return
	}
	r.evaluateParent(ps, p)
}

func (r *Run) cancelSubtree(o *carousel.Occurrence) {
	for _, cid := range o.Children {
		c := r.car.Occurrence(cid)
		cs := r.ensureScope(cid)
		if !cs.State.terminal() {
			if c.Deducible() && c.State == carousel.Undeduced {
				r.car.Withdraw(cid)
				cs.State = ScopeWithdrawn
				r.handle(r.car.Drain())
			} else {
				for id, at := range r.inflight {
					if at.scope == cid && !at.cancelled {
						at.cancelled = true
						r.cfg.Host.Cancel(id)
						r.trace.add(r.now, "EvaluationCancelRequested", cid, id)
					}
				}
				r.notify(cs, PolicyEvent{Kind: CancelRequested})
				cs.State = ScopeCancelled
				if c.IsLeaf() {
					r.car.Discard(cid, "cancelled")
					r.handle(r.car.Drain())
				}
				r.trace.add(r.now, "ScopeOutcome", cid, "Cancelled rule=ancestor")
			}
		}
		r.cancelSubtree(c)
	}
}

func (r *Run) evaluateParent(ps *Scope, p *carousel.Occurrence) {
	var proposed ScopeState
	var out value.Output
	var d *diag.Diagnostic
	children := make([]*Scope, len(p.Children))
	for i, id := range p.Children {
		children[i] = r.ensureScope(id)
	}
	firstBad := func() *Scope {
		for _, c := range children {
			if c.State == ScopeFailed || c.State == ScopeCancelled || c.State == ScopeWithdrawn {
				return c
			}
		}
		return nil
	}
	allSatisfied := func() bool {
		for _, c := range children {
			if c.State != ScopeSatisfied {
				return false
			}
		}
		return true
	}
	switch p.Kind {
	case carousel.KindGoal, carousel.KindArrow:
		c := children[0]
		if !c.State.terminal() {
			return
		}
		proposed, out, d = c.State, c.Output, c.Diag
		if proposed == ScopeWithdrawn {
			proposed = ScopeFailed
		}
	case carousel.KindSerial:
		if bad := firstBad(); bad != nil {
			proposed, d = ScopeFailed, bad.Diag
		} else if last := children[len(children)-1]; last.State == ScopeSatisfied {
			proposed, out = ScopeSatisfied, last.Output
		} else {
			return
		}
	case carousel.KindParallel:
		if bad := firstBad(); bad != nil {
			proposed, d = ScopeFailed, bad.Diag
		} else if allSatisfied() {
			proposed, out = ScopeSatisfied, value.None
		} else {
			return
		}
	case carousel.KindMap:
		if bad := firstBad(); bad != nil {
			proposed, d = ScopeFailed, bad.Diag
		} else if allSatisfied() {
			m := value.NewMap()
			for i, c := range children {
				if c.Output.NoOutput {
					proposed = ScopeFailed
					d = diag.New("NoOutputBranch", diag.Host, nil, "resolving-map branch %s exported NoOutput", p.Keys[i])
					break
				}
				m = m.With(value.StringKey(p.Keys[i]), c.Output.Value)
			}
			if proposed == "" {
				proposed, out = ScopeSatisfied, value.Of(value.MapValue(m))
			}
		} else {
			return
		}
	default:
		return
	}
	if r.apply(ps, r.notify(ps, PolicyEvent{Kind: ScopeOutcomeProposed, Proposed: proposed})) {
		return
	}
	if d == nil && proposed != ScopeSatisfied {
		d = diag.New("ChildFailed", diag.Policy, nil, "a child scope did not succeed")
	}
	r.settle(ps, proposed, out, d, "baseline")
}

func (r *Run) blockingReasons() []string {
	var out []string
	for _, o := range r.car.Occurrences() {
		s := r.scopes[o.ID]
		if s == nil || s.State.terminal() {
			continue
		}
		switch {
		case o.Deducible() && o.State == carousel.Undeduced:
			tag := "undemanded"
			if r.car.IsDemanded(o.ID) {
				tag = "demanded"
			}
			reason := tag
			if o.Input != "" {
				if _, ok := r.values[o.Input]; !ok {
					reason += ",pendingValue:" + o.Input
				}
			}
			if _, rs := r.structurallyReady(o); len(rs) > 0 {
				reason += "," + strings.Join(rs, ",")
			}
			out = append(out, o.ID+"="+reason)
		case o.Deducible() && o.State == carousel.Failed && s.deferred != nil:
			out = append(out, o.ID+"=undemanded,deferredFailure:"+s.deferred.Kind)
		case o.IsLeaf():
			out = append(out, fmt.Sprintf("%s=%s[%s]", o.ID, s.Leaf, strings.Join(s.WaitReasons, ",")))
		}
	}
	sort.Strings(out)
	return out
}
