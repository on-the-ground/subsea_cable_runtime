package runtime_test

import (
	"testing"

	"github.com/on-the-ground/subsea_cable_runtime/carousel"
	"github.com/on-the-ground/subsea_cable_runtime/host"
	"github.com/on-the-ground/subsea_cable_runtime/runtime"
)

// withhold returns a Scheduler test double that withholds the first dispatch
// of one occurrence for a number of ticks.
func withhold(occ string, ticks int) runtime.SchedulerHooks {
	return runtime.SchedulerHooks{WithholdFirstDispatch: func(o string) int {
		if o == occ {
			return ticks
		}
		return 0
	}}
}

// Carousel scenario 16: a withheld leaf occupies the window, so speculative
// deduction does not exceed the target because of it.
func TestWithheldLeafOccupiesTheWindow(t *testing.T) {
	src := "Held = [] -> $held\nS = [n] -> $work(n)\nRoot = [] -> {Held[], S[1], S[2]}\nRoot[]"
	x := start(t, src, runtime.Config{Prefetch: 1, Concurrency: 1, Demand: runtime.DemandManual, Hooks: withhold("r.d.0.d", 5)})
	x.run.Advance()
	x.run.Demand("r.d.0")
	x.run.Advance()
	car := x.run.Carousel()
	if !car.InWindow("r.d.0.d") || x.run.Trace().Count("EvaluationStarted", "") != 0 {
		t.Fatal("the withheld leaf must stay in the window without being attempted")
	}
	if car.Window() != 1 || car.Occurrence("r.d.1").State != carousel.Undeduced || car.Occurrence("r.d.2").State != carousel.Undeduced {
		t.Fatalf("prefetch deduced past a full window: window=%d", car.Window())
	}
	res := x.finish()
	if res.Status != host.Succeeded || x.run.Trace().Count("TouchdownConsumed", "r.d.0.d") != 1 {
		t.Fatalf("the withheld leaf should run later and be consumed once: %+v", res.Diag)
	}
	for _, e := range x.run.Trace().Filter("EvaluationStarted") {
		if e.Occ == "r.d.0.d" && e.Time < 5 {
			t.Fatalf("the withheld leaf started at t=%d", e.Time)
		}
	}
}

// Carousel scenario 17: a grounded leaf waiting on an upstream outcome counts
// toward the window.
func TestIneligibleLeafCountsTowardTheWindow(t *testing.T) {
	src := "S = [n] -> $work(n)\nRoot = [] -> [S[1], S[2], S[3]]\nRoot[]"
	x := start(t, src, runtime.Config{Prefetch: 1, Concurrency: 1})
	x.h.SetTicks("work", 5)
	x.run.Advance()
	car := x.run.Carousel()
	if x.run.Scope("r.d.1.d") == nil || x.run.Scope("r.d.1.d").Leaf != runtime.LeafWaiting {
		t.Fatal("S[2] should be grounded and waiting on S[1]")
	}
	if car.Window() != 1 || !car.InWindow("r.d.1.d") || car.Occurrence("r.d.2").State != carousel.Undeduced {
		t.Fatalf("an ineligible leaf must fill the window: window=%d", car.Window())
	}
}

// Carousel scenario 18: a Scheduler-created later attempt neither re-enters
// the window nor emits a second TouchdownConsumed.
func TestReattemptDoesNotReenterTheWindow(t *testing.T) {
	src := "Patch = [d] -> $editFiles(d)\nNext = [] -> $next\nRoot = [] -> {Patch[1], Next[]}\nRoot[]"
	x := start(t, src, runtime.Config{Prefetch: 1, Concurrency: 1, Hooks: reattempt(2)})
	x.h.FailNext("editFiles", 2)
	x.h.SetTicks("editFiles", 3)
	res := x.finish()
	if res.Status != host.Succeeded {
		t.Fatalf("%+v", res.Diag)
	}
	tr := x.run.Trace()
	if tr.Count("EvaluationStarted", "r.d.0.d") != 3 {
		t.Fatalf("expected three attempts, got %d", tr.Count("EvaluationStarted", "r.d.0.d"))
	}
	if tr.Count("TouchdownPublished", "r.d.0.d") != 1 || tr.Count("TouchdownConsumed", "r.d.0.d") != 1 {
		t.Fatal("a reattempted leaf was published or consumed more than once")
	}
}

// Carousel scenario 19 at Runtime level: with the window full of a withheld
// leaf, explicit demand still reaches Touchdown and is reported as
// DemandedTouchdownOverTarget.
func TestDemandBeatsAFullWindow(t *testing.T) {
	src := "Held = [] -> $held\nS = [n] -> $work(n)\nRoot = [] -> {Held[], S[1]}\nRoot[]"
	x := start(t, src, runtime.Config{Prefetch: 1, Demand: runtime.DemandManual, Hooks: withhold("r.d.0.d", 5)})
	x.run.Advance()
	x.run.Demand("r.d.0")
	x.run.Advance()
	x.run.Demand("r.d.1")
	x.run.Advance()
	tr := x.run.Trace()
	if x.run.Scope("r.d.1.d") == nil || tr.Count("DemandedTouchdownOverTarget", "r.d.1") != 1 || tr.Count("AtomicPrefetchOvershoot", "") != 0 {
		t.Fatal("demanded work must be deduced over a full window and reported as such")
	}
}

// Carousel scenario 21 at Runtime level: a Touchdown discarded before its
// first dispatch never reaches the Host.
func TestDiscardedTouchdownNeverReachesTheHost(t *testing.T) {
	src := "Held = [] -> $held\nRoot = [] -> {Held[], Held[]}\nRoot[]"
	x := start(t, src, runtime.Config{Prefetch: 2, Hooks: withhold("r.d.0.d", 5)})
	x.run.Advance()
	if !x.run.Carousel().InWindow("r.d.0.d") {
		t.Fatal("the withheld leaf should be buffered")
	}
	x.run.CancelScope("r.d.0", "not needed")
	tr := x.run.Trace()
	if tr.Count("TouchdownDiscarded", "r.d.0.d") != 1 || tr.Count("TouchdownConsumed", "r.d.0.d") != 0 {
		t.Fatal("the cancelled leaf must be discarded, not consumed")
	}
	for _, c := range x.h.Calls {
		if c.Ctx.Occurrence == "r.d.0.d" {
			t.Fatal("a discarded Touchdown reached the Host")
		}
	}
}

// hostCalls counts Host invocations for one occurrence.
func hostCalls(x *harness, occ string) int {
	n := 0
	for _, c := range x.h.Calls {
		if c.Ctx.Occurrence == occ {
			n++
		}
	}
	return n
}

// Scenario 20 at Runtime level: a replayed consume from the same attempt
// (the first response was lost) still authorizes that attempt, and the Host is
// invoked exactly once for it.
func TestReplayedConsumeAuthorizesTheSameAttemptOnce(t *testing.T) {
	src := "Held = [] -> $held\nRoot = [] -> {Held[], $other}\nRoot[]"
	x := start(t, src, runtime.Config{Prefetch: 1, Hooks: withhold("r.d.0.d", 3)})
	x.run.Advance()
	car := x.run.Carousel()
	o := car.Occurrence("r.d.0.d")
	attempt := x.run.ID() + "/r.d.0.d#1"
	if res, _ := car.ConsumeTouchdown(o.ID, car.EvaluationInstanceID(o), attempt); res != carousel.AckConsumed {
		t.Fatalf("pre-consume: %s", res)
	}
	res := x.finish()
	if res.Status != host.Succeeded {
		t.Fatalf("%+v", res.Diag)
	}
	if n := hostCalls(x, "r.d.0.d"); n != 1 {
		t.Fatalf("host called %d times, want 1", n)
	}
	if x.run.Trace().Count("AttemptAborted", "") != 0 || car.Window() != 0 {
		t.Fatal("a replayed consume must not abort the attempt or re-enter the window")
	}
}

// Scenario 23 at Runtime level: when a different attempt consumed the
// Touchdown first, this attempt is aborted and never reaches the Host.
func TestCompetingConsumeAbortsWithoutCallingTheHost(t *testing.T) {
	src := "Held = [] -> $held\nRoot = [] -> {Held[], $other}\nRoot[]"
	x := start(t, src, runtime.Config{Prefetch: 1, Hooks: withhold("r.d.0.d", 3)})
	x.run.Advance()
	car := x.run.Carousel()
	o := car.Occurrence("r.d.0.d")
	if res, _ := car.ConsumeTouchdown(o.ID, car.EvaluationInstanceID(o), "elsewhere#1"); res != carousel.AckConsumed {
		t.Fatalf("pre-consume: %s", res)
	}
	res := x.finish()
	if res.Status == host.Succeeded {
		t.Fatal("the run must not succeed when its leaf lost the first dispatch")
	}
	if n := hostCalls(x, "r.d.0.d"); n != 0 {
		t.Fatalf("host called %d times, want 0", n)
	}
	ab := x.run.Trace().Filter("AttemptAborted")
	if len(ab) != 1 || ab[0].Reason != string(carousel.AckAlreadyConsumed) || ab[0].AttemptID != x.run.ID()+"/r.d.0.d#1" || ab[0].RunID != x.run.ID() {
		t.Fatalf("abort not recorded with typed fields: %+v", ab)
	}
	if s := x.run.Scope("r.d.0.d"); s == nil || s.Diag == nil || s.Diag.Kind != "TouchdownAcknowledgementRejected" {
		t.Fatalf("leaf not settled as a boundary failure: %+v", s)
	}
}

// Scenario 22 at Runtime level, consume first: a discard that loses to a
// dispatch leaves the window unchanged and the attempt is cancelled through
// the Host.
func TestDiscardAfterDispatchCancelsTheAttempt(t *testing.T) {
	src := "Slow = [] -> $slow\nRoot = [] -> {Slow[], $other}\nRoot[]"
	x := start(t, src, runtime.Config{Prefetch: 1})
	x.h.SetTicks("slow", 5)
	x.run.Advance()
	if hostCalls(x, "r.d.0.d") != 1 {
		t.Fatal("the leaf should be in flight")
	}
	x.run.CancelScope("r.d.0", "not needed")
	tr := x.run.Trace()
	if tr.Count("TouchdownConsumed", "r.d.0.d") != 1 || tr.Count("TouchdownDiscarded", "r.d.0.d") != 0 {
		t.Fatal("a discard after dispatch must not be applied")
	}
	cancelled := false
	for _, a := range x.h.Cancelled {
		cancelled = cancelled || a == x.run.ID()+"/r.d.0.d#1"
	}
	if !cancelled {
		t.Fatalf("the in-flight attempt was not cancelled: %v", x.h.Cancelled)
	}
}
