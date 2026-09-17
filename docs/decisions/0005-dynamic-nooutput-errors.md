# 0005 — Runtime NoOutput where a value is required

- Status: proposed
- Date: 2026-09-17
- Owners: on-the-ground
- Runtime profile: `poc-baseline/0`, `poc-rational/0`, `poc-sha256-canon/1`
- Supersedes: —
- Related experiment: Carousel POC (this repository)
- Related SCP: [Draft: errors for NoOutput where a value is required](https://github.com/on-the-ground/subsea_cable_language/blob/carousel-poc/proposals/draft-dynamic-nooutput-errors.md)
- Owner decision required: yes
- Affected path frozen at: experimental (diagnostic kinds only)

## Context

A Host leaf may return `NoOutput` where a value is needed. README names no error for this.

## Decision

Keep the experimental POC kinds `NoOutputBranch` (resolving-map branch) and `NoOutputAsValue` (value position in a function body), both phase `host`, and document them as POC-only. The SCP recommends one kind with a detection-dependent phase.

## Alternatives considered

| Alternative | Advantages | Costs/reasons rejected |
|---|---|---|
| Adopt the SCP recommendation now | Consistent | Not accepted yet |
| Treat as generic Host failure | Simple | Loses the routing cause |

## Boundary check

- Diagnostic ownership: language question.
- No structural change.

## Consequences

Diagnostic kinds may change when the SCP is decided.

## Verification

Covered by existing Runtime tests of Host outcomes; no dedicated conformance yet.

## Owner decision record

- Decision requested on: 2026-09-17 (Draft SCP)
- Owner response: pending

## Reversal or supersession

Runs and artifacts produced under this record are identified by the profile
identifiers above. A superseding decision bumps the affected profile version.
