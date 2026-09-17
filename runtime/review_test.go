package runtime_test

import (
	"testing"

	"github.com/on-the-ground/subsea_cable_runtime/carousel"
	"github.com/on-the-ground/subsea_cable_runtime/codebase"
	"github.com/on-the-ground/subsea_cable_runtime/host"
	"github.com/on-the-ground/subsea_cable_runtime/runtime"
	"github.com/on-the-ground/subsea_cable_runtime/sema"
)

// Review #2 and Carousel scenario 15: on the ordinary Step/RunToCompletion
// path, the window is refilled before time advances, and because a Touchdown is
// consumed at dispatch (R10), N Touchdowns are buffered behind the in-flight
// leaf.
func TestPrefetchWindowIsRefilledBehindInFlightWork(t *testing.T) {
	src := "S = [n] -> $work(n)\nRoot = [] -> [S[1], S[2], S[3], S[4], S[5]]\nRoot[]"
	x := start(t, src, runtime.Config{Prefetch: 2, Concurrency: 1})
	x.h.SetTicks("work", 5)
	res := x.finish()
	if res.Status != host.Succeeded {
		t.Fatalf("%+v", res.Diag)
	}
	window, inflight, checked := 0, 0, 0
	for _, e := range x.run.Trace().Events {
		switch e.Kind {
		case "TouchdownPublished":
			window++
		case "TouchdownConsumed", "TouchdownDiscarded":
			window--
		case "EvaluationStarted":
			inflight++
		case "EvaluationSucceeded", "EvaluationFailed":
			// The first two completions still have at least two later steps.
			if checked < 2 {
				if inflight != 1 || window != 2 {
					t.Fatalf("t=%d: inflight=%d window=%d, want 1 in flight and 2 buffered", e.Time, inflight, window)
				}
				checked++
			}
			inflight--
		}
	}
	if checked != 2 {
		t.Fatalf("checked %d completions", checked)
	}
}

// Review #3: ledger identity is (runId, occurrenceId).
func TestTwoRunsShareOneCodebase(t *testing.T) {
	cb := codebase.New()
	src := []byte("Root = [x] -> $echo(x)\nRoot[1]")
	var runs []*runtime.Run
	for i := 0; i < 2; i++ {
		u, err := sema.Check(src, cb)
		if err != nil {
			t.Fatal(err)
		}
		h := host.NewScripted()
		h.Fallback = host.Echo
		r, err := runtime.Start(runtime.Config{Host: h, Codebase: cb}, u)
		if err != nil {
			t.Fatal(err)
		}
		if res := r.RunToCompletion(); res.Status != host.Succeeded {
			t.Fatalf("run %d: %+v", i, res.Diag)
		}
		runs = append(runs, r)
	}
	if runs[0].ID() == runs[1].ID() {
		t.Fatal("runs share an identity")
	}
	a, okA := cb.GetDeduction(runs[0].ID(), "r")
	b, okB := cb.GetDeduction(runs[1].ID(), "r")
	if !okA || !okB || a.Seq == b.Seq || a.RunID == b.RunID {
		t.Fatalf("records not run-scoped: %+v / %+v", a, b)
	}
	if len(runs[0].Ledger()) != 1 || len(runs[1].Ledger()) != 1 || len(cb.Ledger()) != 2 {
		t.Fatalf("ledgers: %d %d %d", len(runs[0].Ledger()), len(runs[1].Ledger()), len(cb.Ledger()))
	}
	u, _ := sema.Check(src, cb)
	if _, err := runtime.Start(runtime.Config{RunID: runs[0].ID(), Host: host.NewScripted(), Codebase: cb}, u); err == nil {
		t.Fatal("a duplicate run identity must be refused")
	}
}

// Review #5: with manual demand, a failure found by prefetch never fails the
// run until the Scheduler demands that occurrence.
func TestManualDemandDefersSpeculativeFailure(t *testing.T) {
	src := "S = [n] -> $work(n)\nRoot = [] -> [S[1], S[1 / 0]]\nRoot[]"

	x := start(t, src, runtime.Config{Demand: runtime.DemandManual, Prefetch: 2})
	x.run.Advance()
	if x.run.Carousel().Occurrence("r.d.1").State != carousel.Failed {
		t.Fatal("prefetch should have found the failure")
	}
	if res := x.run.Result(); res != nil {
		t.Fatalf("speculative failure ended the run: %+v", res.Diag)
	}
	x.run.Demand("r.d.0")
	res := x.finish()
	if res.Diag == nil || res.Diag.Kind != "RunStuck" {
		t.Fatalf("expected the run to stop only for lack of demand, got %+v", res.Diag)
	}
	if x.run.Trace().Count("EvaluationSucceeded", "r.d.0.d") != 1 || x.run.Trace().Count("EvaluationCancelRequested", "") != 0 {
		t.Fatal("the demanded leaf was disturbed by the undemanded failure")
	}

	y := start(t, src, runtime.Config{Demand: runtime.DemandManual, Prefetch: 2})
	y.run.Advance()
	if got := y.run.Demand("r.d.1"); got != carousel.DemandFailedEarlier {
		t.Fatalf("demand result %s", got)
	}
	if res := y.run.Result(); res == nil || res.Diag.Kind != "PrimitiveError" {
		t.Fatalf("the failure must surface on demand, got %+v", res)
	}
}
