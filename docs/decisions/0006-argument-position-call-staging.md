# 0006 — Staging of value-producing calls in argument positions

- Status: blocked
- Date: 2026-09-17
- Owners: on-the-ground
- Runtime profile: `poc-baseline/0`, `poc-rational/0`, `poc-sha256-canon/1`
- Supersedes: —
- Related experiment: Carousel POC (this repository)
- Related SCP: [Draft: staging of value-producing calls in argument positions](https://github.com/on-the-ground/subsea_cable_language/blob/carousel-poc/proposals/draft-argument-position-call-staging.md)
- Owner decision required: yes (Runtime orchestration plan R3)
- Affected path frozen at: blocked (`UnsupportedByProfile`)

## Context

An eager `Goal(...)` call or a value-position `$anchor(...)` call can appear
in an argument position outside a function leaf, for example
`Root = [] -> Report[$fetch(1)]`. Evaluating such an argument needs a value
that only a Host evaluation can produce, so the enclosing deduction would have
to wait on an evaluation it has not yet created. The language does not say
whether the call becomes an occurrence of its own (hoisting), whether
deduction runs in two phases, or whether the construct is restricted. Under
the conservative value barrier the POC cannot pick one without changing the
occurrence tree, the deduction ledger, and the trace.

## Decision

Keep the construct blocked:

- `sema` reports `UnsupportedByProfile` before a run starts.
- The Carousel and the expression evaluator report `UnsupportedByProfile` again
  if a lazily resolved artifact contains one, and commit nothing for that
  occurrence.
- No experimental staging ships until the Draft SCP is decided.

## Alternatives considered

| Alternative | Advantages | Costs/reasons rejected |
|---|---|---|
| A. Hoist the call into a preceding occurrence | Keeps deduction pure | Adds occurrences the source does not name; changes occurrence identity and lineage |
| B. Two-phase deduction (stage, then resume after the value) | No new occurrences | A deduction stays open across a Host evaluation; breaks atomic commits |
| C. Restrict the construct in the language | Simplest | Rejects source that README currently allows |
| Pick one experimentally now | Runs more programs | Would anchor the SCP on an implementation choice |

## Boundary check

- Deduction stays atomic and never waits on a Host evaluation.
- The Host is never called during deduction.
- The decision belongs to the language (SCP) and the owner (R3).

## Consequences

Programs using these calls outside function leaves do not run in this POC.
Function-leaf bodies are unaffected: nested `$anchor(...)` calls there go
through the Host Anchor gateway and are not occurrences.

## Verification

`sema/sema_test.go` `TestUnsupportedConstructsAreFlagged`;
`runtime/runtime_test.go` `TestStartRefusesProgramsThePOCCannotRun`.

## Owner decision record

- Decision requested on: 2026-09-17
- Options presented: A, B, C (Draft SCP)
- Agent recommendation: none yet; the Draft SCP lists the trade-offs
- Owner response: pending
- Decision date: —
- Authorized specification changes: none
- Authorized conformance changes: none
