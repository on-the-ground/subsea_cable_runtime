package runtime_test

import (
	"testing"

	"github.com/on-the-ground/subsea_cable_runtime/carousel"
	"github.com/on-the-ground/subsea_cable_runtime/host"
	"github.com/on-the-ground/subsea_cable_runtime/runtime"
	"github.com/on-the-ground/subsea_cable_runtime/runtime/policyexamples"
	"github.com/on-the-ground/subsea_cable_runtime/value"
)

// holdOnce holds its leaf for a number of ticks before the first attempt.
type holdOnce struct{ ticks int }

type holdState struct{ held bool }

func (h holdOnce) ID() string      { return "holdOnce" }
func (h holdOnce) Version() string { return "test" }
func (h holdOnce) Targets() []carousel.Kind {
	return []carousel.Kind{carousel.KindAnchor, carousel.KindFunction}
}
func (h holdOnce) Validate([]value.Value) error     { return nil }
func (h holdOnce) Attach(runtime.PolicyContext) any { return &holdState{} }
func (h holdOnce) On(ev runtime.PolicyEvent, state any, _ runtime.PolicyContext) []runtime.Action {
	st := state.(*holdState)
	if ev.Kind == runtime.BeforeAttempt && !st.held {
		st.held = true
		return []runtime.Action{{Kind: runtime.Hold, Ticks: h.ticks}}
	}
	return nil
}

// Carousel scenario 16: a held leaf occupies the window, so the Carousel does
// not deduce past the target because of it.
func TestHeldLeafOccupiesTheWindow(t *testing.T) {
	src := "Held = [] -> @holdOnce $held\nS = [n] -> $work(n)\nRoot = [] -> {Held[], S[1], S[2]}\nRoot[]"
	x := start(t, src, runtime.Config{Prefetch: 1, Concurrency: 1, Demand: runtime.DemandManual,
		Policies: runtime.NewRegistry().Register(holdOnce{ticks: 5})})
	x.run.Advance()
	x.run.Demand("r.d.0")
	x.run.Advance()
	car := x.run.Carousel()
	if !car.InWindow("r.d.0.d") || x.run.Trace().Count("EvaluationStarted", "") != 0 {
		t.Fatal("the held leaf must stay in the window without being attempted")
	}
	if car.Window() != 1 || car.Occurrence("r.d.1").State != carousel.Undeduced || car.Occurrence("r.d.2").State != carousel.Undeduced {
		t.Fatalf("prefetch deduced past a full window: window=%d", car.Window())
	}
	res := x.finish()
	if res.Status != host.Succeeded || x.run.Trace().Count("TouchdownConsumed", "r.d.0.d") != 1 {
		t.Fatalf("the held leaf should run after the hold and be consumed once: %+v", res.Diag)
	}
	for _, e := range x.run.Trace().Filter("EvaluationStarted") {
		if e.Occ == "r.d.0.d" && e.Time < 5 {
			t.Fatalf("the held leaf started at t=%d, before its hold ended", e.Time)
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

// Carousel scenario 18: a reattempt neither re-enters the window nor emits a
// second TouchdownConsumed.
func TestReattemptDoesNotReenterTheWindow(t *testing.T) {
	src := "Patch = [d] -> @retry(2) $editFiles(d)\nNext = [] -> $next\nRoot = [] -> {Patch[1], Next[]}\nRoot[]"
	x := start(t, src, runtime.Config{Prefetch: 1, Concurrency: 1, Policies: policyexamples.Registry()})
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
