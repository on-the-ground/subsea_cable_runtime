package runtime_test

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/on-the-ground/subsea_cable_runtime/carousel"
	"github.com/on-the-ground/subsea_cable_runtime/codebase"
	"github.com/on-the-ground/subsea_cable_runtime/host"
	"github.com/on-the-ground/subsea_cable_runtime/runtime"
	"github.com/on-the-ground/subsea_cable_runtime/runtime/policyexamples"
	"github.com/on-the-ground/subsea_cable_runtime/sema"
	"github.com/on-the-ground/subsea_cable_runtime/value"
)

type harness struct {
	t   *testing.T
	cb  *codebase.Codebase
	h   *host.Scripted
	run *runtime.Run
}

func start(t *testing.T, src string, cfg runtime.Config) *harness {
	t.Helper()
	cb := codebase.New()
	u, err := sema.Check([]byte(src), cb)
	if err != nil {
		t.Fatalf("invalid program: %v", err)
	}
	h := host.NewScripted()
	h.Fallback = host.Echo
	cfg.Host, cfg.Codebase = h, cb
	r, err := runtime.Start(cfg, u)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	return &harness{t: t, cb: cb, h: h, run: r}
}

func (x *harness) finish() *runtime.Result {
	res := x.run.RunToCompletion()
	if testing.Verbose() {
		x.run.Trace().WriteText(os.Stdout)
	}
	return res
}

func (x *harness) recordsFor(id string) int {
	n := 0
	for _, r := range x.cb.Ledger() {
		if r.Occurrence == id {
			n++
		}
	}
	return n
}

// §16.1 The run command demands the Root even with prefetch 0.
func TestRunDemandsRootWithZeroPrefetch(t *testing.T) {
	x := start(t, "Root = [x] -> $echo(x)\nRoot[42]", runtime.Config{})
	res := x.finish()
	if res.Status != host.Succeeded || res.Output.Value.AsString() != "echo(42)" {
		t.Fatalf("result %+v", res)
	}
}

// §16.2 Readiness alone never creates demand.
func TestReadinessDoesNotCreateDemand(t *testing.T) {
	x := start(t, "A = [] -> $a\nRoot = [] -> [A[], A[]]\nRoot[]", runtime.Config{Demand: runtime.DemandManual})
	res := x.finish()
	if res.Status != host.Failed || res.Diag.Kind != "RunStuck" {
		t.Fatalf("expected RunStuck, got %+v", res)
	}
	if len(x.cb.Ledger()) != 1 {
		t.Fatalf("only the Root may deduce without demand, ledger=%d", len(x.cb.Ledger()))
	}
	if !strings.Contains(strings.Join(res.Diag.Details["blocking"].([]string), " "), "r.d.0=undemanded") {
		t.Fatalf("blocking reasons missing: %v", res.Diag.Details)
	}
}

func TestManualDemandDrivesTheRun(t *testing.T) {
	y := start(t, "A = [] -> $a\nRoot = [] -> [A[], A[]]\nRoot[]", runtime.Config{Demand: runtime.DemandManual})
	y.run.Advance()
	y.run.Demand("r.d.0")
	y.run.Demand("r.d.1")
	res := y.finish()
	if res.Status != host.Succeeded {
		t.Fatalf("%+v", res.Diag)
	}
}

// §16.3 A leaf reattempt never produces a second deduction record.
func TestReattemptDoesNotRededuce(t *testing.T) {
	x := start(t, "Patch = [d] -> @retry(2) $editFiles(d)\nPatch[\"diag\"]",
		runtime.Config{Policies: policyexamples.Registry()})
	x.h.FailNext("editFiles", 2)
	res := x.finish()
	if res.Status != host.Succeeded {
		t.Fatalf("%+v", res.Diag)
	}
	if x.recordsFor("r") != 1 || len(x.h.Calls) != 3 {
		t.Fatalf("records=%d calls=%d", x.recordsFor("r"), len(x.h.Calls))
	}
	if x.run.Scope("r.d").Attempts != 3 {
		t.Fatal("expected three attempts on one evaluation instance")
	}
}

func TestRetryExhaustionFailsTheRun(t *testing.T) {
	x := start(t, "Patch = [d] -> @retry(1) $editFiles(d)\nPatch[1]", runtime.Config{Policies: policyexamples.Registry()})
	x.h.FailNext("editFiles", 5)
	res := x.finish()
	if res.Status != host.Failed || res.Diag.Kind != "InjectedFailure" || res.Diag.Phase != "host" {
		t.Fatalf("%+v", res)
	}
}

// A retry on a Goal occurrence does not reach its descendant leaf.
func TestPolicyOnGoalOccurrenceIsNotInherited(t *testing.T) {
	x := start(t, "Patch = [d] -> $editFiles(d)\nRoot = [] -> @retry Patch[1]\nRoot[]",
		runtime.Config{Policies: policyexamples.Registry()})
	res := x.finish()
	if res.Status != host.Failed || res.Diag.Kind != "UnsupportedPolicyTarget" {
		t.Fatalf("expected UnsupportedPolicyTarget for @retry on a Goal occurrence, got %+v", res.Diag)
	}
	if len(x.h.Calls) != 0 {
		t.Fatal("the leaf must not run after its enclosing policy was rejected")
	}
}

type recorder struct {
	events []runtime.PolicyEventKind
}

func (r *recorder) ID() string      { return "observe" }
func (r *recorder) Version() string { return "test" }
func (r *recorder) Targets() []carousel.Kind {
	return []carousel.Kind{carousel.KindParallel, carousel.KindAnchor, carousel.KindGoal}
}
func (r *recorder) Validate([]value.Value) error     { return nil }
func (r *recorder) Attach(runtime.PolicyContext) any { return nil }
func (r *recorder) On(ev runtime.PolicyEvent, _ any, _ runtime.PolicyContext) []runtime.Action {
	r.events = append(r.events, ev.Kind)
	return nil
}

// §16.4 A composite policy observes its children only through its own scope.
func TestCompositePolicyObservesOnlyItsOwnScope(t *testing.T) {
	rec := &recorder{}
	x := start(t, "Root = [] -> @observe {$a, $b}\nRoot[]", runtime.Config{Policies: runtime.NewRegistry().Register(rec)})
	res := x.finish()
	if res.Status != host.Succeeded || !res.Output.NoOutput {
		t.Fatalf("%+v", res)
	}
	got := map[runtime.PolicyEventKind]int{}
	for _, e := range rec.events {
		got[e]++
	}
	if got[runtime.ChildScopeOutcome] != 2 || got[runtime.BeforeAttempt] != 0 || got[runtime.AttemptOutcome] != 0 {
		t.Fatalf("composite policy events: %v", rec.events)
	}
	for _, id := range []string{"r.d.0", "r.d.1"} {
		if len(x.run.Carousel().Occurrence(id).Policies) != 0 {
			t.Fatal("composite policy copied onto a child")
		}
	}
}

// §16.5 CancelScope leaves undeduced descendants undeduced.
func TestTimeoutCancelsWithoutTouchingTheLedger(t *testing.T) {
	src := "Slow = [] -> $slow\nNext = [x] -> $next(x)\nRoot = [] -> @timeout(3) [Slow[], Next]\nRoot[]"
	x := start(t, src, runtime.Config{Policies: policyexamples.Registry()})
	x.h.SetTicks("slow", 10)
	x.run.Advance()
	ledgerAtStart := len(x.cb.Ledger())
	res := x.finish()
	if res.Status != host.Failed || res.Diag.Kind != "PolicyTimeout" || res.Time != 3 {
		t.Fatalf("%+v", res)
	}
	next := x.run.Carousel().Occurrence("r.d.1")
	if next.State != carousel.Withdrawn || x.recordsFor("r.d.1") != 0 {
		t.Fatalf("Next must stay undeduced and be withdrawn, state=%s", next.State)
	}
	if len(x.h.Cancelled) != 1 {
		t.Fatalf("the in-flight attempt must be cancelled, got %v", x.h.Cancelled)
	}
	if len(x.cb.Ledger()) != ledgerAtStart {
		t.Fatal("cancellation changed the ledger")
	}
}

// §16.6 A policy disclosed late is rejected before its occurrence executes.
func TestLatePolicyIsRejectedBeforeExecution(t *testing.T) {
	src := "First = [] -> $first\nLater = [x] -> @mystery $later(x)\nRoot = [] -> [First[], Later]\nRoot[]"
	x := start(t, src, runtime.Config{})
	res := x.finish()
	if res.Status != host.Failed || res.Diag.Kind != "UnknownPolicy" || res.Diag.Phase != "policy" {
		t.Fatalf("%+v", res.Diag)
	}
	if got := strings.Join(x.h.CallNames(), ","); got != "first" {
		t.Fatalf("calls=%s", got)
	}
}

// §16.7 An unkeyed parallel scope outputs NoOutput.
func TestParallelExportsNoOutput(t *testing.T) {
	x := start(t, "Root = [] -> {$a, $b}\nRoot[]", runtime.Config{})
	if res := x.finish(); res.Status != host.Succeeded || !res.Output.NoOutput {
		t.Fatalf("%+v", res)
	}
}

// §16.8 A pending routed value creates no downstream record or Touchdown.
func TestPendingValueCreatesNoDownstreamWork(t *testing.T) {
	src := "Use = [x] -> $use(x)\nRoot = [] -> [$make, [{x}] -> Use[x]]\nRoot[]"
	x := start(t, src, runtime.Config{Prefetch: 5})
	x.h.SetTicks("make", 5)
	x.run.Advance()
	if x.recordsFor("r.d.1") != 0 || x.run.Carousel().Window() > 1 {
		t.Fatal("downstream work appeared before its value resolved")
	}
	res := x.finish()
	if res.Status != host.Failed || res.Diag.Kind != "DestructureMismatch" || res.Diag.Phase != "deduction" {
		t.Fatalf("%+v", res.Diag)
	}
	if x.recordsFor("r.d.1") != 0 {
		t.Fatal("the failed deduction committed")
	}
}

// §16.9 is covered by TestReadinessDoesNotCreateDemand.

// §16.10 Nested Anchor calls are traced and carry no policy.
func TestNestedAnchorCallsGoThroughTheGateway(t *testing.T) {
	src := "Verify = (p) -> { $runTests(p); $report(p); \"ok\" }\nRoot = [] -> Verify[\"patch\"]\nRoot[]"
	x := start(t, src, runtime.Config{})
	res := x.finish()
	if res.Status != host.Succeeded || res.Output.Value.AsString() != "ok" {
		t.Fatalf("%+v", res)
	}
	if n := x.run.Trace().Count("AnchorGatewayCall", "r.d.d"); n != 2 {
		t.Fatalf("gateway calls=%d", n)
	}
	for _, c := range x.h.Calls {
		if !c.Nested || c.Ctx.Occurrence != "r.d.d" {
			t.Fatalf("nested call not attributed to its leaf: %+v", c)
		}
	}
	for _, o := range x.run.Carousel().Occurrences() {
		if o.Kind == carousel.KindAnchor {
			t.Fatal("a nested Anchor call became an occurrence")
		}
	}
}

// Carousel plan scenario 10: a failure found by prefetch does not disturb the
// evaluating leaf; it surfaces when that occurrence would be demanded.
func TestPrefetchFailureIsDeferredUntilDemand(t *testing.T) {
	src := "S = [n] -> $work(n)\nRoot = [] -> [S[1], S[1 / 0]]\nRoot[]"
	x := start(t, src, runtime.Config{Prefetch: 2})
	x.h.SetTicks("work", 3)
	res := x.finish()
	if res.Status != host.Failed || res.Diag.Kind != "PrimitiveError" {
		t.Fatalf("%+v", res.Diag)
	}
	tr := x.run.Trace()
	if tr.Count("DeductionFailureDeferred", "r.d.1") != 1 || tr.Count("EvaluationSucceeded", "r.d.0.d") != 1 {
		t.Fatal("the prefetch failure disturbed the evaluating leaf")
	}
	if tr.Count("EvaluationCancelRequested", "") != 0 {
		t.Fatal("no in-flight work should be cancelled")
	}
}

// Carousel plan scenario 11: cancellation preserves the ledger.
func TestCancellationPreservesLedger(t *testing.T) {
	x := start(t, "S = [n] -> $work(n)\nRoot = [] -> [S[1], S[2], S[3]]\nRoot[]", runtime.Config{Prefetch: 1})
	x.h.SetTicks("work", 5)
	x.run.Advance()
	ledger := len(x.cb.Ledger())
	x.run.Cancel("operator request")
	if res := x.run.Result(); res == nil || res.Status != host.Cancelled {
		t.Fatalf("%+v", res)
	}
	if len(x.cb.Ledger()) != ledger || x.run.Step() {
		t.Fatal("cancellation changed the ledger or the run continued")
	}
	if len(x.h.Cancelled) != 1 {
		t.Fatalf("expected the in-flight attempt to be cancelled, got %v", x.h.Cancelled)
	}
}

// Owner decision R10: the consumption point changes prefetch timing. The POC
// exposes both points only to make that difference observable.
func TestConsumptionPointChangesDeductionTiming(t *testing.T) {
	src := "S = [n] -> $work(n)\nRoot = [] -> [S[1], S[2], S[3], S[4]]\nRoot[]"
	atZero := func(cp runtime.ConsumePoint) int {
		x := start(t, src, runtime.Config{Prefetch: 1, Concurrency: 1, ConsumeAt: cp})
		x.h.SetTicks("work", 5)
		x.run.Advance()
		return len(x.cb.Ledger())
	}
	dispatch, completion := atZero(runtime.ConsumeAtDispatch), atZero(runtime.ConsumeAtCompletion)
	if dispatch <= completion {
		t.Fatalf("expected more early deductions when consuming at dispatch: dispatch=%d completion=%d", dispatch, completion)
	}
}

func TestUnknownPolicyIsNeverIgnored(t *testing.T) {
	x := start(t, "Root = [] -> @retry $a\nRoot[]", runtime.Config{})
	res := x.finish()
	if res.Status != host.Failed || res.Diag.Kind != "UnknownPolicy" || len(x.h.Calls) != 0 {
		t.Fatalf("%+v", res.Diag)
	}
}

func TestStackedPoliciesNeedADeclaredPairing(t *testing.T) {
	src := "Root = [] -> @retry @timeout(5) $a\nRoot[]"
	x := start(t, src, runtime.Config{Policies: policyexamples.Registry()})
	if res := x.finish(); res.Diag == nil || res.Diag.Kind != "PolicyConflict" {
		t.Fatalf("%+v", res.Diag)
	}
	y := start(t, src, runtime.Config{Policies: policyexamples.Registry().AllowPair("retry", "timeout")})
	if res := y.finish(); res.Status != host.Succeeded {
		t.Fatalf("%+v", res.Diag)
	}
}

var policyText = regexp.MustCompile(`@[A-Za-z_][A-Za-z0-9_]*(\([^)]*\))? ?`)

// Policy erasure: for every occurrence actually deduced in both runs, the
// policy-erased structural reduction result is the same.
func TestPolicyErasureKeepsReductionResults(t *testing.T) {
	src, err := os.ReadFile("../examples/fix.subc")
	if err != nil {
		t.Fatal(err)
	}
	erased := policyText.ReplaceAllString(string(src), "")
	a := start(t, string(src), runtime.Config{Policies: policyexamples.Registry(), Prefetch: 1})
	b := start(t, erased, runtime.Config{Prefetch: 1})
	if ra, rb := a.finish(), b.finish(); ra.Status != host.Succeeded || rb.Status != host.Succeeded {
		t.Fatalf("%v / %v", ra.Diag, rb.Diag)
	}
	results := map[string]codebase.DeductionRecord{}
	for _, r := range b.cb.Ledger() {
		results[r.Occurrence] = r
	}
	compared := 0
	for _, r := range a.cb.Ledger() {
		other, ok := results[r.Occurrence]
		if !ok {
			continue
		}
		compared++
		if policyText.ReplaceAllString(r.Result, "") != other.Result || r.GoalNodeID != other.GoalNodeID {
			t.Fatalf("%s: %q/%s vs %q/%s", r.Occurrence, r.Result, r.GoalNodeID, other.Result, other.GoalNodeID)
		}
	}
	if compared == 0 {
		t.Fatal("nothing compared")
	}
}

func TestFixExampleEndToEnd(t *testing.T) {
	src, err := os.ReadFile("../examples/fix.subc")
	if err != nil {
		t.Fatal(err)
	}
	x := start(t, string(src), runtime.Config{Policies: policyexamples.Registry(), Prefetch: 1})
	x.h.FailNext("editFiles", 1)
	res := x.finish()
	if res.Status != host.Succeeded || !strings.HasPrefix(res.Output.Value.AsString(), "verified editFiles(") {
		t.Fatalf("%+v", res)
	}
	want := []string{"readCode", "ciLogs", "diagnoseWithLLM", "editFiles", "editFiles", "runTests"}
	if got := x.h.CallNames(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("calls %v", got)
	}
	if x.recordsFor("r.d.2") != 1 {
		t.Fatal("Patch deduced more than once")
	}
}

func TestStartRefusesProgramsThePOCCannotRun(t *testing.T) {
	cb := codebase.New()
	u, err := sema.Check([]byte("C = (x) -> x\nRoot = [] -> [$a, C(1)]\nRoot[]"), cb)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Start(runtime.Config{Host: host.NewScripted(), Codebase: cb}, u); err == nil ||
		!strings.Contains(err.Error(), "UnsupportedByProfile") {
		t.Fatalf("expected UnsupportedByProfile, got %v", err)
	}
}
