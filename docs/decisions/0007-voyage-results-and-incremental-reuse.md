# 0007 — Voyage results and incremental reuse boundary

- Status: proposed
- Date: 2026-09-18
- Owners: on-the-ground
- Runtime profile: future; not implemented by `poc-baseline/0`
- Supersedes: —
- Related experiment: Carousel POC (this repository)
- Related SCP: [SCP-0004 — Voyage Plans and Fully Touchdown Cable artifacts](https://github.com/on-the-ground/subsea_cable_language/blob/main/proposals/0004-voyage-plans-and-touchdown-cable-artifacts.md) (under discussion; link becomes valid after the companion Language PR merges)
- Owner decision required: no for the SCP-0004 language contract; yes if an
  implementation choice would extend portable semantics
- Affected path frozen at: voyage-result persistence and incremental reuse are
  unimplemented pending this ADR's review and its prerequisite decisions

## Context

SCP-0004 separates the authored `.vyg` Voyage Plan from the Cable realized by
deduction. A terminated voyage returns a Fully Touchdown Cable, represented as
an ordered hash list, together with the Root outcome. It also requires enough
provenance to identify which intermediate deductions produced which Cable
segments so later voyages can reuse unaffected work.

The current POC publishes Touchdowns and stores run-scoped deduction records,
but it does not persist a plan, Cable manifest, provenance index, or outcome
journal. Treating the current ledger as a reuse cache would be incorrect:
aliases are observed lazily per occurrence and routed values may come from Host
evaluation.

## Proposed decision

1. Store the outward Cable as a canonical ordered list of grounded
   evaluation-instance descriptor hashes. Preserve duplicates and do not
   collapse instances merely because they share one structural Goal node.
   Compare root-to-leaf vectors of non-negative child ordinals
   lexicographically, comparing ordinal segments numerically rather than as
   decimal strings. Never sort serialized dotted occurrence IDs. Derive the
   ordinals from authored child order, not deduction or execution timing.
2. Keep run IDs, occurrence IDs, positions, lineages, attempts, timestamps, and
   outcomes in a provenance sidecar rather than item identity. Ordered policy
   metadata is also excluded from policy-erased structural item identity and
   retained in occurrence provenance and Scheduler inputs.
3. Build a bidirectional index between intermediate deduction occurrences and
   Cable positions/ranges.
4. Key alias observations by stable structural reference slots. Never collapse
   them into a `Name/Arity -> hash` map and never use run-local occurrence IDs
   as portable slot identity. Record both selected `ArtifactHash` and
   `StructureHash`; compare the latter for structural compatibility while
   committing the new exact artifact selection.
5. Record stable value-slot digests for reductions that depend on routed or
   Host-derived values.
6. Reuse creates new immutable deduction records and records `reusedFrom`; it
   never edits a prior ledger.
7. Structural reuse does not authorize outcome or effect reuse. Skipping Host
   evaluation requires a separately designed and authorized Outcome Journal.
8. Merkle trees, interval indexes, and storage layouts remain implementation
   details. The outward Cable remains the simple ordered hash list.

## Alternatives rejected

| Alternative | Why rejected |
|---|---|
| Put lineage and occurrence identity in every Touchdown hash | Moving an unchanged subtree destroys content reuse and couples identity to one run |
| Use `artifact hash + arguments` as the reuse key | Misses lazy descendant alias observations and value-dependent reductions |
| Use one `Name/Arity -> hash` dependency map | The same name may resolve differently at separate slots in one voyage |
| Include policy metadata in structural item hashes | Violates policy erasure and prevents reuse when topology and leaf content are unchanged |
| Reuse old deduction records directly | Rewrites occurrence identity and makes old voyages unauditable |
| Treat structural equality as permission to skip the Host | Can silently suppress effects or reuse stale outcomes |
| Return a set of hashes | Loses authored order and duplicate occurrences |

## Consequences

- F9 (artifact top-level value closure) is a prerequisite for portable Cable
  identity. Any Cable produced before F9 is decided is experimental and must
  not be used for cross-version reuse decisions.
- Persistent Codebase, decodable artifacts, plan manifests, and immutable
  ledgers precede incremental reuse.
- Pure structural segments can be reused without an Outcome Journal once all
  structural observations match.
- Value-dependent reuse must wait for equal values; avoiding their producer
  evaluations waits for the separate Outcome Journal design.
- The POC must report voyage artifacts and incremental reuse as unsupported
  until the complete result/provenance contract is implemented.
- Required milestones and acceptance tests are maintained in
  [VESSEL_PLAN.md](../VESSEL_PLAN.md).

## Unresolved implementation gate

SCP-0004 must decide whether Cable membership means every published
Touchdown, only consumed/first-dispatched evaluation instances, or only
successful instances. That decision also fixes failed and cancelled voyages
and the canonical empty-list hash. ADR 0007 cannot be accepted and voyage
artifact implementation cannot begin until that language decision is recorded.

## Review gate

Accept this ADR only after reviewers confirm that descriptor identity,
stable-slot construction, and value digest encoding are versioned and that no
Host-result cache is smuggled into structural reuse. Any pressure to change the
portable list shape, deduction identity, lazy alias timing, or component
ownership returns to the language repository as an SCP.
