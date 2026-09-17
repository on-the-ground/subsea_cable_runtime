# 0003 — Structure-valued lookup maps

- Status: blocked
- Date: 2026-09-17
- Owners: on-the-ground
- Runtime profile: `poc-baseline/0`, `poc-rational/0`, `poc-sha256-canon/1`
- Supersedes: —
- Related experiment: Carousel POC (this repository)
- Related SCP: [Draft: structure-valued lookup maps](https://github.com/on-the-ground/subsea_cable_language/blob/main/proposals/draft-structure-valued-lookup-maps.md)
- Owner decision required: yes
- Affected path frozen at: blocked with `UnsupportedByProfile` in validation and deduction

## Context

README allows an ordinary-map lookup whose entries are Goal structure as a Goal-arrow body, but not where the map is bound, how entries are written, or how routed input reaches the selected entry. The POC previously chose answers silently.

## Decision

Block the path until the SCP is decided. Validation flags `name[key]` in structure position as `UnsupportedByProfile`, and the Carousel refuses to expand it. Value-position lookups in ordinary maps remain supported. A bare identifier in a map entry is a value name, never Goal structure.

## Alternatives considered

| Alternative | Advantages | Costs/reasons rejected |
|---|---|---|
| Keep the earlier POC behavior | Programs keep running | Absorbs an undecided language rule |
| Implement the SCP recommendation early | Useful evidence | Same objection until accepted |

## Boundary check

- Source validity: language question.
- No structural or alias change.

## Consequences

Programs that select structure by value cannot run on this POC for now.

## Verification

`carousel/carousel_test.go` (`TestStructureLookupIsBlocked`, `TestValueLookupStillWorks`); `sema/sema_test.go` (bare entry case).

## Owner decision record

- Decision requested on: 2026-09-17 (Draft SCP)
- Owner response: pending

## Reversal or supersession

Runs and artifacts produced under this record are identified by the profile
identifiers above. A superseding decision bumps the affected profile version.
