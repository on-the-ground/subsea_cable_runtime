# 0001 — Touchdown consumption and window counting

- Status: accepted
- Date: 2026-09-17
- Owners: on-the-ground
- Runtime profile: `poc-baseline/0`, `poc-rational/0`, `poc-sha256-canon/1`
- Supersedes: —
- Related experiment: Carousel POC (this repository)
- Related SCP: [SCP-0001](https://github.com/on-the-ground/subsea_cable_language/blob/carousel-poc/proposals/0001-touchdown-consumption-and-window-counting.md) (Accepted)
- Owner decision required: yes
- Affected path frozen at: — (implemented)

## Context

The Carousel keeps a prefetch window of grounded leaves. Before this decision the POC exposed both dispatch-time and completion-time consumption; the two produced different buffered counts behind in-flight work (N versus N−1).

## Decision

Implement SCP-0001:

- `Carousel.ConsumeTouchdown(occurrence, evaluationInstance, attempt)` is applied once, before the Host is invoked, for the first attempt only; only `consumed` lets the attempt proceed.
- `Carousel.DiscardTouchdown(occurrence, reason)` removes never-attempted leaves when their scope settles.
- The window counts every published leaf without an applied acknowledgement.
- A full window stops only speculative deduction; demanded deduction proceeds and emits `DemandedTouchdownOverTarget`.
- `evaluationInstance` is `runId/occurrenceId/digest(arguments)`.
- The `--consume-at` profile knob is removed.

## Alternatives considered

| Alternative | Advantages | Costs/reasons rejected |
|---|---|---|
| Consume at selection | Most throughput | Earlier commits for withheld leaves |
| Consume at Host start | Exact execution match | Needs a Host acknowledgement |
| Consume at completion | Most late binding | Only N−1 buffered |
| Keep a profile knob | Flexible | Portable behavior would differ between Runtimes |

## Boundary check

- Scheduler/Carousel boundary behavior, fixed by SCP-0001.
- Policy-erased Goal structure is unchanged.
- No alias is resolved earlier than its occurrence's demand.
- Nothing framework-specific.

## Consequences

- Traces carry `instance`, `attempt`, and `window` on Touchdown events.
- A discarded Touchdown never reaches the Host; a boundary rejection fails the leaf with `TouchdownAcknowledgementRejected`.

## Verification

`carousel/carousel_test.go` (`TestDemandIsDeducedOverAFullWindow`, `TestConsumeIsAppliedOnce`, `TestConsumeDiscardRace`); `runtime/window_test.go` and `runtime/review_test.go` (Carousel scenarios 15–19 and 21).

## Owner decision record

- Decision requested on: 2026-09-17
- Options presented: selection, dispatch, Host start, completion; counting all or eligible leaves; target correction; reattempt re-entry
- Agent recommendation: dispatch; all unconsumed leaves; no correction; no re-entry
- Owner response: accepted (2026-09-17)
- Decision date: 2026-09-17
- Authorized specification changes: SCP-0001
- Authorized conformance changes: Carousel plan scenarios 15–22

## Reversal or supersession

Runs and artifacts produced under this record are identified by the profile
identifiers above. A superseding decision bumps the affected profile version.
