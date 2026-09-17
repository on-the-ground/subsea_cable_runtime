package runtime_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/on-the-ground/subsea_cable_runtime/carousel"
	"github.com/on-the-ground/subsea_cable_runtime/codebase"
	"github.com/on-the-ground/subsea_cable_runtime/host"
	"github.com/on-the-ground/subsea_cable_runtime/runtime"
	"github.com/on-the-ground/subsea_cable_runtime/value"
)

// SCP-0002 component tests: Carousel reads resolved values through a narrow
// read-only port and cannot write outcomes; the Codebase stores no live
// outcomes or routed Runtime values.

var outcomeTypes = []reflect.Type{
	reflect.TypeOf(value.Output{}),
	reflect.TypeOf(host.Outcome{}),
}

// acceptsOutcome reports whether a method signature takes an outcome-bearing
// parameter, which would let a caller hand live outcomes to that component.
func acceptsOutcome(m reflect.Method) bool {
	for i := 1; i < m.Type.NumIn(); i++ {
		in := m.Type.In(i)
		for in.Kind() == reflect.Pointer || in.Kind() == reflect.Slice {
			in = in.Elem()
		}
		for _, t := range outcomeTypes {
			if in == t {
				return true
			}
		}
	}
	return false
}

func TestCarouselValuePortIsReadOnly(t *testing.T) {
	port := reflect.TypeOf((*carousel.ValueSource)(nil)).Elem()
	if port.NumMethod() != 1 {
		t.Fatalf("the value port must expose exactly one read method, has %d", port.NumMethod())
	}
	m := port.Method(0)
	if m.Name != "Output" || m.Type.NumIn() != 1 || m.Type.In(0).Kind() != reflect.String ||
		m.Type.NumOut() != 2 || m.Type.Out(0) != reflect.TypeOf(value.Output{}) {
		t.Fatalf("unexpected value port shape: %s %s", m.Name, m.Type)
	}
	car := reflect.TypeOf(&carousel.Carousel{})
	for i := 0; i < car.NumMethod(); i++ {
		if m := car.Method(i); acceptsOutcome(m) {
			t.Errorf("Carousel.%s accepts an outcome; Carousel must not receive or write outcomes", m.Name)
		}
	}
}

func TestCodebaseHasNoOutcomeWritePath(t *testing.T) {
	cb := reflect.TypeOf(&codebase.Codebase{})
	for i := 0; i < cb.NumMethod(); i++ {
		if m := cb.Method(i); acceptsOutcome(m) {
			t.Errorf("Codebase.%s accepts an outcome; the Codebase must not store live values", m.Name)
		}
	}
}

func TestCodebaseStoresNoLiveOutcomeAfterARun(t *testing.T) {
	const marker = "LIVE-OUTCOME-7f3c"
	src := "Probe = [] -> $probe\nRoot = [] -> {Probe[], $other}\nRoot[]"
	x := start(t, src, runtime.Config{Prefetch: 1})
	x.h.Register("probe", func(host.Call) (value.Output, error) { return value.Of(value.String(marker)), nil })
	res := x.finish()
	if res.Status != host.Succeeded {
		t.Fatalf("%+v", res.Diag)
	}
	if out, ok := x.run.Output("r.d.0.d"); !ok || out.Value.AsString() != marker {
		t.Fatalf("the Runtime store must hold the outcome, got %v %v", out, ok)
	}
	var dump strings.Builder
	for _, rec := range x.cb.Ledger() {
		b, _ := json.Marshal(rec)
		dump.Write(b)
	}
	for _, a := range x.cb.Artifacts() {
		dump.WriteString(a.Canonical)
	}
	if strings.Contains(dump.String(), marker) {
		t.Fatal("a live outcome leaked into the Codebase or Deduction Ledger")
	}
}
