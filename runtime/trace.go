package runtime

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/on-the-ground/subsea_cable_runtime/codebase"
	"github.com/on-the-ground/subsea_cable_runtime/diag"
)

// TraceEvent is one normalized, monotonically ordered observation. Event names
// are a POC diagnostic contract, not Subsea source syntax.
//
// The SCP-0001 events (TouchdownPublished, TouchdownConsumed,
// TouchdownDiscarded, DemandedTouchdownOverTarget) carry their required fields
// as typed members; Detail is an auxiliary human-readable rendering and is not
// part of the contract.
type TraceEvent struct {
	Seq   int    `json:"seq"`
	Time  int    `json:"t"`
	Kind  string `json:"kind"`
	RunID string `json:"runId,omitempty"`
	Occ   string `json:"occurrenceId,omitempty"`
	// EvaluationInstanceID, AttemptID, and Reason are set when the event
	// defines them.
	EvaluationInstanceID string `json:"evaluationInstanceId,omitempty"`
	AttemptID            string `json:"attemptId,omitempty"`
	Reason               string `json:"reason,omitempty"`
	// WindowCount is the window count after the event; a pointer so that a
	// count of zero is still emitted.
	WindowCount *int `json:"windowCount,omitempty"`
	// Target is the prefetch target (DemandedTouchdownOverTarget).
	Target *int                      `json:"target,omitempty"`
	Detail string                    `json:"detail,omitempty"`
	Record *codebase.DeductionRecord `json:"record,omitempty"`
	Diag   *diag.Diagnostic          `json:"diag,omitempty"`
}

// Trace is an append-only event log.
type Trace struct {
	Events []TraceEvent
}

func (t *Trace) add(now int, kind, occ, detail string) {
	t.Events = append(t.Events, TraceEvent{Seq: len(t.Events) + 1, Time: now, Kind: kind, Occ: occ, Detail: detail})
}

func (t *Trace) addEvent(e TraceEvent) {
	e.Seq = len(t.Events) + 1
	t.Events = append(t.Events, e)
}

func (t *Trace) addDiag(now int, kind, occ, detail string, d *diag.Diagnostic) {
	t.add(now, kind, occ, detail)
	t.Events[len(t.Events)-1].Diag = d
}

func (t *Trace) addRecord(now int, kind, occ, detail string, rec *codebase.DeductionRecord) {
	t.add(now, kind, occ, detail)
	t.Events[len(t.Events)-1].Record = rec
}

// Kinds returns event kinds in order.
func (t *Trace) Kinds() []string {
	out := make([]string, len(t.Events))
	for i, e := range t.Events {
		out[i] = e.Kind
	}
	return out
}

// Filter returns events of the given kinds.
func (t *Trace) Filter(kinds ...string) []TraceEvent {
	want := map[string]bool{}
	for _, k := range kinds {
		want[k] = true
	}
	var out []TraceEvent
	for _, e := range t.Events {
		if want[e.Kind] {
			out = append(out, e)
		}
	}
	return out
}

// Count returns how many events of kind occurred for occ ("" matches any).
func (t *Trace) Count(kind, occ string) int {
	n := 0
	for _, e := range t.Events {
		if e.Kind == kind && (occ == "" || e.Occ == occ) {
			n++
		}
	}
	return n
}

// WriteText renders a compact human-readable trace.
func (t *Trace) WriteText(w io.Writer) {
	for _, e := range t.Events {
		line := fmt.Sprintf("%4d t=%-3d %-24s %-14s %s", e.Seq, e.Time, e.Kind, e.Occ, e.Detail)
		if e.Record != nil {
			r := e.Record
			hash := r.ArtifactHash
			if len(hash) > 12 {
				hash = hash[:12]
			}
			if r.ReferenceKind == "inline-arrow" {
				line += fmt.Sprintf(" [inline-arrow/%d args=(%s) -> %s]", r.RequestedArity, strings.Join(r.Arguments, ", "), r.Result)
			} else {
				line += fmt.Sprintf(" [%s %s/%d hash=%s rev=%d args=(%s) -> %s]", r.ReferenceKind, r.RequestedName,
					r.RequestedArity, hash, r.CodebaseRevision, strings.Join(r.Arguments, ", "), r.Result)
			}
		}
		if e.Diag != nil {
			line += " !" + e.Diag.Error()
		}
		fmt.Fprintln(w, strings.TrimRight(line, " "))
	}
}

// WriteJSONL renders one JSON object per line.
func (t *Trace) WriteJSONL(w io.Writer) error {
	enc := json.NewEncoder(w)
	for _, e := range t.Events {
		if err := enc.Encode(e); err != nil {
			return err
		}
	}
	return nil
}
