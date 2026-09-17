# 0002 — Inline Goal-arrow stages as deducible occurrences

- Status: proposed
- Date: 2026-09-17
- Owners: on-the-ground
- Runtime profile: `poc-baseline/0`, `poc-rational/0`, `poc-sha256-canon/1`
- Supersedes: —
- Related experiment: Carousel POC (this repository)
- Related SCP: [Draft: occurrence kind for inline Goal-arrow stages](https://github.com/on-the-ground/subsea_cable_language/blob/carousel-poc/proposals/draft-inline-goal-arrow-stage-occurrence.md)
- Owner decision required: yes
- Affected path frozen at: experimental (kept running: resolving-map routing depends on it)

## Context

An inline stage such as `[{code, logs}] -> Diagnose[code, logs]` must bind a routed value before its body can be reduced. The Runtime Contract lists no occurrence kind for it.

## Decision

Recommended (not accepted): model the stage as a deducible occurrence of kind `goal-arrow-stage`. It deduces only when demanded and after its input resolves, commits a record with `referenceKind = inline-arrow` and no artifact hash, and adds no lineage segment. The path stays **experimental**: traces and ledgers that involve inline stages are not claimed portable.

## Alternatives considered

| Alternative | Advantages | Costs/reasons rejected |
|---|---|---|
| Expand with a pending input | No new kind | Violates the conservative value barrier |
| Synthetic Goal identity | Reuses records | Invents identities |
| Block the path | Strict | Blocks every program that routes a resolving-map result |

## Boundary check

- Occurrence identity and deduction records: language/Runtime Contract question.
- Policy-erased structure is unchanged.
- Aliases are unaffected (inline stages have none).

## Consequences

Programs using inline stages run, but their ledger shape may change when the SCP is decided.

## Verification

`carousel/carousel_test.go` (`TestValueBarrierBlocksPrefetch`, `TestDestructureMismatchAtDeduction`).

## Owner decision record

- Decision requested on: 2026-09-17 (Draft SCP)
- Owner response: pending

## Reversal or supersession

Runs and artifacts produced under this record are identified by the profile
identifiers above. A superseding decision bumps the affected profile version.
