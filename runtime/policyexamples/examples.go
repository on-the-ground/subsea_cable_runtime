// Package policyexamples contains ILLUSTRATIVE policy interpreters used only
// to exercise the POC policy carrier. They are not accepted Subsea policy
// semantics and are never registered by default. Their names deliberately
// match the illustrative examples in the language documents.
package policyexamples

import (
	"errors"

	"github.com/on-the-ground/subsea_cable_runtime/carousel"
	"github.com/on-the-ground/subsea_cable_runtime/diag"
	"github.com/on-the-ground/subsea_cable_runtime/host"
	"github.com/on-the-ground/subsea_cable_runtime/runtime"
	"github.com/on-the-ground/subsea_cable_runtime/value"
)

func intArg(args []value.Value, def int) (int, error) {
	if len(args) == 0 {
		return def, nil
	}
	if len(args) > 1 || args[0].Kind() != value.KNumber || !args[0].AsRat().IsInt() || args[0].AsRat().Sign() < 0 {
		return 0, errors.New("expects one non-negative integer")
	}
	return int(args[0].AsRat().Num().Int64()), nil
}

// Retry reattempts a failed leaf up to N additional times: @retry(N).
type Retry struct{}

type retryState struct{ used int }

func (Retry) ID() string      { return "retry" }
func (Retry) Version() string { return "example-0" }
func (Retry) Targets() []carousel.Kind {
	return []carousel.Kind{carousel.KindFunction, carousel.KindAnchor}
}
func (Retry) Validate(args []value.Value) error { _, err := intArg(args, 1); return err }
func (Retry) Attach(runtime.PolicyContext) any  { return &retryState{} }
func (Retry) On(ev runtime.PolicyEvent, state any, ctx runtime.PolicyContext) []runtime.Action {
	st := state.(*retryState)
	if ev.Kind != runtime.AttemptOutcome || ev.Outcome.Status != host.Failed {
		return nil
	}
	max, _ := intArg(ctx.Args, 1)
	if st.used >= max {
		return nil
	}
	st.used++
	return []runtime.Action{{Kind: runtime.Reattempt}}
}

// Timeout fails its target scope if it has not settled after N ticks:
// @timeout(N). It observes only its own target.
type Timeout struct{}

func (Timeout) ID() string      { return "timeout" }
func (Timeout) Version() string { return "example-0" }
func (Timeout) Targets() []carousel.Kind {
	return []carousel.Kind{carousel.KindGoal, carousel.KindArrow, carousel.KindSerial, carousel.KindParallel,
		carousel.KindMap, carousel.KindFunction, carousel.KindAnchor}
}
func (Timeout) Validate(args []value.Value) error {
	n, err := intArg(args, -1)
	if err == nil && n <= 0 {
		return errors.New("expects one positive integer tick count")
	}
	return err
}
func (Timeout) Attach(runtime.PolicyContext) any { return nil }
func (Timeout) On(ev runtime.PolicyEvent, _ any, ctx runtime.PolicyContext) []runtime.Action {
	switch ev.Kind {
	case runtime.ScopeOpened:
		n, _ := intArg(ctx.Args, 1)
		return []runtime.Action{{Kind: runtime.StartTimer, Ticks: n, Token: "deadline"}}
	case runtime.TimerFired:
		if ev.Token == "deadline" {
			return []runtime.Action{{Kind: runtime.FailScope,
				Diag: diag.New("PolicyTimeout", diag.Policy, nil, "@timeout elapsed")}}
		}
	}
	return nil
}

// Registry returns a registry with the illustrative interpreters.
func Registry() *runtime.Registry {
	return runtime.NewRegistry().Register(Retry{}).Register(Timeout{})
}
