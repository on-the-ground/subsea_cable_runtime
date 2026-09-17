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

- `Carousel.ConsumeTouchdown(occurrence, evaluationInstance, attempt)` is applied once, before the Host is invoked, for the first attempt only. Results: `consumed`; `consumed-replayed` (same attempt, no event); `already-consumed` with the original attempt (different attempt); `discarded`; `invalid-attempt` (empty id); `mismatch`; `unknown`. Only `consumed` and `consumed-replayed` (`AckResult.Authorizes`) let the attempt proceed; the Scheduler's attempt record keeps the Host call to one per attempt. Any other result aborts the attempt (`AttemptAborted`) without calling the Host and settles the leaf with `TouchdownAcknowledgementRejected`.
- The Carousel keeps an explicit per-Touchdown state (buffered, consumed by an attempt, discarded).
- `Carousel.DiscardTouchdown(occurrence, reason)` removes never-attempted leaves when their scope settles.
- The window counts every published leaf without an applied acknowledgement.
- A full window stops only speculative deduction; demanded deduction proceeds and emits `DemandedTouchdownOverTarget` when it publishes at least one leaf and the window count after it exceeds the target.
- The SCP-0001 trace events carry typed fields (`runId`, `occurrenceId`, `evaluationInstanceId`, `attemptId`, `reason`, `windowCount`, `target`); `detail` is auxiliary.
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

| Scenario (Carousel plan) | Tests |
|---|---|
| 15 Dispatch consumption | `runtime/review_test.go` `TestPrefetchWindowIsRefilledBehindInFlightWork` |
| 16 Withheld first dispatch | `runtime/window_test.go` `TestWithheldLeafOccupiesTheWindow` |
| 17 Ineligible leaf | `runtime/window_test.go` `TestIneligibleLeafCountsTowardTheWindow` |
| 18 Subsequent attempt | `runtime/window_test.go` `TestReattemptDoesNotReenterTheWindow` |
| 19 Demand over the target | `carousel/carousel_test.go` `TestDemandIsDeducedOverAFullWindow`, `TestDemandOverTargetFromPartialWindow`, `TestDemandWithinTargetIsNotOverTarget`; `runtime/window_test.go` `TestDemandBeatsAFullWindow` |
| 20 Consume replay | `carousel/carousel_test.go` `TestConsumeIsAppliedOnce`; `runtime/window_test.go` `TestReplayedConsumeAuthorizesTheSameAttemptOnce` |
| 21 Discard before dispatch | `carousel/carousel_test.go` `TestConsumeDiscardRace`; `runtime/window_test.go` `TestDiscardedTouchdownNeverReachesTheHost` |
| 22 Dispatch/cancellation race | `carousel/carousel_test.go` `TestConsumeDiscardRace` (both orders); `runtime/window_test.go` `TestDiscardedTouchdownNeverReachesTheHost` (discard first) and `TestDiscardAfterDispatchCancelsTheAttempt` (consume first) |
| 23 Competing first dispatch | `carousel/carousel_test.go` `TestConsumeIsAppliedOnce`; `runtime/window_test.go` `TestCompetingConsumeAbortsWithoutCallingTheHost` |
| Trace fields | `runtime/trace_test.go` `TestTouchdownEventsHaveTypedFields` (JSONL round trip) |

Scenario 22 is covered by ordering, not by concurrency: the POC reactor is
serialized, so the Carousel applies acknowledgements one at a time and the
tests exercise each order explicitly. No test runs the two acknowledgements on
separate threads.

## Owner decision record

- Decision requested on: 2026-09-17
- Options presented: selection, dispatch, Host start, completion; counting all or eligible leaves; target correction; reattempt re-entry
- Agent recommendation: dispatch; all unconsumed leaves; no correction; no re-entry
- Owner response: accepted (2026-09-17)
- Decision date: 2026-09-17
- Authorized specification changes: SCP-0001
- Authorized conformance changes: Carousel plan scenarios 15–23

## Reversal or supersession

Runs and artifacts produced under this record are identified by the profile
identifiers above. A superseding decision bumps the affected profile version.
