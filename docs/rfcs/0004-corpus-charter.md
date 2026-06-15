# RFC-0004 — Example corpus charter

| Field | Value |
|---|---|
| Number | 0004 |
| Status | accepted |
| Created | 2026-06-15 |
| Supersedes | — |
| Target | `examples/README.md`; pointer added in `docs/rfcs/0000-process.md`; index row in `docs/rfcs/README.md` |

## Summary

The `examples/` directory is Tide's **acceptance corpus**: a set of
real programs that *define* what the language must be able to express
and *drive* its development — a feature is done when an example that
forces it compiles and runs. This RFC turns that working practice into
a **public, governed contract** so the corpus can grow with the
community instead of freezing as a dev-internal artefact. It fixes:
the corpus's purpose; a purpose-based taxonomy; per-example metadata
(`example.toml`); the criteria that decide whether a *new* example
improves the set; a mechanism for **negative cases** (programs that
must fail, with a specified diagnostic) carried as patches alongside
the program they perturb; and the corpus's role in language-feature
acceptance.

## Motivation

The corpus has done real work: examples are written feature-first, and
each one landing has pulled a concrete language feature to completion.
That leverage is the reason to govern it rather than leave it implicit.

Three forces make a charter necessary now:

1. **Community-driveability.** The corpus is part of the codebase and
   must not freeze. For contributors to extend it well, the answers to
   "what is an example *for*", "what makes a *good* new example", and
   "what addition is just noise" must be **public and normative** — not
   tacit knowledge.

2. **A latent gap: negative coverage.** The corpus today proves only
   that programs *compile and run*. But half of a language's
   user-experience is **how it fails** — whether a mistake yields a
   legible diagnostic in Tide source coordinates and Tide terminology
   (D10), with the right stable code. Nothing in the corpus exercises
   that on *realistic* code; only minimal atomic fixtures do. We close
   the gap without standing up a wasteful parallel "broken programs"
   corpus.

3. **Feature acceptance depends on examples.** RFC-0000 governs how
   language features enter Tide, and that process is inseparable from
   the corpus (a feature is not *done* until an example drives it). For
   RFC-0000 to reference the example dimension cleanly it must point at
   another RFC, not at an internal process note — and the normative
   coverage rule it needs currently lives only in gitignored process
   files, invisible to contributors. This RFC lifts that rule into the
   public record so RFC-0000 can cite it (RFC → RFC).

## Design

### The corpus is an acceptance suite

`examples/` is the v1 acceptance suite (D12), not a tutorial
collection. Its contract has two halves:

- **Positive:** an example compiles end-to-end and runs — and, for a
  deterministic program, its stdout matches an expected baseline.
  "Compiles **and runs**" (not merely compiles) is the definition of
  *done* for the features it forces. The corpus-status ratchet measures
  this (`build_ok`, and an output-checked count, never regress).
- **Negative:** specified mistakes in real programs produce specified
  diagnostics — correct stable code, Tide source coordinates, Tide
  terminology (D10), never a raw `go/types` message. See
  [Negative cases](#negative-cases).

Examples are written **feature-first**: the program is authored against
the spec before the compiler can build it (a paper validation), then
the implementation catches up.

### An example is a directory

The unit of the corpus is a **directory**, not a single file. An
example may span multiple `.td` files — and *should* when the feature
under test is itself multi-file (this exercises the package model of
RFC-0002: package = directory). A single-file example is just the
common case of a one-file directory.

Each example directory carries a manifest, `example.toml`, and may
carry an `errors/` subdirectory of negative cases.

```
examples/<category>/<name>/
  example.toml          # required manifest
  main.td               # the program (one or more .td files; entry by default)
  expected.out          # expected stdout (when expects = output-check)
  errors/               # optional: negative cases
    <case>.patch        # unified diff vs. the base program
    <case>.expected     # expected diagnostic text (tolerant match)
```

### The `example.toml` manifest

```toml
description = "Two-sum via a hash map"  # one-line purpose — required; the index/report line
source   = "leetcode/1 two-sum"   # provenance — informational, NOT the category
category = "core-language"        # one of the purpose categories below
showcase = false                  # orthogonal flag — demonstrates value over Go
forces   = ["slice", "map-literal", "for-range"]  # spec artefacts pinned — controlled vocab
since    = "0.1"                  # language/corpus version the example targets
status   = "running"             # current pipeline reach: target-sketch | compiling | running
expects  = "output-check"        # the gate: output-check | run-pass | compile-pass | no-run
output   = "expected.out"        # expected-stdout sidecar (required when expects = output-check)
entry    = "main.td"             # entry file for a multi-file example; optional when single-file

[[error]]                         # zero or more negative cases (see below)
patch      = "errors/missing-arrow.patch"   # unified diff vs. the base, marker-anchored
expect     = ["E0201"]           # diagnostic code(s) — matched as an unordered set
stage      = "parse"            # pipeline stage that MUST reject it: parse | sema | emit
matches    = "errors/missing-arrow.expected" # paired sidecar; substring match, lenient coords
origin     = "synthetic"        # provenance: synthetic | organic:<run> | <issue/bug ref>
# exhaustive = false             # default subset-match (extra diagnostics tolerated); true = exact set
# count      = "1"               # cardinality per code; "1-2" allows a range (Clang-style)
```

Field notes:

- **`description`** is a one-line statement of the example's purpose —
  the human-readable line in any index or coverage report. Required,
  following [test262, where `description` is mandatory
  frontmatter][test262-interpreting].
- **`source`** records where the problem came from (LeetCode number,
  Timus problem, Advent of Code day, a borrowed showcase). It is
  *metadata*, deliberately decoupled from the category — provenance is
  useful for filtering but is not how the corpus is organised.
- **`forces`** is the coverage declaration: the spec artefacts
  (constructs, operators, type rules — by their `lang-spec/` names)
  that this program pins into *live* coverage. It is the live-coverage
  analogue of an atomic fixture's single-artefact target. Values are a
  **controlled vocabulary** — each must be a known artefact ID from
  `lang-spec/` (CI rejects an unknown one), and IDs are **stable
  anchors** (`T-Record-Lit`, `E0209`, a named lowering rule), never
  section or paragraph numbers. This is the lesson [test262 learned the
  hard way][test262-rationale]: it deprecated its section-number
  `es5id`/`es6id` keys for the stable `esid` anchor precisely because
  section numbers churn on every spec revision.
- **`status`** tracks the example's *current* pipeline reach:
  `target-sketch` (hand-written against the spec, may use illustrative
  syntax the compiler does not yet accept), `compiling` (parses and
  type-checks), `running` (builds and runs end-to-end). It mirrors the
  ratchet's view for humans; the ratchet's `auto-status.json` remains
  the machine source of truth for `build_ok`.
- **`expects`** is the gate the example must clear, in four modes:
  - `output-check` — builds, runs, and stdout matches the `output`
    sidecar **byte-for-byte**. This is the strongest gate and the
    default for deterministic programs.
  - `run-pass` — builds and runs, exit 0, output not checked (for
    programs whose output is uninteresting).
  - `no-run` — builds but is *not* run (non-deterministic or
    environment-bound: a server, a clock, the network). The analogue of
    [Rust's `no_run` doctest][rust-doctests] / a [Go `Example` with no
    `// Output:`][go-examples].
  - `compile-pass` — only type-checks; not built to a binary.

  **Why `output-check` matters: compiles ≠ correct.** A green build
  proves type/lowering soundness, not runtime correctness — a miscompile
  can build and produce wrong output. [Go (`// Output:`)][go-examples],
  [Wasm (`assert_return`)][wasm-testsuite] and [Rust (`run-pass`, exit
  0)][rust-test-directives] all gate on *run* behaviour, not just
  compilation, for this reason. So the corpus's
  "compiles **and runs**" definition of done is enforced, not merely
  stated: the ratchet grows an output-checked count alongside
  `build_ok` (implementation: PR2/PR3), and a feature is not "done" on a
  compile alone.
- **`expects`** vs. **`status`** — `expects` is the *intended* outcome,
  `status` is what the example *currently* achieves. A `target-sketch`
  has `expects = output-check` but `status = target-sketch` until the
  implementation it drives lands. The **stage / outcome** axis
  (`expects` here, `stage` on negative cases) is *orthogonal* to the
  purpose taxonomy: purpose says what a program is *about*;
  stage/outcome says what the harness should *do* with it and where it
  should stop. Tide's atomic suite already carries this axis (the
  `tests/{lexer,grammar,sema,codegen}/` split and the ratchet's
  `parse`/`sema`/`emit`/`build` classification); the corpus manifest
  names it explicitly so a negative case can assert *which* stage
  rejects it.

### Taxonomy — by purpose, not by source

Examples are organised into a small set of **purpose** categories. The
problem's origin lives in `source`, not in the directory name (mixing
the two is what made the pre-charter layout — `leetcode/`, `timus/`,
`borgo/` — hard to reason about as governance).

| Category | What it exercises |
|---|---|
| `core-language` | type system, control flow, generics, recursion, data structures — little or no stdlib |
| `modeling-errors` | sum types, `Result`/`try`, exhaustive `match`, state machines — domain modelling and failure as values |
| `stdlib-binding` | a specific Go-stdlib binding surface (`encoding/json`, `net/http`, …) |
| `concurrency` | uncolored concurrency — `spawn`, `scope`, channels, `select` |

`showcase` is an **orthogonal flag**, not a category: it marks the
examples that demonstrate Tide's value over plain Go (sum types,
exhaustive matching, ergonomic errors, uncolored concurrency, interface
conformance). A `core-language` example and a `concurrency` example can
both be showcases.

The category set is intentionally small and may grow through a later
RFC when a cluster of examples genuinely shares a purpose none of these
captures — not by ad-hoc directory creation.

Purpose is the *top-level* axis because it maps to Tide's distinctive
value. It is coarser than the language-area trees spec suites favour
([test262][test262-contributing], [TypeScript
`conformance/<area>/`][ts-conformance], the [WebAssembly
testsuite][wasm-testsuite] all taxonomise by area), so a **second level
by language area** is
anticipated *inside* a category once it grows lumpy — e.g.
`core-language/generics/`, `core-language/pattern-matching/` — rather
than letting one flat `core-language/` accrete dozens of files. The
second level is introduced when a category bloats, not pre-emptively.

### Acceptance criteria — what improves the set

A new example earns its place if it does **at least one** of:

- **Coverage delta** — it forces a construct, operator, type rule, or
  *combination* not yet pinned by any existing example (it pulls a spec
  artefact into live coverage, or covers a feature interaction the
  corpus lacks).
- **Showcase value** — it demonstrates Tide's advantage over Go more
  clearly than what exists, even if the underlying constructs are
  already covered.

…and **all** of:

- **Realistic / idiomatic** — a program someone would plausibly write,
  not a contrived feature-dump.
- **Atomic in intent** — it forces its feature with the least
  incidental complexity; the reader can see *what* it is for.
- **Compiles and runs** (`status = running`) — or is an explicitly
  flagged `target-sketch` authored ahead of the implementation it
  drives.
- **Declares `forces`** — every example states which artefacts it pins.

### Rejection criteria — what does *not* improve the set

- Pure duplication of coverage an existing example already provides,
  with no gain in clarity, realism, or showcase value.
- A kitchen-sink program with no single discernible driver.
- "More tests for already-covered constructs" — that is an *atomic
  fixture* in `tests/{lexer,grammar,sema,codegen}`, not a corpus
  example. The corpus is for realistic programs; exhaustive
  per-artefact coverage is the atomic suite's job.
- Dependence on an unbound stdlib package with no accompanying binding
  RFC.
- Style or golfing variations of an existing example.

### Negative cases

A negative case asserts that a *specific* mistake in a *real* program
yields a *specific* diagnostic. Rather than maintain a parallel corpus
of broken programs — wasteful, and divorced from realistic code —
negative cases live **as deltas against the valid base**:

- Each case is a **unified-diff patch** (`errors/<case>.patch`) that,
  applied to the example's base program, produces the erroneous form.
  The patch *is* the unit under test: it isolates the single mistake,
  making explicit "this one change must produce this one diagnostic" —
  which a whole alternative file would obscure. (This patch-against-the-
  valid-base model is, as far as the surveyed prior art goes, novel —
  every mature compiler instead stores a standalone broken file; the
  trade is realism + DRY against *patch-context drift* in place of
  *snapshot drift*, which the anchoring discipline below contains.)
- The case is registered as an `[[error]]` entry in `example.toml`:
  - **`expect`** — the diagnostic code(s), **inline in the manifest**,
    matched as an **unordered set** (multi-error ordering plays no
    role — it churns under compiler refactors and must never be
    asserted). Keeping codes in the manifest (not buried in message
    files) makes them grep-able: scanning `expect` across all manifests
    yields an instant map of which `E0XXX` codes have *live* coverage
    and which do not — the corpus side of the
    [code-registry set-difference gate](#coverage-model).
  - **`stage`** — the pipeline stage that must reject the case
    (`parse` / `sema` / `emit`). This pins the D-decision that errors
    surface as Tide diagnostics in Tide coordinates *before* the Go
    backend (D10) — a property a code-only assertion would lose.
  - **`matches`** — a paired sidecar file (`errors/<case>.expected`)
    holding the expected diagnostic *text*, which can be multi-line and
    bulky. Kept out of the manifest so the manifest stays lean.
- **Matching is tolerant.** Code(s) must match as a set; message text
  matches by substring against the sidecar; coordinates are checked
  **relatively** (position *within the patched region*, à la [Clang's
  `@+1`][clang-verify]), never as absolute line/col — a patch shifts every line below
  its hunk, so absolute coordinates would rot by construction. By
  default matching is **subset** (extra diagnostics are tolerated);
  a case may set `exhaustive = true` for sema-precision tests that must
  pin the *exact* diagnostic set, and `count` (e.g. `"1-2"`) for a
  diagnostic that legitimately fires a known number of times. (Atomic
  fixtures in `test-contract.md`'s `--- ERRORS ---` form remain the
  place for *exact*, verbatim diagnostic assertions; the corpus
  mechanism is the realistic, tolerant sibling — complementary, not
  redundant.)
- **Patches are anchored, not line-numbered.** To survive reformatting
  of the base, patches use minimal context (`-U1`) and/or a marker
  comment in the base program rather than absolute line numbers, so an
  unrelated edit upstream does not break the hunk.
- **A patch that no longer applies is a hard failure, not a skip.** If
  editing the base program breaks a patch despite anchoring, the
  harness fails loudly (locally and in CI). That failure is the useful
  signal "the base moved — regenerate this negative case", never a
  silent skip that would rot coverage.
- **Provenance — three tiers, recorded in `origin`.** A negative case
  is `synthetic` (an author deliberately injects an error to provoke a
  known diagnostic), `organic:<run>` (distilled from a real mistake — a
  solution attempt authored without the compiler), or an issue/bug
  reference (a mistake from the field). Tracking `origin` makes "how
  much of our diagnostic coverage is still synthetic" a grep and gives
  curation a handle. Synthetic cases are honest scaffolding, not the end
  state: organic and field-sourced cases are preferred and supersede an
  unrealistic synthetic case once one exists for the same diagnostic.
  A productive way to harvest realistic mistakes — and a good avenue for
  community contribution — is to write a solution against the spec
  *without* a compiler and keep the genuine slips that result; the
  detailed methodology is kept as internal process, outside this format
  spec.

Negative cases are primarily aimed at **parser / syntax diagnostics**
(the motivating need: validating and improving parser error behaviour
on known mistakes), but the mechanism is stage-agnostic — `expect`
carries any code and `stage` any stage, so sema (type-error)
diagnostics use the same form.

### Coverage model

The corpus participates in two coverage dimensions, each with a **hard
atomic** rule and a **soft live** rule:

| Dimension | Atomic (hard) | Live (soft) |
|---|---|---|
| **Constructs** (keywords, grammar productions, operators, AST nodes, type rules, lowering rules) | ≥ 1 fixture in `tests/{lexer,grammar,sema,codegen}` — blocks the PR | a realistic `examples/` program declaring it in `forces` — absence is a backlog item, not a blocker |
| **Diagnostics** (each `E0XXX`) | ≥ 1 atomic error fixture — blocks the PR | a corpus `[[error]]` negative case triggering it on realistic code — absence is a backlog item |

The hard atomic rules are the existing project coverage discipline,
**lifted here into the public record** (they previously lived only in
gitignored process notes). The live rules are this RFC's symmetric
extension: just as a construct earns live coverage from an example that
forces it, a diagnostic earns live coverage from a negative case that
provokes it on a real program.

Because both the diagnostic registry (`lang-spec/diagnostics.md`, the
closed `E0XXX` catalog) and the corpus's `expect` codes are
machine-readable, diagnostic coverage is a **set-difference gate**: the
codes in the registry minus the codes grepped from every `[[error]]`
`expect` are exactly the diagnostics lacking *live* coverage. This is
the cheap, mechanical check that backs the live-diagnostics rule —
modelled on Rust's first-class [error index (`rustc --explain
E0XXX`)][rust-error-index], where codes are an enumerable registry
rather than ad-hoc strings.

### Contribution process

Adding an example is a normal PR: it creates the example directory with
`example.toml` (declaring the required `description` and `expects` gate
plus `source`, `category`, `showcase`, `forces`, `since`, `status`,
`entry`), optionally `errors/` negative cases, keeps
the corpus-status ratchet green (`build_ok` never drops), and runs the
standard review pass. Adding an example that needs no spec change does
**not** itself require an RFC (per RFC-0000's table); this charter only
governs *what makes the addition worth landing*.

## Alternatives considered

- **Leave it descriptive in `examples/README.md` only.** Rejected: a
  README describes; it does not give contributors normative acceptance
  criteria, and RFC-0000 cannot cite it as a governance dependency.
- **Keep the coverage rule in the gitignored process notes.** Rejected:
  invisible to the community, and a public RFC citing it would be a
  dangling reference. Lifting it here is the point.
- **A separate negative/broken-programs corpus.** Rejected as wasteful
  and unrealistic — it would duplicate whole programs and divorce the
  errors from real code. Patches against the valid base reuse the
  realistic program and make the *single mistake* the unit under test.
- **Full-file expected snapshots with exact matching** (à la blessed
  baselines). Rejected as the *primary* corpus mechanism: exact
  whole-file snapshots are brittle under unrelated churn. Tolerant
  matching on code + substring fits realistic programs; exact,
  verbatim assertion stays the atomic suite's job
  (`test-contract.md` `--- ERRORS ---`).
- **Organise by problem source** (`leetcode/`, `timus/`, …). Rejected:
  source is not a purpose; it mixes axes and obscures coverage. Source
  moves to `example.toml` metadata, where it is more useful (filterable)
  and not load-bearing for organisation.

## Paired edits

This is a declarative / governance RFC; it touches no `lang-spec/`
contract files.

- `examples/README.md` — rewritten as the charter's prose mirror
  (purpose, the purpose-based taxonomy, per-example `Forces`, the
  acceptance/rejection criteria, the negative-case convention). This is
  the canonical home of the *description*; the RFC is the governance.
- `docs/rfcs/0000-process.md` — a Feature-acceptance pointer citing
  this RFC for the example/diagnostic-coverage dimension of accepting a
  language feature.
- `docs/rfcs/README.md` — index row for RFC-0004.
- `lang-spec/` — **None.**

## Transition / compatibility

Strictly additive. Existing examples are grandfathered: the taxonomy
reorg relocates them into purpose categories and the charter describes
them retroactively; `forces` is backfilled where known and tracked as
follow-up where not. No language or compiler behaviour changes. **Not
applicable** as a breaking change.

## Open questions

- **Patch format strictness.** Unified diff with minimal context
  (`-U1`) and marker-anchoring is the default (familiar,
  `git`-generable). Whether a stricter or marker-only format is ever
  needed is deferred until churn proves it.
- **Coordinate-match band.** Matching coordinates *relatively* (within
  the patched region, not absolute line/col) is decided; the exact
  tolerance band — how far from the patched region a diagnostic may
  land and still match — is a harness detail to pin from experience.
- **Corpus curation / pruning.** As the corpus grows, deliberate
  pruning of redundant or obsolete examples *will* be needed. Its
  process is **deliberately not specified here** — we do not yet know
  the concrete pressures (bloat, drift, category imbalance) to design
  against. The deferral is *structured*, though, learning from the
  prior art: the primary defence against bloat is **admission control**
  (the acceptance/rejection criteria above) rather than after-the-fact
  deletion — [test262 fights bloat with a redundancy bar and procedural
  generation][test262-contributing], not pruning. And when an example *is* retired, the bias is
  to **supersede, not delete** — mark it superseded with a pointer to
  its replacement — so the "this feature landed / was done" provenance
  the corpus encodes is never lost. The pruning *process* itself remains
  an anticipated future need, not an undecided detail of this RFC.
- **Corpus versioning across language versions.** `since` tags the
  version an example targets; whether the corpus ever needs a stronger
  per-version partitioning (a frozen "v1 suite" vs. a moving set) is
  left open until a second language version exists to force it.

## Prior art

The design borrows from mature compiler corpora; each concrete artefact
referenced above links to its definition here.

- **test262** (ECMAScript conformance suite) — per-test YAML frontmatter
  (`description`, `esid`, `features`, `flags`), the "every spec change
  observable in a test" bar, and bloat control by redundancy rule +
  procedural generation. The `esid`-over-section-number lesson directly
  shapes our stable-ID `forces`.
  - frontmatter / interpretation: [test262-interpreting]
  - contribution + anti-bloat rules: [test262-contributing]
  - `esid` deprecation rationale: [test262-rationale]
- **Rust** — doctests (`no_run`/`ignore`/`should_panic`) as tested docs;
  `compiletest` pass directives (`check-pass`/`run-pass`); the first-class
  error index (`rustc --explain`) that models our diagnostic registry.
  - doctests: [rust-doctests]
  - test directives: [rust-test-directives]
  - error index: [rust-error-index]
- **Go** — testable `Example` functions with `// Output:` (the run-and-
  check-output gate; no `// Output:` ⇒ compile-only): [go-examples]
- **WebAssembly testsuite** — declarative `assert_return`, taxonomy by
  feature area: [wasm-testsuite]
- **Clang `-verify`** — `expected-error`/`@+N` relative-offset
  annotations (the basis for our relative-coordinate matching):
  [clang-verify]
- **TypeScript** — `tests/cases/conformance/<area>/` as a curated,
  spec-area-organised positive suite: [ts-conformance]

[test262-interpreting]: https://github.com/tc39/test262/blob/main/INTERPRETING.md
[test262-contributing]: https://github.com/tc39/test262/blob/main/CONTRIBUTING.md
[test262-rationale]: https://github.com/tc39/test262/wiki/Test262-Technical-Rationale-Report,-October-2017
[rust-doctests]: https://doc.rust-lang.org/rustdoc/write-documentation/documentation-tests.html
[rust-test-directives]: https://rustc-dev-guide.rust-lang.org/tests/directives.html
[rust-error-index]: https://doc.rust-lang.org/error_codes/error-index.html
[go-examples]: https://go.dev/blog/examples
[wasm-testsuite]: https://github.com/WebAssembly/testsuite
[clang-verify]: https://clang.llvm.org/docs/InternalsManual.html#the-verifydiagnosticconsumer-class
[ts-conformance]: https://github.com/microsoft/TypeScript/wiki/Spec-conformance-testing
