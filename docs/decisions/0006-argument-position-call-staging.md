# 0006 — Value-producing call positions

- Status: accepted
- Date: 2026-09-17
- Owners: on-the-ground
- Runtime profile: `poc-baseline/0`, `poc-rational/0`, `poc-sha256-canon/1`
- Supersedes: the earlier "blocked pending R3" version of this record
- Related experiment: Carousel POC (this repository)
- Related SCP: [SCP-0003 — Explicit staging of value-producing calls](https://github.com/on-the-ground/subsea_cable_language/blob/main/proposals/0003-explicit-value-producing-call-staging.md) (Accepted)
- Owner decision required: no (decided as Owner decision R3)
- Affected path frozen at: — (implemented)

## Context

An eager `Goal(...)` call or an `$anchor(...)` call nested in a value
expression, for example `Root = [x] -> D[C(x)]`, needs an evaluation before
the enclosing deduction can commit. This POC had rejected every eager Goal call
and every value-position Anchor call outside function leaves as
`UnsupportedByProfile` until the language decided.

SCP-0003 decided it:

- A nested call outside a function leaf is invalid source
  (`InvalidStructuralContext`).
- A call that is itself a direct structural occurrence (a Goal-arrow body, a
  serial or parallel element, or a resolving-map branch) is valid.
- The README defines the eager suffix as a timing difference only: `Goal(...)`
  demands its occurrence immediately, and `Goal[...]` waits for demand.

## Decision

- **Validation** (`sema`): a `Goal(...)` or `$anchor(...)` reached in value
  context outside a function leaf reports `InvalidStructuralContext`. This
  covers Goal and Anchor arguments, policy arguments, operator operands
  (including short-circuit operands), lookup keys, ordinary map values,
  top-level value bindings, and the Root's arguments. Primitive expressions
  stay valid. Nested Anchors inside function leaves stay valid Host call sites.
  Goal calls inside function leaves stay invalid.
- **Direct eager stages** (`carousel`): a structural `Goal(...)` becomes an
  ordinary Goal occurrence with explicit arguments, marked `Eager`.
  - Registering it records demand at once and emits `DemandObserved` with
    reason `eager`. This demand comes from the language; it is not Scheduler
    readiness.
  - It deduces, commits, and publishes Touchdowns like any Goal occurrence, and
    it never receives an implicit upstream argument.
  - Its leaves are dispatched by the Scheduler in composition order, as usual.
  - Static `DestructureMismatch` does not depend on the suffix: it is checked
    for `Goal[...]`, `Goal(...)`, and the Root alike. A hash-qualified
    reference is skipped, because validation cannot read the pinned artifact's
    parameters and a local definition of the same `Name/Arity` may differ;
    deduction reports that mismatch.
- **Defensive guard** (`expr`): during deduction, an evaluator without a Host
  Anchor gateway reports `InvalidStructuralContext` (phase `validation`) for
  any nested call. This covers artifacts that bypassed validation.
- **Removed:** every `UnsupportedByProfile` path for R3. Structure-valued
  lookup maps (ADR 0003) remain the only construct blocked by this profile.

## Alternatives considered

| Alternative | Advantages | Costs/reasons rejected |
|---|---|---|
| Hoist nested calls | Accepts more source | Rejected by SCP-0003 |
| Two-phase deduction | No synthetic stages | Rejected by SCP-0003 |
| Treat `Goal(...)` stages exactly like `Goal[...]` | Simpler | Ignores the README's timing rule |
| Eager also bypasses composition order at dispatch | "Evaluate now" read literally | Would let the Carousel or the syntax override Scheduler eligibility; the README says the suffix changes deduction timing |

## Boundary check

- Deduction stays atomic and never waits on a Host evaluation.
- The Carousel never evaluates a leaf; eager only changes when it deduces.
- Validation invents no occurrence; nested calls are rejected, not staged.

## Consequences

- Programs such as `[C(x), [v] -> D[v]]` run.
- Source with nested calls fails validation with a portable error kind.
- Applying the rule showed that the language's `DestructureMismatch` fixture
  itself nested a call. The language fixture was changed to a direct eager call.

## Verification

- `sema/sema_test.go` `TestValueProducingCallPositions`: every forbidden
  position, every valid direct position, function-leaf rules, and static
  destructure on a direct call, a deferred reference, and the Root.
- `sema/sema_test.go` `TestHashQualifiedReferenceSkipsStaticDestructure`.
- `carousel/carousel_test.go` `TestEagerCallIsDemandedOnExposure`.
- `runtime/runtime_test.go` `TestEagerGoalStageRoutesItsValue`.
- Conformance cases `NestedGoalCall.subc` and `NestedAnchorCall.subc`.
- `conformancetest` `TestSemanticCasesReportOnlyTheExpectedKind`, which now
  reads expected kinds from `cases.tsv`.

## Owner decision record

- Decision requested on: 2026-09-17
- Options presented: A hoisting, B two-phase deduction, C explicit stages
- Owner response: C accepted as SCP-0003 (2026-09-17)
- Authorized specification changes: SCP-0003
- Authorized conformance changes: `NestedGoalCall`, `NestedAnchorCall`
