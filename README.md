# Subsea Cable Runtime — Carousel POC

> **Status:** proof of concept. This is an independent implementation
> experiment for the [Subsea Cable](https://github.com/on-the-ground/subsea_cable_language)
> language. It is **not** an official Runtime, it does not define language
> semantics, and it does not resolve any open Owner decision. Every behavior
> below that is not normative in the language repository is a named, versioned
> POC profile choice.

It implements the Carousel deduction engine and a Runtime that coordinates it
with the Host, keeping `@policy` as opaque metadata. Language-level questions
found here are recorded as ADRs in [docs/decisions](docs/decisions/README.md)
and as Subsea Cable Proposals in the language repository, indexed by its
`implementation/CAROUSEL_POC_FINDINGS.md`.

## Language pin

The language is pinned as the `language` git submodule. The conformance tests
read `language/conformance/`; set `SUBSEA_LANGUAGE_DIR` to test against another
checkout.

```sh
git clone --recurse-submodules https://github.com/on-the-ground/subsea_cable_runtime.git
# or, in an existing clone:
git submodule update --init
```

| Contract | Revision |
|---|---|
| Grammar, conformance corpus, `README.md` semantics | `on-the-ground/subsea_cable_language@cbc6f53` (the `language` submodule) |
| Design documents followed (`implementation/CAROUSEL_ENGINE_PLAN.md`, `implementation/RUNTIME_ORCHESTRATION_PLAN.md`, `proposals/0001-*`) | `on-the-ground/subsea_cable_language@d48e640` (PR #1, not yet merged; CI also tests this pull request head) |
| Profiles | `poc-baseline/0`, `poc-rational/0`, `poc-sha256-canon/1` |

The design documents are not in the pinned revision yet. When PR #1 merges,
the submodule moves to the merge commit and the two rows become one.

| Path | What it is |
|---|---|
| [docs/POC_PROFILE.md](docs/POC_PROFILE.md) | Every profile choice this POC makes, and how it maps to open decisions |
| [docs/decisions](docs/decisions/README.md) | Implementation ADRs; experimental and blocked paths |
| `syntax/` | UTF-8 decoding, the conformance preprocessing pass, and a recursive-descent parser following `SubseaCable.g4` |
| `sema/` | Structural validation and Unit preparation |
| `codebase/` | In-memory artifacts, mutable `Name/Arity` index, revisions, run-scoped deduction ledger |
| `expr/`, `value/` | Value model and value-expression evaluation (Subsea rules + Host primitives) |
| `carousel/` | **The Carousel**: demand-time deduction, atomic commits, frontier, lineage, Touchdown window |
| `host/` | Host Port, the `poc-rational/0` primitive profile, and a scripted recording Host |
| `runtime/` | **The Runtime**: Scheduler, scope tracking, Outcome & Value Store, policy engine, reactor, trace |
| `cmd/subc-poc/` | CLI to check and run programs |
| `examples/` | Runnable programs |
| `conformancetest/` | Runs the pinned language's `conformance/cases.tsv` |
| `language/` | Pinned language repository (submodule) |

## Quick start

Requires Go 1.24 or newer. No third-party modules.

```sh
go test ./...
go run ./cmd/subc-poc check examples/fix.subc
go run ./cmd/subc-poc run --prefetch 1 --fail editFiles=1 --reattempt editFiles=2 examples/fix.subc
go run ./cmd/subc-poc run --prefetch 2 --ticks work=2 --concurrency 1 examples/prefetch.subc
```

`run` prints the normalized trace (`--jsonl` for JSON lines). Anchors without a
registered implementation use an echo Host that returns `name(args)`.

CLI flags:

| Flag | Meaning |
|---|---|
| `--prefetch N` | additional Touchdowns to keep ahead of evaluation |
| `--concurrency N` | maximum in-flight attempts |
| `--demand ready\|manual` | baseline Scheduler demand mode |
| `--reattempt leaf=N` | Scheduler test double: create up to N further attempts of a failed leaf (not a policy) |
| `--fail leaf=N` | make the next N direct attempts of a leaf fail |
| `--ticks leaf=N` | virtual duration of a leaf |

## How a run works

```text
run(program)                         Runtime.Start: commit unit, prepare Root, demand Root
  │
  ├─ pump ──────────────────────────  Scheduler issues explicit demand
  │    └─ Carousel.Replenish          deduce demanded occurrences, then prefetch
  │         alias → hash → reduce → commit → expose children → publish Touchdowns
  │
  ├─ dispatch ──────────────────────  eligible leaves → consume acknowledgement → Host
  │    └─ Host.EvaluateFunction / Host.InvokeAnchor (nested $ calls via gateway)
  │
  └─ deliver next timeline event ───  completion or timer (virtual clock)
       └─ settle scope → propagate outcome upward
            → Value Store resolves the output → value barriers lift → pump again
```

The reactor is single-threaded with a virtual clock, so every run and trace is
deterministic.

### Invariants the tests enforce

- An unqualified reference resolves its alias only when its own occurrence is
  demanded; a committed deduction never retargets (`conformance/DEDUCTION.md` 1–4).
- A failed deduction commits nothing (6). Primitive operators run during
  deduction through the Host profile and create no Goal node (7).
- Demand comes only from the Scheduler. Dependency readiness never creates
  demand inside the Carousel.
- The conservative value barrier: an occurrence with an unresolved routed input
  commits no deduction and publishes no Touchdown.
- Prefetch keeps a Touchdown **set** at a target, with recorded atomic
  overshoot and reported blocking reasons. Lowering it never discards
  anything.
- A later attempt never re-deduces. `@policy` stays opaque, ordered
  occurrence metadata: no concrete interpreters ship, so every policy fails its
  own scope with `UnknownPolicy` when its occurrence is disclosed, before it
  executes. The interpreter interface exists only as an experimental carrier
  probe for tests (Owner decision R9 is open).
- Nested Anchor calls in function leaves are traced through a gateway and are
  not occurrences.
- The Carousel never calls the Host's function or Anchor capabilities.
- A Touchdown leaves the window exactly once, through a consume
  acknowledgement applied before the Host is invoked for its first attempt, or
  through a discard acknowledgement if it will never be attempted (SCP-0001).
  The window counts every published leaf without an applied acknowledgement,
  including ineligible and withheld ones. A full window stops only speculative
  deduction; demanded work is always deduced.
- Before virtual time advances, the Runtime repeats demand, deduction, and
  dispatch until nothing changes, so the window is refilled behind in-flight
  work.
- A failure found by speculative prefetch never ends a run by itself; it
  surfaces only when the Scheduler demands that occurrence.
- Deduction records are keyed by `(runId, occurrenceId)`; many runs can share
  one Codebase.
- Canonical encodings are length-framed and collision-tested.

`go test ./...` covers the whole pinned conformance corpus, `conformance/DEDUCTION.md`
scenarios 1–4 and 6–8 (5 only partially: no resume), Carousel plan scenarios
1–13 and 15–22 (14, replay, is not implemented), and orchestration plan §16
scenarios 1–3 and 5–11 (scenario 4 is covered with a carrier probe).

## What this POC does not do

- **Concrete policy semantics.** None ship; see `POLICY_DISCOVERY.md` in the
  language repository.
- **Structure-valued lookup maps** are blocked (`UnsupportedByProfile`, ADR 0003).
- **Eager `Goal(...)` calls and value-position `$anchor(...)` calls** outside
  function leaves are rejected with `UnsupportedByProfile`. Their staging is
  Owner decision R3 (ADR 0006).
- **Crash/resume and exact replay** from the ledger are not implemented.
- **Concurrent deduction** (plan Phase 5) is not implemented; the reactor is
  serialized.
- **Experimental paths:** inline Goal-arrow stages (ADR 0002), artifact value
  capture (ADR 0004), and runtime `NoOutput` diagnostics (ADR 0005) may change
  when their proposals are decided.
- **Cross-runtime hashes**: artifact hashes use the POC-only
  `poc-sha256-canon/1` encoding.
- **Recursion** stays unsupported (static and dynamic `CycleDetected`).

## Continuous integration

- `test.yml` runs gofmt, vet, tests, and the examples against the pinned
  language revision on every push and pull request. While language PR #1 is
  open, a second job runs the same tests against that pull request's head
  (`refs/pull/1/head`), so the design documents and conformance changes this
  POC follows are exercised before they merge. The job is removed once the
  submodule moves to the merge commit (see the merge checklist below).
- `language-drift.yml` runs the same tests against the language repository's
  `main` branch every day and on demand, so grammar, conformance, or contract
  changes that the parser has not caught up with are reported even when this
  repository does not change.

## License and security

Licensed under the [Apache License 2.0](LICENSE). Report vulnerabilities
privately as described in [SECURITY.md](SECURITY.md).

## Merge checklist (language PR #1)

1. Merge this repository's pending pull request while the pin is still
   `cbc6f53` and both CI jobs are green.
2. Merge `on-the-ground/subsea_cable_language#1`.
3. In a follow-up pull request here: move the `language` submodule to the
   language merge commit, change `carousel-poc` links to `main`, merge the two
   revision rows above into one, and remove the language-PR-head CI job.
4. Merge the follow-up only when `test.yml` is green on the new pin.
