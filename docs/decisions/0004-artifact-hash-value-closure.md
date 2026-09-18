# 0004 — Top-level values in artifact hashes

- Status: proposed
- Date: 2026-09-17
- Owners: on-the-ground
- Runtime profile: `poc-baseline/0`, `poc-rational/0`, `poc-sha256-canon/1`
- Supersedes: —
- Related experiment: Carousel POC (this repository)
- Related SCP: [Draft: top-level values in artifact identity](https://github.com/on-the-ground/subsea_cable_language/blob/main/proposals/draft-artifact-hash-value-closure.md)
- Owner decision required: yes
- Affected path frozen at: experimental (hash profile `poc-sha256-canon/1`)

## Context

Goal bodies read top-level value bindings; README does not say whether they are part of `ArtifactHash`.

## Decision

Keep the current experimental profile: every artifact captures all value bindings of its source unit, length-framed, in name order. The SCP recommends capturing only the referenced closure; the profile will be bumped if that is accepted. Hashes from this profile are not claimed interoperable.

## Alternatives considered

| Alternative | Advantages | Costs/reasons rejected |
|---|---|---|
| Referenced closure | Precise | Not accepted yet |
| Exclude values | Simple | Different programs could share a hash |

## Boundary check

- Identity question; no routing or alias change.

## Consequences

Unrelated value edits change every hash in a unit, weakening structural sharing across units.

## Verification

`carousel/encoding_test.go`.

## Owner decision record

- Decision requested on: 2026-09-17 (Draft SCP)
- Owner response: pending

## Reversal or supersession

Runs and artifacts produced under this record are identified by the profile
identifiers above. A superseding decision bumps the affected profile version.
