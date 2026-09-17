# Subsea Cable Runtime — Carousel POC

> **Status:** proof of concept. This is an independent implementation
> experiment for the [Subsea Cable](https://github.com/on-the-ground/subsea_cable_language)
> language. It is **not** an official Runtime, it does not define language
> semantics, and it does not resolve any open Owner decision. Every behavior
> below that is not normative in the language repository is a named, versioned
> POC profile choice.

It implements the Carousel deduction engine and a Runtime that coordinates it
with the Host and `@policy`, following the language repository's
`implementation/CAROUSEL_ENGINE_PLAN.md` and
`implementation/RUNTIME_ORCHESTRATION_PLAN.md`. Language-level findings from
this work are recorded in the language repository at
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

| Supported revision | Profile |
|---|---|
| `on-the-ground/subsea_cable_language@cbc6f53` | `poc-baseline/0`, `poc-rational/0`, `poc-sha256-canon/1` |

| Path | What it is |
|---|---|
| [docs/POC_PROFILE.md](docs/POC_PROFILE.md) | Every profile choice this POC makes, and how it maps to open decisions |
| `syntax/` | UTF-8 decoding, the conformance preprocessing pass, and a recursive-descent parser following `SubseaCable.g4` |
| `sema/` | Structural validation and Unit preparation |
| `codebase/` | In-memory artifacts, mutable `Name/Arity` index, revisions, run-scoped deduction ledger |
| `expr/`, `value/` | Value model and value-expression evaluation (Subsea rules + Host primitives) |
| `carousel/` | **The Carousel**: demand-time deduction, atomic commits, frontier, lineage, Touchdown window |
| `host/` | Host Port, the `poc-rational/0` primitive profile, and a scripted recording Host |
| `runtime/` | **The Runtime**: Scheduler, scope tracking, Outcome & Value Store, policy engine, reactor, trace |
| `runtime/policyexamples/` | Illustrative `@retry` / `@timeout` interpreters (never registered by default) |
| `cmd/subc-poc/` | CLI to check and run programs |
| `examples/` | Runnable programs |
| `conformancetest/` | Runs the pinned language's `conformance/cases.tsv` |
| `language/` | Pinned language repository (submodule) |

## Quick start

Requires Go 1.24 or newer. No third-party modules.

```sh
go test ./...
go run ./cmd/subc-poc check examples/fix.subc
go run ./cmd/subc-poc run --example-policies --prefetch 1 --fail editFiles=1 examples/fix.subc
go run ./cmd/subc-poc run --prefetch 2 --ticks work=2 --concurrency 1 examples/prefetch.subc
```

`run` prints the normalized trace (`--jsonl` for JSON lines). Anchors without a
registered implementation use an echo Host that returns `name(args)`.

CLI flags:

| Flag | Meaning |
|---|---|
| `--prefetch N` | additional Touchdowns to keep ahead of evaluation |
| `--concurrency N` | maximum in-flight attempts |
| `--consume-at dispatch\|completion` | POC knob for Owner decision R10 |
| `--demand ready\|manual` | baseline Scheduler demand mode |
| `--example-policies` | register the illustrative `@retry` / `@timeout` interpreters |
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
  ├─ dispatch ──────────────────────  eligible leaves → @policy BeforeAttempt → Host
  │    └─ Host.EvaluateFunction / Host.InvokeAnchor (nested $ calls via gateway)
  │
  └─ deliver next timeline event ───  completion or timer (virtual clock)
       └─ @policy AttemptOutcome → settle scope → propagate outcome upward
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
- Retry never re-deduces. Policies attach only to the occurrence they are
  authored on; composite policies see only their own scope events.
- A policy disclosed late is rejected before its occurrence executes; unknown
  policies are never ignored.
- Nested Anchor calls in function leaves are traced through a gateway and are
  not occurrences.
- The Carousel never calls the Host's function or Anchor capabilities.
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
1–13 (14, replay, is not implemented), and orchestration plan §16 scenarios
1–10.

## What this POC does not do

- **Eager `Goal(...)` calls and value-position `$anchor(...)` calls** outside
  function leaves are rejected with `UnsupportedByProfile`. Their staging is
  Owner decision R3.
- **Crash/resume and exact replay** from the ledger are not implemented.
- **Concurrent deduction** (plan Phase 5) is not implemented; the reactor is
  serialized.
- **Composite reattempt** (R4), `SatisfyScope`, and composite `Hold` are
  rejected.
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

## License

No license has been chosen for this repository yet.
