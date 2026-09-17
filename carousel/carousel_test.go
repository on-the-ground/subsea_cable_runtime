package carousel_test

import (
	"strings"
	"testing"

	"github.com/on-the-ground/subsea_cable_runtime/carousel"
	"github.com/on-the-ground/subsea_cable_runtime/codebase"
	"github.com/on-the-ground/subsea_cable_runtime/expr"
	"github.com/on-the-ground/subsea_cable_runtime/host"
	"github.com/on-the-ground/subsea_cable_runtime/sema"
	"github.com/on-the-ground/subsea_cable_runtime/value"
)

type values map[string]value.Output

func (v values) Output(id string) (value.Output, bool) { o, ok := v[id]; return o, ok }

type countingPrims struct {
	expr.Primitives
	calls int
}

func (c *countingPrims) Binary(op string, l, r value.Value) (value.Value, error) {
	c.calls++
	return c.Primitives.Binary(op, l, r)
}

type fixture struct {
	t    *testing.T
	cb   *codebase.Codebase
	vals values
	prim *countingPrims
	car  *carousel.Carousel
	evs  []carousel.Event
}

func unit(t *testing.T, cb *codebase.Codebase, src string) *sema.Unit {
	t.Helper()
	u, err := sema.Check([]byte(src), cb)
	if err != nil {
		t.Fatalf("invalid test program: %v\n%s", err, src)
	}
	return u
}

// store commits src (which must have its own Root) and returns name/arity.
func store(t *testing.T, cb *codebase.Codebase, src, name string, arity int) *codebase.Artifact {
	t.Helper()
	if _, _, err := cb.CommitUnit(unit(t, cb, src)); err != nil {
		t.Fatal(err)
	}
	a, _, ok := cb.ResolveCurrent(name, arity)
	if !ok {
		t.Fatalf("%s/%d not stored", name, arity)
	}
	return a
}

func open(t *testing.T, cb *codebase.Codebase, src string, prefetch int) *fixture {
	t.Helper()
	u := unit(t, cb, src)
	if _, _, err := cb.CommitUnit(u); err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, cb: cb, vals: values{}, prim: &countingPrims{Primitives: host.RationalProfile{}}}
	f.car = carousel.New(carousel.Config{RunID: "t", Codebase: cb, Primitives: f.prim, Values: f.vals, Prefetch: prefetch})
	if _, err := f.car.OpenRoot(u); err != nil {
		t.Fatal(err)
	}
	f.car.Demand("r")
	f.step()
	return f
}

func (f *fixture) step() []carousel.Event {
	ev := f.car.Replenish()
	f.evs = append(f.evs, ev...)
	return ev
}

func (f *fixture) consume(id string) carousel.AckResult {
	f.t.Helper()
	o := f.occ(id)
	res, _ := f.car.ConsumeTouchdown(id, f.car.EvaluationInstanceID(o), "attempt-"+id)
	return res
}

func (f *fixture) occ(id string) *carousel.Occurrence {
	f.t.Helper()
	o := f.car.Occurrence(id)
	if o == nil {
		f.t.Fatalf("occurrence %s not exposed; have %v", id, f.ids())
	}
	return o
}

func (f *fixture) ids() []string {
	var out []string
	for _, o := range f.car.Occurrences() {
		out = append(out, o.ID)
	}
	return out
}

func (f *fixture) record(id string) codebase.DeductionRecord {
	f.t.Helper()
	r, ok := f.cb.GetDeduction("t", id)
	if !ok {
		f.t.Fatalf("no deduction record for %s", id)
	}
	return r
}

func count(evs []carousel.Event, k carousel.EventKind) int {
	n := 0
	for _, e := range evs {
		if e.Kind == k {
			n++
		}
	}
	return n
}

// ---- conformance/DEDUCTION.md ----

// Scenario 1: a parent deduction does not resolve its child.
func TestParentDeductionDoesNotResolveChild(t *testing.T) {
	cb := codebase.New()
	store(t, cb, "B = [] -> $b1\nB[]", "B", 0)
	f := open(t, cb, "Upper = [] -> B[]\nUpper[]", 0)
	child := f.occ("r.d")
	if child.State != carousel.Undeduced || child.ArtifactHash != "" {
		t.Fatalf("child must stay symbolic, got %+v", child)
	}
	b2 := store(t, cb, "B = [] -> $b2\nB[]", "B", 0)
	f.car.Demand("r.d")
	f.step()
	if got := f.record("r.d").ArtifactHash; got != b2.Hash {
		t.Fatalf("child selected %s, want the rebound hash %s", got, b2.Hash)
	}
}

// Scenario 2: a committed occurrence never retargets.
func TestCommittedOccurrenceNeverRetargets(t *testing.T) {
	cb := codebase.New()
	b1 := store(t, cb, "B = [] -> $b1\nB[]", "B", 0)
	f := open(t, cb, "Upper = [] -> B[]\nUpper[]", 0)
	f.car.Demand("r.d")
	f.step()
	store(t, cb, "B = [] -> $b2\nB[]", "B", 0)
	before := len(cb.Ledger())
	f.car.Demand("r.d")
	f.step()
	if len(cb.Ledger()) != before || f.record("r.d").ArtifactHash != b1.Hash {
		t.Fatal("a committed occurrence was resolved again")
	}
}

// Scenario 3: separate occurrences can observe separate revisions.
func TestSeparateOccurrencesObserveSeparateRevisions(t *testing.T) {
	cb := codebase.New()
	b1 := store(t, cb, "B = [] -> $b1\nB[]", "B", 0)
	f := open(t, cb, "Upper = [] -> {B[], B[]}\nUpper[]", 0)
	f.car.Demand("r.d.0")
	f.step()
	b2 := store(t, cb, "B = [] -> $b2\nB[]", "B", 0)
	f.car.Demand("r.d.1")
	f.step()
	if f.record("r.d.0").ArtifactHash != b1.Hash || f.record("r.d.1").ArtifactHash != b2.Hash {
		t.Fatal("occurrences did not observe their own revisions")
	}
}

// Scenario 4: hash-qualified references stay pinned.
func TestHashQualifiedReferenceStaysPinned(t *testing.T) {
	cb := codebase.New()
	b1 := store(t, cb, "B = [] -> $b1\nB[]", "B", 0)
	f := open(t, cb, "Upper = [] -> B#"+b1.Hash[:8]+"[]\nUpper[]", 0)
	store(t, cb, "B = [] -> $b2\nB[]", "B", 0)
	f.car.Demand("r.d")
	f.step()
	rec := f.record("r.d")
	if rec.ArtifactHash != b1.Hash || rec.ReferenceKind != "hash-qualified" {
		t.Fatalf("pinned reference resolved to %s (%s)", rec.ArtifactHash, rec.ReferenceKind)
	}
}

// Scenario 5 (partial): an intermediate deduction is committed without
// Touchdown. Crash/resume persistence is not implemented by this POC.
func TestIntermediateDeductionIsCommittedWithoutTouchdown(t *testing.T) {
	cb := codebase.New()
	f := open(t, cb, "B = [] -> $b\nUpper = [] -> B[]\nUpper[]", 0)
	f.record("r")
	if f.occ("r.d").State != carousel.Undeduced || f.car.Window() != 0 {
		t.Fatal("expected a committed parent, an undeduced child, and no Touchdown")
	}
}

// Scenario 6: a failed deduction is atomic.
func TestFailedDeductionIsAtomic(t *testing.T) {
	cb := codebase.New()
	f := open(t, cb, "B = [x] -> $b(x)\nA = [x] -> B[x / 0]\nA[1]", 0)
	f.car.Demand("r.d")
	f.step()
	b := f.occ("r.d")
	if b.State != carousel.Failed || b.Failure.Kind != "PrimitiveError" || b.Failure.Phase != "deduction" {
		t.Fatalf("expected a deduction-phase PrimitiveError, got %+v", b.Failure)
	}
	if _, ok := cb.GetDeduction("t", "r.d"); ok || len(b.Children) != 0 || len(cb.Ledger()) != 1 {
		t.Fatal("a failed deduction left a record or children")
	}
}

// Structure-valued lookup maps are blocked pending a language decision
// (Draft SCP: structure-valued lookup maps; runtime ADR 0003).
func TestStructureLookupIsBlocked(t *testing.T) {
	cb := codebase.New()
	src := "routes = {a: A[], b: B[]}\nA = [] -> $a\nB = [] -> $b\nPick = [k] -> routes[k]\nRoot = [] -> Pick[\"a\"]\nRoot[]"
	u := unit(t, cb, src)
	if len(u.Unsupported) == 0 {
		t.Fatal("validation must flag structure-valued lookup as unsupported by this profile")
	}
	f := open(t, cb, src, 0)
	f.car.Demand("r.d")
	f.step()
	if o := f.occ("r.d"); o.State != carousel.Failed || o.Failure.Kind != "UnsupportedByProfile" {
		t.Fatalf("got %+v", o.Failure)
	}
}

// Value-position lookup in ordinary maps remains supported.
func TestValueLookupStillWorks(t *testing.T) {
	src := "m = {a: 1, _: 2}\nS = [n] -> $s(n)\nRoot = [k] -> S[m[k]]\nRoot[\"zzz\"]"
	f := open(t, codebase.New(), src, 0)
	f.car.Demand("r.d")
	f.step()
	if got := f.record("r.d").Arguments[0]; got != "n1:2" {
		t.Fatalf("wildcard value not selected: %s", got)
	}
}

// F7: explicit brackets never receive an implicit argument, so a nested
// serial's first bare stage is /0 even when the upstream stage exports a value.
func TestNestedSerialReceivesNoUpstreamValue(t *testing.T) {
	src := "A = [] -> $a\nB = [] -> $b\nC = [x] -> $c(x)\nRoot = [] -> [A, [B, C]]\nRoot[]"
	f := open(t, codebase.New(), src, 0)
	b := f.occ("r.d.1.0")
	if b.Name != "B" || b.Arity != 0 || b.Input != "" {
		t.Fatalf("nested first stage: %+v", b)
	}
	if c := f.occ("r.d.1.1"); c.Arity != 1 || c.Input != "r.d.1.0" {
		t.Fatalf("second nested stage must receive B's value: %+v", c)
	}
}

// Scenario 7: primitive semantics belong to the Host.
func TestPrimitiveSemanticsAreRequestedDuringDeduction(t *testing.T) {
	cb := codebase.New()
	f := open(t, cb, "B = (y) -> y\nA = [x] -> B[x + 1]\nA[1]", 0)
	if f.prim.calls != 0 {
		t.Fatal("argument evaluated before its occurrence was demanded")
	}
	f.car.Demand("r.d")
	f.step()
	rec := f.record("r.d")
	if f.prim.calls != 1 || rec.Arguments[0] != "n1:2" || rec.PrimitiveProfile != "poc-rational/0" {
		t.Fatalf("unexpected primitive use: calls=%d record=%+v", f.prim.calls, rec)
	}
	if f.occ("r.d.d").Kind != carousel.KindFunction {
		t.Fatal("the operator must not create a Goal node or leaf")
	}
}

// ---- CAROUSEL_ENGINE_PLAN.md mandatory scenarios ----

const pipeline = "Step = [n] -> $work(n)\nPipeline = [] -> [Step[1], Step[2], Step[3], Step[4]]\nPipeline[]"

// 1. Minimum supply: prefetch 0 still grounds demanded work.
func TestMinimumSupplyWithZeroPrefetch(t *testing.T) {
	f := open(t, codebase.New(), pipeline, 0)
	if f.car.Window() != 0 || len(f.cb.Ledger()) != 1 {
		t.Fatal("prefetch 0 must not deduce undemanded work")
	}
	f.car.Demand("r.d.0")
	f.step()
	if f.car.Window() != 1 || !f.car.InWindow("r.d.0.d") {
		t.Fatal("the demanded leaf was not grounded")
	}
}

// 2. Sliding replenishment.
func TestSlidingReplenishment(t *testing.T) {
	f := open(t, codebase.New(), pipeline, 2)
	if f.car.Window() != 2 {
		t.Fatalf("window=%d, want 2", f.car.Window())
	}
	before := len(f.cb.Ledger())
	f.consume("r.d.0.d")
	f.step()
	if f.car.Window() != 2 || len(f.cb.Ledger()) != before+1 || f.occ("r.d.2").State != carousel.Committed {
		t.Fatalf("expected exactly one refill deduction, window=%d ledger=%d", f.car.Window(), len(f.cb.Ledger()))
	}
}

// 3. Value barrier.
func TestValueBarrierBlocksPrefetch(t *testing.T) {
	src := "A = [] -> $a\nB = [x] -> $b(x)\nRoot = [] -> [A, B]\nRoot[]"
	f := open(t, codebase.New(), src, 2)
	if f.car.Window() != 1 || f.occ("r.d.1").State != carousel.Undeduced {
		t.Fatal("B must stay undeduced behind A's unresolved value")
	}
	if _, ok := f.cb.GetDeduction("t", "r.d.1"); ok {
		t.Fatal("no deduction record may exist for a blocked occurrence")
	}
	var reason string
	for _, e := range f.evs {
		if e.Kind == carousel.PrefetchBlocked {
			reason = e.Reason
		}
	}
	if !strings.Contains(reason, "r.d.1=pendingValue:r.d.0") {
		t.Fatalf("barrier not reported, got %q", reason)
	}
	f.vals["r.d.0"] = value.Of(value.String("x"))
	f.step()
	if f.record("r.d.1").Arguments[0] != "s1:x" {
		t.Fatal("B did not deduce with the resolved value")
	}
}

// 4. Parallel fill without dependency edges.
func TestParallelFill(t *testing.T) {
	src := "S = [n] -> $s(n)\nRoot = [] -> {S[1], S[2], S[3]}\nRoot[]"
	f := open(t, codebase.New(), src, 3)
	if f.car.Window() != 3 {
		t.Fatalf("window=%d", f.car.Window())
	}
	for _, id := range []string{"r.d.0", "r.d.1", "r.d.2"} {
		if len(f.occ(id).Deps) != 0 {
			t.Fatal("parallel siblings acquired a dependency")
		}
	}
}

// 5. Atomic overshoot.
func TestAtomicOvershoot(t *testing.T) {
	src := "Wide = [] -> {$a, $b, $c}\nRoot = [] -> Wide[]\nRoot[]"
	f := open(t, codebase.New(), src, 1)
	if f.car.Window() != 3 {
		t.Fatalf("window=%d, want all three leaves", f.car.Window())
	}
	found := false
	for _, e := range f.evs {
		if e.Kind == carousel.AtomicPrefetchOvershoot && e.Count == 2 {
			found = true
		}
	}
	if !found {
		t.Fatal("overshoot not recorded")
	}
}

// 6. Shared node, distinct occurrences.
func TestSharedNodeDistinctOccurrences(t *testing.T) {
	src := "D = (x) -> x\nB = [x] -> D[x]\nC = [x] -> D[x]\nRoot = [] -> {B[1], C[2]}\nRoot[]"
	f := open(t, codebase.New(), src, 10)
	d1, d2 := f.occ("r.d.0.d"), f.occ("r.d.1.d")
	if d1.GoalNodeID == "" || d1.GoalNodeID != d2.GoalNodeID {
		t.Fatal("D/1 occurrences must share one GoalNodeId")
	}
	if d1.Lineage != "Root/0.B/1.D/1" || d2.Lineage != "Root/0.C/1.D/1" {
		t.Fatalf("lineages: %s, %s", d1.Lineage, d2.Lineage)
	}
	if f.car.Window() != 2 {
		t.Fatal("two distinct Touchdowns expected")
	}

	g := open(t, codebase.New(), "G = [] -> $g\nRoot = [] -> {G, G}\nRoot[]", 10)
	a, b := g.occ("r.d.0"), g.occ("r.d.1")
	if a.ID == b.ID || a.GoalNodeID != b.GoalNodeID || g.car.Window() != 2 {
		t.Fatal("{G, G} must keep two occurrences of one node")
	}
}

// 7 and 8. Alias before and after prefetch.
func TestAliasTimingRelativeToPrefetch(t *testing.T) {
	cb := codebase.New()
	store(t, cb, "B = [] -> $b1\nB[]", "B", 0)
	f := open(t, cb, "Upper = [] -> {B[], B[]}\nUpper[]", 0)
	b2 := store(t, cb, "B = [] -> $b2\nB[]", "B", 0)
	f.car.SetPrefetch(1)
	f.step()
	if f.record("r.d.0").ArtifactHash != b2.Hash {
		t.Fatal("prefetch after the alias move must observe the new hash")
	}
	store(t, cb, "B = [] -> $b3\nB[]", "B", 0)
	f.consume("r.d.0.d")
	f.step()
	if f.record("r.d.0").ArtifactHash != b2.Hash {
		t.Fatal("an already prefetched occurrence changed its hash")
	}
	if f.record("r.d.1").ArtifactHash == b2.Hash {
		t.Fatal("the second occurrence must observe the later alias")
	}
}

// 9. Reconfiguration never rolls back.
func TestPrefetchReconfiguration(t *testing.T) {
	f := open(t, codebase.New(), pipeline, 3)
	ledger := len(f.cb.Ledger())
	f.car.SetPrefetch(1)
	f.step()
	if f.car.Window() != 3 || len(f.cb.Ledger()) != ledger {
		t.Fatal("lowering prefetch must not discard grounded leaves or deductions")
	}
	f.car.SetPrefetch(4)
	f.step()
	if f.car.Window() != 4 {
		t.Fatalf("raising prefetch must refill, window=%d", f.car.Window())
	}
}

// 11 (Carousel part). Withdrawal preserves the ledger.
func TestWithdrawPreservesLedger(t *testing.T) {
	f := open(t, codebase.New(), pipeline, 1)
	ledger := f.cb.Ledger()
	f.car.Withdraw("r.d.3")
	f.car.Withdraw("r.d.0") // committed: no effect
	f.car.SetPrefetch(10)
	f.step()
	if f.occ("r.d.3").State != carousel.Withdrawn || f.occ("r.d.0").State != carousel.Committed {
		t.Fatal("withdraw must affect only undeduced occurrences")
	}
	if len(f.cb.Ledger()) != len(ledger)+2 {
		t.Fatalf("only r.d.1 and r.d.2 may still deduce, ledger=%d", len(f.cb.Ledger()))
	}
}

// 12. The Carousel never evaluates leaves.
func TestCarouselNeverEvaluatesLeaves(t *testing.T) {
	src := "F = (x) -> { $sideEffect(x); x }\nRoot = [] -> {F[1], $direct}\nRoot[]"
	cb := codebase.New()
	u := unit(t, cb, src)
	cb.CommitUnit(u)
	h := host.NewScripted()
	car := carousel.New(carousel.Config{RunID: "t", Codebase: cb, Primitives: h.Primitives(), Values: values{}, Prefetch: 10})
	car.OpenRoot(u)
	car.Demand("r")
	car.Replenish()
	if car.Window() != 2 || len(h.Calls) != 0 || len(h.Functions) != 0 {
		t.Fatalf("window=%d calls=%d functions=%d", car.Window(), len(h.Calls), len(h.Functions))
	}
}

// 13. Policy opacity.
func TestPolicyMetadataIsOpaque(t *testing.T) {
	with := open(t, codebase.New(), "S = [n] -> @x(n) $s(n)\nRoot = [] -> @y {S[1], @z S[2]}\nRoot[]", 10)
	without := open(t, codebase.New(), "S = [n] -> $s(n)\nRoot = [] -> {S[1], S[2]}\nRoot[]", 10)
	if with.car.Window() != without.car.Window() || len(with.cb.Ledger()) != len(without.cb.Ledger()) {
		t.Fatal("policies changed Carousel topology or counts")
	}
	if with.occ("r.d.0").GoalNodeID != without.occ("r.d.0").GoalNodeID {
		t.Fatal("policies changed GoalNodeId")
	}
	if p := with.occ("r.d").Policies; len(p) != 1 || p[0].Ident != "y" {
		t.Fatalf("composite policy lost: %+v", p)
	}
	if len(with.occ("r.d.0").Policies) != 0 || with.occ("r.d.1").Policies[0].Ident != "z" {
		t.Fatal("policies must stay on their own occurrences")
	}
	if arg := with.occ("r.d.0.d").Policies[0].Args[0]; arg.String() != "1" {
		t.Fatalf("policy argument not preserved: %s", arg)
	}
}

func TestDynamicCycleIsADeductionError(t *testing.T) {
	cb := codebase.New()
	store(t, cb, "B = [] -> $b\nB[]", "B", 0)
	f := open(t, cb, "A = [] -> B[]\nA[]", 0)
	// Rebind B so that it re-enters A.
	store(t, cb, "B = [] -> A[]\nB[]", "B", 0)
	f.car.Demand("r.d")
	f.step()
	f.car.Demand("r.d.d")
	f.step()
	if o := f.occ("r.d.d"); o.State != carousel.Failed || o.Failure.Kind != "CycleDetected" || o.Failure.Phase != "deduction" {
		t.Fatalf("expected a deduction-phase CycleDetected, got %+v", o.Failure)
	}
}

func TestDestructureMismatchAtDeduction(t *testing.T) {
	src := "Use = [x] -> $use(x)\nRoot = [] -> [$make, [{x}] -> Use[x]]\nRoot[]"
	f := open(t, codebase.New(), src, 0)
	f.vals["r.d.0"] = value.Of(value.String("not a map"))
	f.car.Demand("r.d.1")
	f.step()
	o := f.occ("r.d.1")
	if o.State != carousel.Failed || o.Failure.Kind != "DestructureMismatch" || o.Failure.Phase != "deduction" {
		t.Fatalf("got %+v", o.Failure)
	}
	if _, ok := f.cb.GetDeduction("t", "r.d.1"); ok {
		t.Fatal("failed deduction committed")
	}
}

// SCP-0003 / README: Goal(...) demands its occurrence as soon as it is
// exposed; Goal[...] waits for demand. The suffix changes timing only.
func TestEagerCallIsDemandedOnExposure(t *testing.T) {
	src := "C = (x) -> x + 1\nRoot = [x] -> {C(x), C[x]}\nRoot[1]"
	f := open(t, codebase.New(), src, 0)
	eager, lazy := f.occ("r.d.0"), f.occ("r.d.1")
	if !eager.Eager || lazy.Eager {
		t.Fatal("only the call suffix marks an occurrence eager")
	}
	if eager.State != carousel.Committed || lazy.State != carousel.Undeduced {
		t.Fatalf("eager=%s lazy=%s; want committed and undeduced with prefetch 0", eager.State, lazy.State)
	}
	demands := 0
	for _, e := range f.evs {
		if e.Kind == carousel.DemandObserved && e.Occ == "r.d.0" && e.Reason == "eager" {
			demands++
		}
	}
	if demands != 1 {
		t.Fatalf("eager demand events: %d", demands)
	}
	er, lr := f.record("r.d.0"), f.car.Occurrence("r.d.1")
	if er.GoalNodeID == "" || lr.GoalNodeID != "" {
		t.Fatal("the eager occurrence must commit its own deduction record")
	}
}

// ---- SCP-0001 acknowledgement scenarios ----

// Scenario 19: a full window never blocks explicit demand.
func TestDemandIsDeducedOverAFullWindow(t *testing.T) {
	src := "S = [n] -> $work(n)\nRoot = [] -> {S[1], S[2]}\nRoot[]"
	f := open(t, codebase.New(), src, 1)
	if f.car.Window() != 1 || f.occ("r.d.1").State != carousel.Undeduced {
		t.Fatal("prefetch should stop at the target")
	}
	f.car.Demand("r.d.1")
	ev := f.step()
	if f.car.Window() != 2 || f.occ("r.d.1").State != carousel.Committed {
		t.Fatalf("demanded occurrence not deduced, window=%d", f.car.Window())
	}
	if count(ev, carousel.DemandedTouchdownOverTarget) != 1 || count(ev, carousel.AtomicPrefetchOvershoot) != 0 {
		t.Fatalf("over-target demand must be reported separately: %+v", ev)
	}
}

// Scenario 19 (partial window): the over-target condition uses the count
// after the demanded deduction, not the count before it.
func TestDemandOverTargetFromPartialWindow(t *testing.T) {
	src := "P = [] -> {$a(), $b()}\nRoot = [] -> {$w(), P[]}\nRoot[]"
	f := open(t, codebase.New(), src, 1)
	if f.car.Window() != 1 || f.occ("r.d.1").State != carousel.Undeduced {
		t.Fatalf("setup: window=%d", f.car.Window())
	}
	f.car.SetPrefetch(2) // target 2, one leaf buffered
	f.car.Demand("r.d.1")
	ev := f.step()
	if f.car.Window() != 3 {
		t.Fatalf("window=%d, want 3", f.car.Window())
	}
	var over *carousel.Event
	for i := range ev {
		if ev[i].Kind == carousel.DemandedTouchdownOverTarget {
			over = &ev[i]
		}
	}
	if over == nil || over.Occ != "r.d.1" || over.Window != 3 || over.Count != 2 {
		t.Fatalf("missing or wrong DemandedTouchdownOverTarget: %+v", ev)
	}
	if count(ev, carousel.AtomicPrefetchOvershoot) != 0 {
		t.Fatalf("a demanded deduction is not a prefetch overshoot: %+v", ev)
	}
}

// A demanded deduction that stays within the target reports nothing.
func TestDemandWithinTargetIsNotOverTarget(t *testing.T) {
	src := "P = [] -> {$a(), $b()}\nRoot = [] -> {$w(), P[]}\nRoot[]"
	f := open(t, codebase.New(), src, 1)
	f.car.SetPrefetch(3)
	f.car.Demand("r.d.1")
	ev := f.step()
	if f.car.Window() != 3 || count(ev, carousel.DemandedTouchdownOverTarget) != 0 {
		t.Fatalf("window=%d events=%+v", f.car.Window(), ev)
	}
}

// Scenarios 20 and 23: consume is applied once; a replay by the same attempt
// re-confirms it, and a competing attempt is not authorized.
func TestConsumeIsAppliedOnce(t *testing.T) {
	f := open(t, codebase.New(), pipeline, 1)
	o := f.occ("r.d.0.d")
	inst := f.car.EvaluationInstanceID(o)
	if res, _ := f.car.ConsumeTouchdown(o.ID, inst, "a1"); res != carousel.AckConsumed {
		t.Fatalf("first consume: %s", res)
	}
	f.car.Drain()
	// Replay by the same attempt re-confirms its authorization.
	res, by := f.car.ConsumeTouchdown(o.ID, inst, "a1")
	if res != carousel.AckConsumedReplay || by != "a1" || !res.Authorizes() {
		t.Fatalf("replayed consume: %s/%s", res, by)
	}
	// A different attempt lost the first dispatch and is not authorized.
	res, by = f.car.ConsumeTouchdown(o.ID, inst, "a2")
	if res != carousel.AckAlreadyConsumed || by != "a1" || res.Authorizes() {
		t.Fatalf("competing consume: %s/%s", res, by)
	}
	if res, _ := f.car.ConsumeTouchdown(o.ID, inst, ""); res != carousel.AckInvalidAttempt || res.Authorizes() {
		t.Fatalf("empty attempt: %s", res)
	}
	if len(f.car.Drain()) != 0 {
		t.Fatal("a repeated acknowledgement emitted events")
	}
	if f.car.InWindow(o.ID) {
		t.Fatal("a consumed Touchdown re-entered the window")
	}
	if res, _ := f.car.ConsumeTouchdown(o.ID, "wrong", "a3"); res != carousel.AckMismatch {
		t.Fatalf("mismatched instance: %s", res)
	}
	if res, _ := f.car.ConsumeTouchdown("r.nope", inst, "a4"); res != carousel.AckUnknown {
		t.Fatalf("unknown occurrence: %s", res)
	}
}

// Scenarios 21 and 22: discard before dispatch, and both race orders.
func TestConsumeDiscardRace(t *testing.T) {
	f := open(t, codebase.New(), pipeline, 2)
	first, second := f.occ("r.d.0.d"), f.occ("r.d.1.d")

	// Discard wins.
	if res, _ := f.car.DiscardTouchdown(first.ID, "cancelled"); res != carousel.AckDiscarded {
		t.Fatalf("discard: %s", res)
	}
	if res, _ := f.car.ConsumeTouchdown(first.ID, f.car.EvaluationInstanceID(first), "late"); res != carousel.AckDiscarded {
		t.Fatalf("consume after discard: %s", res)
	}
	if res, _ := f.car.DiscardTouchdown(first.ID, "again"); res != carousel.AckAlreadyDiscarded {
		t.Fatalf("repeat discard: %s", res)
	}

	// Consume wins.
	if res, _ := f.car.ConsumeTouchdown(second.ID, f.car.EvaluationInstanceID(second), "a1"); res != carousel.AckConsumed {
		t.Fatalf("consume: %s", res)
	}
	if res, by := f.car.DiscardTouchdown(second.ID, "cancelled"); res != carousel.AckAlreadyConsumed || by != "a1" {
		t.Fatalf("discard after consume: %s", res)
	}
	ev := f.car.Drain()
	if count(ev, carousel.TouchdownDiscarded) != 1 || count(ev, carousel.TouchdownConsumed) != 1 {
		t.Fatalf("each Touchdown must leave the window exactly once: %+v", ev)
	}
	for _, e := range ev {
		if e.Kind == carousel.TouchdownConsumed && (e.Attempt != "a1" || e.EvaluationInstance == "" || e.Window != 0) {
			t.Fatalf("consume event fields: %+v", e)
		}
	}
}
