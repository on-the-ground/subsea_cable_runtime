package runtime_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/on-the-ground/subsea_cable_runtime/runtime"
)

// The SCP-0001 events carry their required fields as typed JSON members, and
// the JSONL trace round-trips without relying on Detail.
func TestTouchdownEventsHaveTypedFields(t *testing.T) {
	src := "Held = [] -> $held\nS = [n] -> $work(n)\nRoot = [] -> {Held[], S[1]}\nRoot[]"
	x := start(t, src, runtime.Config{Prefetch: 1, Demand: runtime.DemandManual, Hooks: withhold("r.d.0.d", 2)})
	x.run.Advance()
	x.run.Demand("r.d.0")
	x.run.Advance()
	x.run.Demand("r.d.1")
	x.run.Advance()
	x.run.CancelScope("r.d.1", "not needed")
	x.finish()

	var buf bytes.Buffer
	if err := x.run.Trace().WriteJSONL(&buf); err != nil {
		t.Fatal(err)
	}
	required := map[string][]string{
		"TouchdownPublished":          {"runId", "occ", "evaluationInstanceId", "windowCount"},
		"TouchdownConsumed":           {"runId", "occ", "evaluationInstanceId", "attemptId", "windowCount"},
		"TouchdownDiscarded":          {"runId", "occ", "evaluationInstanceId", "reason", "windowCount"},
		"DemandedTouchdownOverTarget": {"runId", "occ", "windowCount", "target"},
	}
	seen := map[string]bool{}
	var round []runtime.TraceEvent
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var raw map[string]any
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			t.Fatal(err)
		}
		kind := raw["kind"].(string)
		for _, f := range required[kind] {
			if _, ok := raw[f]; !ok {
				t.Errorf("%s lacks %q: %s", kind, f, line)
			}
		}
		if required[kind] != nil {
			seen[kind] = true
		}
		var e runtime.TraceEvent
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatal(err)
		}
		round = append(round, e)
	}
	for k := range required {
		if !seen[k] {
			t.Errorf("trace has no %s event", k)
		}
	}
	orig := x.run.Trace().Events
	if len(round) != len(orig) {
		t.Fatalf("round trip lost events: %d vs %d", len(round), len(orig))
	}
	for i := range orig {
		a, _ := json.Marshal(orig[i])
		b, _ := json.Marshal(round[i])
		if !bytes.Equal(a, b) {
			t.Fatalf("event %d changed in round trip:\n%s\n%s", i, a, b)
		}
		if orig[i].Seq != i+1 {
			t.Fatalf("sequence not monotonic at %d", i)
		}
	}
	// A window count of zero is still emitted.
	zero := false
	for _, e := range orig {
		if e.WindowCount != nil && *e.WindowCount == 0 {
			zero = true
		}
	}
	if !zero {
		t.Error("no event reported a zero window count")
	}
}
