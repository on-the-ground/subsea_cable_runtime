# Vessel implementation plan

> **Status:** living implementation plan, not a contract or proof of completed
> behavior. Accepted language SCPs and Runtime ADRs override this document;
> operational milestone status belongs in the repository issue tracker.
> Language semantics come from the pinned Subsea Cable revision. Runtime design
> choices require ADRs; language-level changes require an SCP and owner decision.

This plan turns the current Carousel POC into an agent-addressable **Vessel**:
the assembled Consumer Runtime that accepts a `.vyg` Voyage Plan and eventually
returns a Fully Touchdown Cable plus the Root outcome. Carousel remains the
only deduction engine.

## 1. Product and component boundary

```text
Agent ── MCP ──┐
CLI ───────────┼── Vessel control plane
Library API ───┘       |
                       +── Frontend
                       +── persistent Codebase + Deduction Ledger
                       +── Carousel (only deduction engine)
                       +── Outcome & Value Store
                       +── Scheduler
                       `── Host ports
```

CLI, MCP, service, and library entry points are adapters over one outward
Vessel contract. A component API may be public for embedding or testing, but it
must remain named and documented as a component surface. It is not an alternate
Vessel and cannot transfer deduction ownership away from Carousel.

The Voyage Plan declares dependency and routing precedence, not wall-clock
order. Scheduler issues demand and decides eligibility. Carousel alone applies
reduction rules and commits deductions. Host supplies primitive semantics and
evaluates grounded leaves.

## 2. Target lifecycle

```text
plan.vyg
  -> check (no commit)
  -> encode artifacts + Voyage Plan manifest
  -> atomic Codebase commit
  -> voyagePlanRef + revision
  -> load only from Codebase
  -> Carousel deduction <-> Scheduler demand <-> Host evaluation
  -> Fully Touchdown Cable + Root outcome + provenance reference
```

The acceptance boundary is persistence, not file parsing: process A commits a
plan and exits; the source file is removed; process B opens only the Codebase
and runs the same plan reference. Later alias rebinding affects only occurrences
that have not yet deduced themselves. Hash-qualified references stay pinned.

## 3. Fully Touchdown Cable

SCP-0004 fixes the portable outward shape:

```text
VoyageResult
  voyagePlanHash
  schedulerProfile
  deductionControlRef
  touchdownCableHash
  touchdownHashes[]
  rootOutcome
  provenanceRef
```

`touchdownHashes` is a flat ordered list of grounded evaluation-instance
descriptors. Order compares the root-to-leaf vector of non-negative child
ordinals lexicographically, with each ordinal compared numerically rather than
as a decimal string. Thus `[0, 2]` precedes `[0, 10]`; implementations never
sort dotted occurrence-ID strings. Each reduction assigns child ordinals from
authored result order, so parallel children retain authored order. Structural sharing does not collapse instances
reached through distinct occurrences or argument tuples. Duplicate content
remains duplicate entries with the same hash. Deduction, Scheduler dispatch,
and Host completion timing never change the list.

`schedulerProfile` identifies the Scheduler semantics for the voyage.
`deductionControlRef` points to canonical provenance for the demand mode,
explicit demand sequence, initial prefetch target, and every later prefetch
reconfiguration. They explain why the same plan may ground a different Cable
without contaminating individual Touchdown content hashes.

Each item hash identifies grounded structural work. It includes the selected
policy-erased `StructureHash` and artifact-local leaf path, leaf kind or Anchor
identifier, canonical arguments, and the applicable encoding/profile identity.
It excludes run IDs, occurrence IDs, list positions, lineages, ordered
policies, timestamps, attempts, and outcomes. Exact `ArtifactHash` selections
and policies remain in deduction/provenance records.

The provenance sidecar provides both directions:

```text
intermediate deduction occurrence -> cable positions or ranges
cable position                     -> deduction ancestry and lineages
```

This keeps the public Cable a simple hashed list while preserving enough
evidence to find which segments an intermediate Goal produced.

## 4. Incremental deduction invariants

Incremental deduction never edits an earlier run. A later voyage commits new
occurrence and deduction records and may point at an immutable prior segment
with `reusedFrom`.

A segment is structurally reusable only when all relevant inputs match:

- selected policy-erased structural identity and canonical arguments;
- language, value encoding, primitive, and artifact profiles;
- every alias observation at a stable reference slot; and
- every required value observation at a stable value slot.

A plain `Name/Arity -> hash` map is invalid as an alias fingerprint. The same
name may be observed at different hashes in different occurrences—even during
one voyage. The portable key is an ordered stable reference slot within the
candidate segment, paired with requested `Name/Arity`, selected `ArtifactHash`,
selected `StructureHash`, and the observed revision. Structural compatibility
uses the stable slot, requested name/arity, and `StructureHash`; the full tuple
remains audit evidence. Run-local occurrence IDs are provenance, not stable
slots.

A policy-only artifact change may reuse the policy-erased structure while the
new voyage commits its newly selected `ArtifactHash`, occurrence policies, and
Scheduler inputs. Policy effects may still cause the voyage to reach a
different set of leaves.

Structural reuse and outcome reuse are separate. A value-dependent segment may
be reused only after equal canonical input values are established. Avoiding the
Host evaluation that would produce those values additionally requires an
authorized Outcome Journal or cache policy. A matching Touchdown hash by itself
never authorizes an effect to be skipped.

Publication-time membership intentionally records all structure grounded by
the voyage, including unused speculative Touchdowns. Cable hashes are therefore
comparable only under the same Scheduler profile and deduction-control history,
not merely the same Voyage Plan. This is an explicit consequence of representing
grounded structure rather than only first-dispatched work.

## 5. Required decisions

| ID | Decision | Owner and record | Gate |
|---|---|---|---|
| D0 | Repository, Go module, binary, and control-plane package rename | Runtime ADR; recommended repository/module/binary `vessel` | M0 |
| D1 | Top-level value closure captured by a Goal artifact | Language owner; existing draft SCP F9 | M1 artifact codec |
| D2 | Whether a run starts only from a committed Voyage Plan or may invoke an arbitrary stored Goal | Language owner; R1 | Public run API |
| D3 | Carousel storage ports | Runtime ADR: artifact read, ledger append, value read | M0 |
| D4 | Decodable artifact encoding profile | Runtime ADR | M1 |
| D5 | Persistent backend and transaction model | Runtime ADR | M1 |
| D6 | Vessel API and authorization model | Runtime ADR | M2 |
| D7 | MCP protocol/SDK/dependency profile | Runtime ADR | M3 |
| D8 | Touchdown descriptor and cable-list encoding profile | Runtime ADR 0007 constrained by SCP-0004 | M2 |
| D9 | Stable reference/value slot representation and reuse index | Runtime ADR 0007; escalate any semantic pressure | M4 |
| D10 | Outcome Journal authority and effect-reuse policy | Owner decision if portable behavior is proposed | M4 |
| D11 | Cable membership and empty-list hash | **Decided in SCP-0004:** every `TouchdownPublished` instance remains a member; failed/cancelled voyages return the accumulated Cable; zero members use the canonical framed empty list | M2 |

The `.vyg` extension, ordered Cable shape, provenance separation, and immutable
reuse records are already owner decisions in SCP-0004; they are not open
Runtime profile choices.

## 6. Milestones

### M0 — Naming and hard boundaries

1. Rename repository, Go module, binary, and control-plane package in a
   behavior-free change after this documentation PR.
2. Split Scheduler ownership from the control plane.
3. Give Carousel narrow `ArtifactSource`, `LedgerSink`, and `ValueSource` ports.
4. Add import-boundary tests: Carousel imports no filesystem, network, Vessel,
   Scheduler, Host implementation, or MCP package.
5. Keep `.vyg` as the only canonical example/conformance extension. The
   pre-1.0 language defines no dual-extension compatibility period.

### M1 — Persistent Codebase

1. Define a Store interface and a shared contract suite for the in-memory and
   persistent implementations.
2. Add a versioned, decodable artifact codec. Spans and source origin remain in
   provenance, outside artifact identity.
3. Commit a Voyage Plan manifest containing the Root reference, captured input
   closure, language revision, and profiles.
4. Implement a filesystem CAS, append-only revisioned alias log, immutable plan
   manifests, persistent deduction ledgers, single-writer locking, atomic
   publication, and format-version refusal.
5. Start production runs from a stored plan reference. Starting from an in-memory
   parsed unit remains a test helper that commits first.
6. Pass the cross-process, deleted-source acceptance test.

The Codebase stores structure and immutable deduction evidence. It must never
store live attempt outcomes or routed Runtime values.

### M2 — Voyage results and control plane

1. Create the Vessel in-process API: check, commit, resolve, inspect, rebind,
   start/cancel voyage, status, trace, result, and capabilities.
2. Make the CLI a thin adapter over that API.
3. Build the canonical Touchdown descriptor and ordered Cable manifest.
4. Persist the bidirectional provenance index and return `VoyageResult`.
5. Add read/commit/run authorization tiers, optimistic alias revisions, and an
   append-only audit log.
6. Report unsupported features and all active profiles through capabilities.

### M3 — Agent MCP

1. Add an MCP stdio adapter exposing the same control-plane operations.
2. Add loopback-only Streamable HTTP with explicit opt-in for non-loopback
   binding, Origin validation, authentication, and authorization tiers.
3. Keep tools outside the granted tier out of tool discovery.
4. Exercise the full agent loop: diagnose invalid plan, fix, commit, resolve,
   run, inspect trace/result, observe a stale-revision conflict, rebind, rerun.

Protocol version and SDK choice are recorded in D7 at implementation time; this
plan deliberately does not freeze a time-sensitive MCP version.

### M4 — Outcome Journal and incremental deduction

1. Persist stable-slot alias and value observations.
2. Compute reusable segments and precise invalidation ranges from provenance.
3. Add an Outcome Journal only after its authority, Host identity, effect
   safety, retention, and invalidation rules are explicit.
4. Create new deductions with `reusedFrom` for valid segments and deduce only
   invalidated sections.
5. Prove that changing one intermediate Goal preserves unrelated segments,
   recomputes affected/downstream value-dependent segments, and never mutates
   prior voyage evidence.

## 7. Acceptance tests

The following tests are gates, not examples:

1. **Restart:** commit in one process, delete source, run by plan reference in
   another process.
2. **Lazy alias:** rebind between deductions; committed occurrences retain the
   old hash and undeduced occurrences observe the new hash.
3. **Cable order:** parallel Host completions are deliberately reversed while
   the Cable list remains in structural order.
4. **Numeric ordinal order:** a 12-branch parallel preserves authored
   `0..11` order and never sorts `10` before `2`.
5. **Prefetch is observable:** the same plan under prefetch `0` and `2` may
   produce different Cables when the larger window publishes unused speculative
   leaves; each result records a different deduction-control reference.
6. **Duplicates:** identical grounded content at two occurrences produces the
   same item hash twice in the list and distinct provenance entries.
7. **Membership:** speculative, withheld, discarded, never-dispatched, failed,
   and cancelled published instances remain in the Cable; unpublished
   occurrences do not. Failed/cancelled voyages return that Cable separately
   from their Root outcome.
8. **Empty Cable:** zero published instances produce the profile-tagged,
   length-framed zero-item list hash, never an absent or `null` hash.
9. **Reverse lookup:** every intermediate deduction maps to its contributed
   positions/ranges, and every list position maps back to ancestry and lineages.
10. **Alias slots:** two references to the same `Name/Arity` resolve at different
   revisions without collapsing in the fingerprint.
11. **Pure reuse:** an unrelated Goal edit reuses an unchanged structural
   segment and emits new records with `reusedFrom`.
12. **Value invalidation:** a changed routed value invalidates the dependent
   segment even when its selected artifact is unchanged.
13. **Effect safety:** structural reuse does not suppress Host invocation without
   explicit Outcome Journal authorization.
14. **Boundary:** Carousel uses only its narrow ports; Codebase bytes contain no
    Host outcome; Scheduler policy cannot rewrite committed topology.

## 8. Change protocol

Implementation convenience is not language semantics. If work reveals a gap:

1. freeze only the affected path;
2. preserve a minimal reproduction and traces;
3. write a proposed Runtime ADR with invariants, alternatives, recommendation,
   compatibility impact, and conformance impact;
4. open a language SCP when syntax, structural semantics, identity, diagnostic
   ownership, policy composition, or component boundaries would change;
5. request the owner's explicit decision; and
6. continue unrelated work only.

No implementation may silently turn a convenient cache key, scheduling rule,
or storage layout into a portable Subsea Cable rule.

## 9. Sequencing

1. Merge the companion language PR and repin this submodule to its merge commit.
2. Merge this documentation/profile PR after its CI passes.
3. Perform M0 as an isolated behavior-free rename/boundary PR.
4. Obtain D1 and D2 before finalizing the persistent artifact and public run
   contracts.
5. Implement M1, then M2 and M3.
6. Treat M4 as a separate correctness project, not an optimization folded into
   storage work.
