# Subsea Cable Runtime — a Vessel POC

> **Status:** proof of concept. This is an independent implementation
> experiment for the [Subsea Cable](https://github.com/on-the-ground/subsea_cable_language)
> language. It is **not** an official Runtime, it does not define language
> semantics, and it does not resolve any open Owner decision. Every behavior
> below that is not normative in the language repository is a named, versioned
> POC profile choice.

This repository is one **Vessel**: the complete Consumer Runtime that
[SCP-0002](https://github.com/on-the-ground/subsea_cable_language/blob/main/proposals/0002-carousel-runtime-boundaries.md)
names. It carries the **Carousel**, the only deduction engine, together with the
Codebase, the Outcome & Value Store, the Scheduler, and the Host port, and keeps
`@policy` as opaque metadata. Vessel names that whole; no package or interface
here is a second deduction engine. Language-level questions
found here are recorded as ADRs in [docs/decisions](docs/decisions/README.md)
and as Subsea Cable Proposals in the language repository, indexed by its
`implementation/CAROUSEL_POC_FINDINGS.md`.

The Vessel accepts a `.vyg` **Voyage Plan**. Under
[SCP-0004](https://github.com/on-the-ground/subsea_cable_language/blob/main/proposals/0004-voyage-plans-and-touchdown-cable-artifacts.md),
its eventual outward result is a Fully Touchdown Cable—an ordered list of
grounded leaf content hashes—plus the Root outcome. This POC does not implement
that result artifact or incremental segment reuse yet; the staged work and its
non-negotiable boundaries are recorded in
[docs/VESSEL_PLAN.md](docs/VESSEL_PLAN.md). The SCP link becomes valid after
the companion Language PR merges.

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
| Grammar, conformance corpus, `README.md` semantics, and accepted SCPs | `on-the-ground/subsea_cable_language@86997d3` (the `language` submodule; companion language PR #5) |
| Profiles | `poc-baseline/0`, `poc-rational/0`, `poc-sha256-canon/1` |

| Path | What it is |
|---|---|
| [docs/POC_PROFILE.md](docs/POC_PROFILE.md) | Every profile choice this POC makes, and how it maps to open decisions |
| [docs/VESSEL_PLAN.md](docs/VESSEL_PLAN.md) | Staged product rename, persistent Codebase, voyage artifacts, incremental deduction, and agent MCP plan |
| [docs/decisions](docs/decisions/README.md) | Implementation ADRs; experimental and blocked paths |
| `syntax/` | UTF-8 decoding, the conformance preprocessing pass, and a recursive-descent parser following `SubseaCable.g4` |
| `sema/` | Structural validation and Unit preparation |
| `codebase/` | In-memory artifacts, mutable `Name/Arity` index, revisions, run-scoped deduction ledger |
| `expr/`, `value/` | Value model and value-expression evaluation (Subsea rules + Host primitives) |
| `carousel/` | **The Carousel**: demand-time deduction, atomic commits, frontier, lineage, Touchdown window |
| `host/` | Host Port, the `poc-rational/0` primitive profile, and a scripted recording Host |
| `runtime/` | Vessel control plane: run lifecycle, Scheduler, scope tracking, Outcome & Value Store, policy carrier, reactor, trace. It holds no deduction logic |
| `cmd/subc-poc/` | CLI to check and run programs |
| `examples/` | Runnable programs |
| `conformancetest/` | Runs the pinned language's `conformance/cases.tsv` |
| `language/` | Pinned language repository (submodule) |

## Roles

SCP-0002 fixes who owns what. This repository maps those boxes onto packages:

| SCP-0002 box | Here | Owns |
|---|---|---|
| Vessel (the whole Consumer Runtime) | this repository; currently exposed through `cmd/subc-poc`, with MCP and an in-process API planned as peer adapters | assembling the parts, voyage/run lifecycle, and the outward product contract |
| Frontend | `syntax/`, `sema/` | decoding, parsing, validation, Unit preparation |
| Codebase + Deduction Ledger | `codebase/` | artifacts, the `Name/Arity` index, revisions, deduction records |
| Carousel | `carousel/` | demand-time alias resolution, reduction, atomic commits, frontier, lineage, Touchdown publication |
| Outcome & Value Store | `runtime/` | attempt outcomes and scope outputs; Carousel only reads through a one-method port |
| Scheduler | `runtime/` | demand, eligibility, attempts, cancellation, the opaque `@policy` carrier |
| Host Port | `host/` | primitive semantics, function-leaf evaluation, Anchor invocation |

Two names in that table are historical and will move: the `runtime/` package is
the Vessel's control plane rather than the Vessel itself, and the Scheduler
inside it is not yet its own package. Renaming this repository, its module, and
its binary to `vessel`, and splitting the Scheduler out, is planned as a
separate change with no behavior difference. Until then, read `runtime/` as
"control plane", never as "the whole runtime".

The implemented parts of this boundary are checked by focused tests: the
Carousel value port is read-only, no Carousel or Codebase method accepts an
outcome, and a live leaf outcome never reaches the ledger or artifacts. The
planned package/import guards and persistent-store checks remain work items in
`docs/VESSEL_PLAN.md`; this README does not claim they already exist.

## Quick start

Requires Go 1.24 or newer. No third-party modules.

```sh
go test ./...
go run ./cmd/subc-poc check examples/fix.vyg
go run ./cmd/subc-poc run --prefetch 1 --fail editFiles=1 --reattempt editFiles=2 examples/fix.vyg
go run ./cmd/subc-poc run --prefetch 2 --ticks work=2 --concurrency 1 examples/prefetch.vyg
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
run(voyage plan)                     Vessel start: commit unit, prepare Root, demand Root
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
- Prefetch keeps a Touchdown **window** at a target, with recorded atomic
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
- Before virtual time advances, the Vessel repeats demand, deduction, and
  dispatch until nothing changes, so the window is refilled behind in-flight
  work.
- A failure found by speculative prefetch never ends a run by itself; it
  surfaces only when the Scheduler demands that occurrence.
- Deduction records are keyed by `(runId, occurrenceId)`; many runs can share
  one Codebase.
- Canonical encodings are length-framed and collision-tested.

`go test ./...` covers the whole pinned conformance corpus, `conformance/DEDUCTION.md`
scenarios 1–4 and 6–8 (5 only partially: no resume), Carousel plan scenarios
1–13 and 15–23 (14, replay, is not implemented), and orchestration plan §16
scenarios 1–3 and 5–11 (scenario 4 is covered with a carrier probe).

## What this POC does not do

- **Concrete policy semantics.** None ship; see `POLICY_DISCOVERY.md` in the
  language repository.
- **Structure-valued lookup maps** are blocked (`UnsupportedByProfile`, ADR 0003).
- **Crash/resume and exact replay** from the ledger are not implemented.
- **Concurrent deduction** (plan Phase 5) is not implemented; the reactor is
  serialized.
- **Fully Touchdown Cable artifacts, provenance indexes, Outcome Journaling,
  and incremental segment reuse** from SCP-0004 are planned, not implemented.
- **Experimental paths:** inline Goal-arrow stages (ADR 0002), artifact value
  capture (ADR 0004), and runtime `NoOutput` diagnostics (ADR 0005) may change
  when their proposals are decided.
- **Cross-runtime hashes**: artifact hashes use the POC-only
  `poc-sha256-canon/1` encoding.
- **Recursion** stays unsupported (static and dynamic `CycleDetected`).

## Continuous integration

- `test.yml` runs gofmt, vet, tests, and the examples against the pinned
  language revision on every push and pull request.
- `language-drift.yml` runs the same tests against the language repository's
  `main` branch every day and on demand, so grammar, conformance, or contract
  changes that the parser has not caught up with are reported even when this
  repository does not change.

## License and security

Licensed under the [Apache License 2.0](LICENSE). Report vulnerabilities
privately as described in [SECURITY.md](SECURITY.md).
