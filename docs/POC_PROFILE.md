# POC Profile Choices

> **Status:** POC-only. Rows marked **decided** follow owner decisions recorded
> in the language repository's `implementation/CAROUSEL_ENGINE_PLAN.md`
> ("Recorded decisions"). Every other row is a reversible implementation-profile
> choice made so the proof of concept can run; it is not language semantics and
> does not answer an Owner decision. Each choice is named and shown in the trace
> (`RunRequested`) or in the code where it applies.

## Profile identifiers

| Identifier | Area | Where |
|---|---|---|
| `poc-baseline/0` | Scheduler and scope-outcome rules | `runtime/runtime.go` |
| `poc-rational/0` | Host primitive semantics | `host/primitives.go` |
| `poc-sha256-canon/1` | Artifact encoding and hashing | `codebase/codebase.go` |
| `example-0` | Illustrative `@retry` / `@timeout` interpreters | `runtime/policyexamples/` |

## Decisions the POC had to make, and what stays open

| Topic | Open decision | What this POC does | How to change it |
|---|---|---|---|
| Prefetch scope | Carousel 1 | One target per run | — |
| Demand cardinality | Carousel 2, R2 | `--demand ready` (default): the baseline Scheduler explicitly demands every exposed occurrence whose serial predecessors (and its ancestors' predecessors) are satisfied. `--demand manual`: only the Root is demanded; the caller demands the rest. The Carousel never infers demand. All demand goes through one Runtime path; `Carousel.Demand` reports the occurrence state (`accepted`, `already-committed`, `failed`, `withdrawn`, ...). | `runtime.Config.Demand` |
| Window counting | Carousel 3 — **decided** | Counts every published, unconsumed grounded leaf, including ones the Scheduler considers ineligible or holds; the target is compared with that count directly | — |
| Prefetch traversal | Carousel 4 | Exposure order: the first undeduced, undemanded, unblocked occurrence | `carousel.Replenish` |
| Reconfiguration | Carousel 5 | `Run.SetPrefetch` at any time; affects later passes only | — |
| Consumption point | Carousel 6, R10 — **decided** | A Touchdown is consumed when its first attempt is dispatched. Selection and `BeforeAttempt` (including `Hold`) do not consume it; reattempts never re-enter the window. A leaf settled before its first attempt leaves with `TouchdownDiscarded`. | — |
| Resource budgets | Carousel 7 | Only a run step budget (`StepBudgetExceeded`) | `runtime.Config.MaxSteps` |
| Default prefetch | Carousel 8 | `0` in the API and the CLI | — |
| Completion | Carousel 9 | A run with no timeline event and no dispatchable work ends as `RunStuck` with blocking reasons. `PrefetchExhausted` only means no undeduced candidate remains. | — |
| Vessel naming | Carousel 10, R8 | Not used in code | — |
| Stored-Goal invocation | R1 | Not offered. A run always starts from the source unit's prepared Root. | — |
| Eager calls in arguments | R3 | Rejected (`UnsupportedByProfile`) before a run starts, and again if a lazily resolved artifact contains one | — |
| Composite reattempt | R4 | `Reattempt` on a non-leaf target fails the scope with `UnsupportedPolicyTarget` | — |
| Speculative demand on alias change | R5 | Nothing is withdrawn | — |
| Scope outcome rules | R6 | `poc-baseline/0`: see below | `runtime.evaluateParent` |
| Value store owner | R7 | The Runtime owns it; the Carousel reads it through `carousel.ValueSource` | — |
| Policy observation and actions | R9 | Closed action set `Admit`, `Hold`, `StartTimer`, `Reattempt`, `CancelScope`, `FailScope`, `Emit`. `Hold` and `Reattempt` apply only to leaf targets. `SatisfyScope` is not implemented. | `runtime/policy.go` |
| Policy stacking | — | Rejected as `PolicyConflict` unless the registry declares the ordered pair. With a declared pair, events go to each policy in source order and actions are concatenated (POC-only rule). | `Registry.AllowPair` |

## `poc-baseline/0` scope outcomes

| Scope | Satisfied when | Output | Fails when |
|---|---|---|---|
| Goal / inline arrow | its body scope is satisfied | body output | body fails, is cancelled, or is withdrawn |
| Serial | the last element is satisfied | last element's output | any element fails |
| Unkeyed parallel | all children are satisfied | `NoOutput` | any child fails |
| Resolving map | every branch is satisfied with a value | `{key: value}` | any branch fails or yields `NoOutput` |
| Leaf | an attempt succeeds and no policy asks to reattempt | attempt value | the final attempt fails |

When a scope fails or is cancelled, the baseline cancels in-flight attempts
under it, calls `Host.Cancel`, and withdraws undeduced occurrences under it.
Committed deductions are never touched.

**Deferred prefetch failures.** Every deduction failure found by speculative
prefetch is recorded (`DeductionFailed cause=prefetch`,
`DeductionFailureDeferred`) and is surfaced as a scope failure only when the
Scheduler explicitly demands that occurrence (`DeductionFailureSurfaced`),
whatever its readiness. A run that never demands it ends for lack of demand,
not because of the speculative failure (Carousel plan scenario 10).

**Stabilization before time advances.** Each reactor step repeats demand,
deduction, and dispatch until a pass changes nothing, and only then delivers the
next completion or timer. Because consumption happens at dispatch, the
requested Touchdowns are buffered behind in-flight work.

**Run identity.** `Runtime.Start` reserves a run ID in the Codebase (generated
when not given; duplicates are refused). Deduction records carry it and are
keyed by `(runId, occurrenceId)`.

## `poc-rational/0` primitives

- Numbers are exact rationals. `/` and `%` by zero are `PrimitiveError`. `%`
  requires integers.
- `+` also concatenates two Strings.
- `==` and `!=` compare canonical encodings; values of different kinds are
  unequal, not an error.
- `<`, `<=`, `>`, `>=` accept two Numbers or two Strings.
- Runtime value equality and argument digests use the same framed value
  encoding.
- Computed map keys normalize terminating decimals (`1.50` → `1.5`); other
  rationals use `num/den`.

## `poc-sha256-canon/1` artifacts

- Every atom is length-framed (`tag<len>:<bytes>`) and every node carries its
  child count, so the encoding is prefix-free. `/0` concatenated raw map keys
  and could collide; it is retired.
- `ArtifactHash` = SHA-256 over the authored canonical term, including ordered
  policies, the definition name and arity, and **all** top-level value
  bindings of its source unit (a simplification: unrelated value edits change
  the hash).
- `StructureHash` = the same with policies and the name erased.
  `GoalNodeId` = first 16 hex digits of `StructureHash` plus `/arity`.
- Unqualified references stay symbolic `Name/Arity`. Hash-qualified references
  are expanded to one full hash when the unit is stored.

## Occurrence identity and lineage

- Occurrence IDs are structural paths: the Root is `r`, a deducible
  occurrence's body is `<id>.d`, and composite children are `<id>.<index>`.
- Each occurrence carries one lineage (its path of Goal `Name/Arity`
  segments). A shared `GoalNodeId` reached through different paths therefore
  shows its lineage set across its occurrences.
- Inline Goal-arrow stages (`[x] -> ...` inside a composition) are
  occurrences of kind `goal-arrow-stage`. They deduce like Goals, with
  reference kind `inline-arrow`, once their routed input exists. See
  the language repository's `implementation/CAROUSEL_POC_FINDINGS.md` F3.

## Trace

The trace is a POC diagnostic contract. It includes every event in
`implementation/RUNTIME_CONTRACT.md` §11 that the POC can produce, the Carousel
plan events, and the orchestration plan §12 additions, plus:
`DeductionBlocked`, `PrefetchExhausted`, `TouchdownDiscarded`,
`DeductionFailureDeferred`, `DeductionFailureSurfaced`, `EvaluationWaiting`,
`ReattemptScheduled`, `TimerStarted`, `TimerFired`, `TimerIgnored`,
`BackoffElapsed`, `EvaluationCancelRequested`, and `LateCompletionIgnored`.
