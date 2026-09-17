// Package diag defines the stable diagnostic shape shared by every POC layer.
//
// Kind and Phase are the machine-readable contract (README "Error ownership").
// Messages are informative only.
package diag

import (
	"fmt"
	"strings"
)

// Phase identifies the layer that owns a diagnostic.
type Phase string

const (
	Source     Phase = "source"
	Validation Phase = "validation"
	Deduction  Phase = "deduction"
	Host       Phase = "host"
	Policy     Phase = "policy"
	// Profile is POC-only. It marks behavior this proof of concept refuses
	// because the language or an owner decision has not specified it yet.
	// It is not part of the language phase set.
	Profile Phase = "profile"
)

// Span is a 1-based source region.
type Span struct {
	Line, Col int
}

func (s Span) String() string { return fmt.Sprintf("%d:%d", s.Line, s.Col) }

// Diagnostic is the POC error value.
type Diagnostic struct {
	Kind       string         `json:"kind"`
	Phase      Phase          `json:"phase"`
	Message    string         `json:"message"`
	Span       *Span          `json:"span,omitempty"`
	Occurrence string         `json:"occurrence,omitempty"`
	Artifact   string         `json:"artifact,omitempty"`
	Lineages   []string       `json:"lineages,omitempty"`
	Details    map[string]any `json:"details,omitempty"`
}

func (d *Diagnostic) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s/%s", d.Phase, d.Kind)
	if d.Span != nil {
		fmt.Fprintf(&b, " at %s", d.Span)
	}
	if d.Occurrence != "" {
		fmt.Fprintf(&b, " (occurrence %s)", d.Occurrence)
	}
	if d.Message != "" {
		fmt.Fprintf(&b, ": %s", d.Message)
	}
	return b.String()
}

// New builds a diagnostic.
func New(kind string, phase Phase, span *Span, format string, args ...any) *Diagnostic {
	return &Diagnostic{Kind: kind, Phase: phase, Span: span, Message: fmt.Sprintf(format, args...)}
}

// At returns a pointer to a copy of s, convenient for New.
func At(s Span) *Span { return &s }

// List collects independent diagnostics.
type List []*Diagnostic

func (l List) Error() string {
	parts := make([]string, len(l))
	for i, d := range l {
		parts[i] = d.Error()
	}
	return strings.Join(parts, "\n")
}

// Kinds returns the kinds in order.
func (l List) Kinds() []string {
	out := make([]string, len(l))
	for i, d := range l {
		out[i] = d.Kind
	}
	return out
}

// Has reports whether kind occurs in the list.
func (l List) Has(kind string) bool {
	for _, d := range l {
		if d.Kind == kind {
			return true
		}
	}
	return false
}

// As extracts a *Diagnostic from err when possible.
func As(err error) (*Diagnostic, bool) {
	if err == nil {
		return nil, false
	}
	if d, ok := err.(*Diagnostic); ok {
		return d, true
	}
	if l, ok := err.(List); ok && len(l) > 0 {
		return l[0], true
	}
	return nil, false
}
