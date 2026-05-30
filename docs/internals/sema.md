# `internal/sema` — engineering notes

> **Internal implementation doc — subject to change.** This
> page describes how `internal/sema/` is *currently* organised.
> The package layout, pass split, and `Type` representation are
> implementation choices, not language commitments — they can
> be reshaped without a spec or RFC change. The language
> contract sema must satisfy lives elsewhere: see
> `lang-spec/name-resolution.md`,
> `lang-spec/type-system.md`, and
> `lang-spec/diagnostics.md`. Changing this page changes only
> the *how*; changing those files changes the *what*.

How the type checker is built. The implementation companion to
the spec — how the package is shaped, which passes run in what
order, and how errors flow back to the user.

## 1. Mission

Sema validates a `.td` program is well-formed and produces a
**resolved, type-checked AST** for codegen to lower
mechanically. Two firm boundaries:

- Sema does **not** rewrite the AST into another shape — that's
  desugaring (`lang-spec/desugaring.md`, currently parser-stage).
  Sema annotates existing nodes (name links, resolved types,
  variant tags) **via the side-table in §2**, it doesn't
  reshape them. Because parser-stage desugaring runs first,
  sema sees an already-lowered AST — `try foo()` is already an
  early-return-on-Err shape, compound assignment is already
  `lv = lv <op> rhs`, etc. There is no special `try` case in
  `body.go`; the lowered nodes type-check via the regular
  typing rules.
- Sema does **not** lower types into Go form — codegen handles
  the Go-side encoding (`lang-spec/lowering-go.md`). Sema's type
  representation is Tide-side: `int`, `Map<rune, int>`,
  `Status`, `Option<T>`.

What sema owns (see §4 for how this fits the barrier model):

1. **Name resolution.** Every `Ident` / `NamedType` / qualified
   name resolves to a `Symbol` — local binding, top-level decl,
   imported module, field, method, variant, or builtin. After
   resolution an `IdentExpr` no longer means "the string `foo`";
   it means "this resolved `LocalSymbol#42`" or
   "`TypeSymbol#17`". `E0103` / `E0104` / `E0108`.
2. **Type construction.** Every `NamedType` in source
   (`User`, `Option<User>`, `Map<string, int>`) is built into a
   canonical Tide-side `Type` value the rest of sema can
   compare, substitute through generics, and pattern-match on.
3. **Type inference and checking.** Fills in types the source
   omits — `let x = foo()`, variant constructors
   (`Ok(42)` ⇒ `Result<int, _>`), the empty `[]` literal (from
   context) — and enforces every typing rule from
   `type-system.md`. `E0201`–`E0208`.
4. **Trait / interface satisfaction.** Validates that a class
   that declares `implements I` actually provides the method
   set `I` requires, and (where it surfaces through a binding)
   that a Tide value satisfies the relevant Go-side interface.
   v1 has nominal `implements` only; the structural-vs-Go-
   nominal bridge is anticipated work, not v1 surface.
5. **Exhaustiveness.** `match` arm patterns cover every value
   of the scrutinee. `E0303` with witness on miss; `E0304`
   for unreachable arms; `E0305` for float-literal patterns.
6. **Context validation.** Not effect *types* in the academic
   sense — just "is this construct legal at this context?".
   `try` only inside a function returning `Result`/`Option`
   (`E0402` / `E0403`); `break` / `continue` only inside a
   loop (`E0404`); `spawn` only inside a `scope` (`E0405`);
   `defer` arg must be a call (`E0406`); `scope` error param
   must be `error` (`E0407`); `this` only inside an instance
   method (`E0501`); write-shadowing a field (`E0502`); `scope`
   reference outside a `scope { … }` body (`E0601`).
7. **Desugaring preconditions.** After sema, the lowering
   passes (`try` → early-return, `match` → switch / IIFE,
   `scope` → `errgroup`) must succeed *mechanically* — no
   additional analysis. Concretely: every `MatchExpr` carries
   `Info.Type`; every `TryExpr` is inside a function whose
   `Info.ReturnType` is `Result`/`Option`; every variant
   constructor has its resolved `Info.Variant`; every closure
   has captured-name bindings recorded.

The **Dynamic-doesn't-leak** invariant (§6.1) is enforced at
every site in (3) and (4) where a type can be assigned, widened
or inferred — it's a cross-cutting check, not a separate
concern.

What sema explicitly does **not** own:

- Borrow / lifetime analysis (Tide has no borrow checker).
- Constant folding (codegen's responsibility if at all).
- Effect tracking, purity (no spec for it yet).

## 2. Inputs and outputs

```
ast.File ──┐
           │     ┌──► []*Diag         (deterministic, source-ordered)
imports ───┼──► Check ┤
           │     └──► AnnotatedFile   (AST + sema.Info side table)
builtins ──┘
```

`sema.Check(f *ast.File) (*Info, []*Diag)`:

- `Info` is a side table keyed by AST node pointer →
  `{Symbol, Type, …}`. Codegen reads it where today it does
  ad-hoc local lookups (`g.varKind`, `g.class`, etc.) and the
  emitter loses the "without sema we don't know" caveats.
  **Invariant:** sema (and codegen) treat the AST as immutable
  after parser-stage desugaring. Pointer identity is the
  side-table contract — any later pass that clones or rebuilds
  AST nodes invalidates `Info` lookups silently. Future
  desugaring stages, if any, must declare themselves
  pre-sema-pre-info or rebuild `Info` afterwards.
- `Diag` slice is ordered by source position so the user sees
  errors top-to-bottom on `tide build`. **Accumulate, not
  fail-fast**: a malformed function should still let later
  functions get checked.

Both outputs are pure values — sema does not mutate AST nodes
in place. This keeps `internal/parser` tests insulated from
sema regressions and lets codegen tests run a "skip sema" path
during fixture authoring.

## 3. Internal layout

Files reflect the four **barriers** (§4) plus the cross-cutting
concerns. Inside a barrier, several walkers may run in parallel
(§8); each file owns one walker.

```
internal/sema/
├── doc.go              — package overview
├── check.go            — entry point: sema.Check; barrier orchestration
├── env.go              — Scope, Symbol; the lexical environment
├── types.go            — Type representation + unification helpers
│
│  Barrier A — declaration indexing
├── index.go            — collect top-level decls into the global symbol table
│
│  Barrier B — type / member shape resolution
├── resolve.go          — name resolution into Symbol#N references
├── construct.go        — NamedType → canonical Type; alias / cycle SCC analysis
├── signatures.go       — function / method signatures, class field sets,
│                        sum variant shapes — all from resolved Symbols
│
│  Barrier C — body checking (parallel per body)
├── body.go             — per-body walker: infer + check expressions / stmts
├── satisfy.go          — interface satisfaction sites encountered inside a body
│
│  Barrier D — whole-program checks
├── exhaust.go          — Maranget exhaustiveness across collected match summaries
├── context.go          — try / return / break / spawn / defer context legality
├── shape.go            — desugaring-precondition assertions for codegen
│
│  Cross-cutting
├── dynamic.go          — Dynamic-doesn't-leak invariant; consumed by body.go
├── diag.go             — Diag construction with .td coordinates + source-span sort
└── info.go             — the AST-keyed side-table
```

Tests live under `tests/sema/<barrier>/` with the per-barrier
fixture contract mirroring the existing `tests/codegen/` shape.

## 4. Two layers: modules above, bodies below

Sema runs at two distinct scales, and confusing them is the
fastest way to design a broken type checker.

- **Module-level sema** orchestrates the order in which modules
  are checked. Modules form a dependency graph; each module's
  exported interface is the only thing its dependents can see.
- **Body-level sema** runs *inside* one module against the
  already-known exported interfaces of its dependencies. This
  is where the four-barrier DAG (§4.2) lives.

### 4.1 Module-level sema

Per D20, the module import graph is **acyclic**. Sema enforces
this before any body check looks at any function:

```
Parse all modules
      ↓
Build module import graph
      ↓
Cycle? → diagnostic (cycle path printed), stop
      ↓
Topological sort
      ↓
For each module in topo order:
    ┌────────────────────────────────────┐
    │ Module-internal sema (§4.2)        │
    │  Barrier A — declaration indexing  │
    │  Barrier B — shape resolution      │
    │  Barrier C — body checking         │
    │                                    │
    │ Inputs:                            │
    │  - this module's AST               │
    │  - exported interfaces of every    │
    │    module in topo-order < self     │
    └─────────────────┬──────────────────┘
                      ↓
              Exported interface
              (types · functions · classes ·
               methods · variants · consts)
                      ↓
                Dependents see it
                      ↓
… continues until every module is checked …
      ↓
Barrier D — whole-program validation
      ↓
   Info + Diagnostics
```

A module's **exported interface** is whatever its dependents
can legally reach: pub-marked types, functions, classes,
methods, sum variants, constants. The interface is a
deterministic in-memory value (a future `.tidei` file would be
its on-disk projection); it does *not* expose function bodies
or private declarations. Cross-module reads go through this
interface; cross-module writes are impossible.

**Tide modules vs Go-stdlib bindings.** The import graph is
over Tide modules only. `import fmt`, `import strings`,
`import strconv`, … in `.td` source are *not* Tide-module
imports — they're Go-stdlib bindings whose surface is built
by `internal/bindgen` from `go/packages`. D20's acyclic rule
applies to Tide-module edges only; binding leaves sit outside
the graph. Sema treats a binding as an immutable
already-resolved interface — same shape as a Tide module's
exported interface, sourced through the bindgen pipeline
instead of from another module's Barrier B output.

**Cross-module generics.** A reference like `Map<K, V>` where
`Map` lives in one module and `K` in another resolves by
fetching the type-arg names through the importer's namespace,
then looking the type up in the producing module's already-
finalised exported interface. Because topological order
guarantees each module is fully checked before its dependents
run Barrier B, the producing module's interface is always
available when a dependent constructs the generic.

**v1 reality.** Tide v1 ships with a single user module — the
`.td` file passed to `tide build`. The module-level layer is a
no-op in degenerate form (one node, trivial topo order, no
cycle possible). The layer is in the architecture from day one
so adding multi-file support later is a multi-module loop
around the existing single-module pass, not a sema rewrite.

### 4.2 Body-level sema: the barrier DAG

Inside one module, sema is **not a strict-sequential pipeline**.
It is a dependency DAG with four invariant barriers. Each
barrier fixes "what's known" so the next barrier can rely on
it; **within** a barrier, work that doesn't cross those
invariants runs in parallel.

The barriers exist because some questions genuinely cannot be
answered until prerequisite data lands (you can't typecheck a
body until method signatures resolve), but most of the work
inside a barrier is independent and worth parallelising.

```
                       Parse AST
                          │
                          ▼
  ┌──────────────────────────────────────────────────────────┐
  │ Barrier A — Declaration indexing                         │
  │   index.go                                               │
  │                                                          │
  │   Collect top-level declarations into a global symbol    │
  │   table: types · classes · interfaces · funcs · imports  │
  │   · modules · variants · methods.                        │
  │                                                          │
  │   Parallelism: per file / per module.                    │
  │                                                          │
  │   After A: every top-level name is a `Symbol#N`. Bodies  │
  │   are still un-traversed.                                │
  └────────────────────────┬─────────────────────────────────┘
                           ▼
  ┌──────────────────────────────────────────────────────────┐
  │ Barrier B — Type / member shape resolution               │
  │   resolve.go · construct.go · signatures.go              │
  │                                                          │
  │   With the global symbol table in hand, build shapes:    │
  │   ├─ resolve type aliases / cycles (SCC analysis)        │
  │   ├─ build sum variant shapes                            │
  │   ├─ build class field sets                              │
  │   ├─ build method sets                                   │
  │   └─ build function / method signatures                  │
  │                                                          │
  │   Bodies still not inspected. Diagnostics: E0103, E0104, │
  │   E0105, E0106, E0107, E0108, E0207, plus alias-cycle.   │
  │                                                          │
  │   Parallelism: each declaration's shape is independent   │
  │   once Barrier A's table is frozen. Type-alias SCC is    │
  │   the single per-graph sub-pass.                         │
  │                                                          │
  │   After B: every declaration's *external surface* is     │
  │   fully typed and frozen. Sema may still grow purely     │
  │   additive content-addressed caches during C (generic    │
  │   interner, satisfaction cache, exhaustiveness summary   │
  │   bag) — those don't invalidate the freeze because they  │
  │   never rewrite an existing entry.                       │
  └────────────────────────┬─────────────────────────────────┘
                           ▼
  ┌──────────────────────────────────────────────────────────┐
  │ Barrier C — Body checking (the parallel zone)            │
  │   body.go · satisfy.go (per-body sites)                  │
  │                                                          │
  │   For each function / method body, walk the AST. Bodies  │
  │   are independent: each one consumes the same immutable  │
  │   semantic world and produces its own Info-fragment +    │
  │   diagnostics + match-coverage summary.                  │
  │                                                          │
  │   Inputs per body:                                       │
  │     - immutable global environment (Barrier B's output)  │
  │     - local scope stack                                  │
  │     - expected return type                               │
  │     - context flags (in-loop, in-scope, in-Result-fn)    │
  │                                                          │
  │   Outputs per body:                                      │
  │     - typed expressions / stmts in Info                  │
  │     - diagnostics                                        │
  │     - per-body match-coverage summary for Barrier D      │
  │     - interface-satisfaction sites for Barrier D         │
  │                                                          │
  │   This is where typing rules (E0201–E0212) fire. Context │
  │   legality (E0402–E0407, E0501–E0502, E0601) is checked  │
  │   in Barrier D — it needs the per-body context stack but │
  │   not the typing verdicts, and parking it with the other │
  │   whole-program validators simplifies diagnostics order. │
  │                                                          │
  │   The Dynamic-doesn't-leak check (§6.1) lives here —     │
  │   every inferred type and every assignment / return /    │
  │   collection-widening site is checked against the        │
  │   allowed-introduction whitelist.                        │
  │                                                          │
  │   Parallelism: per function / method body.               │
  └────────────────────────┬─────────────────────────────────┘
                           ▼
  ┌──────────────────────────────────────────────────────────┐
  │ Barrier D — Whole-program validation                     │
  │   exhaust.go · shape.go                                  │
  │                                                          │
  │   Concerns either inherently cross bodies or co-located  │
  │   here because the diagnostic-ordering story (§8 #4)     │
  │   wants a single deterministic finaliser:                │
  │                                                          │
  │   ├─ exhaustiveness: Maranget's algorithm over each      │
  │   │   match's collected summary (E0303 / E0304 / E0305). │
  │   │   Per-match in isolation, but parked here so all     │
  │   │   match diagnostics emit from one sorted pass.       │
  │   ├─ context legality (E0402–E0407 / E0501 / E0502 /     │
  │   │   E0601) — needs scope-stack from C but no typing.   │
  │   ├─ interface conformance cache resolution.             │
  │   ├─ orphan / conflicting impls (future).                │
  │   ├─ reflection-metadata completeness (every class /     │
  │   │   sum must produce a descriptor; D18 CT-1).          │
  │   ├─ entrypoint validation (`main` exists, right sig).   │
  │   └─ desugaring-precondition assertions for codegen      │
  │     (every MatchExpr has Info.Type; every TryExpr is in  │
  │     a Result/Option function; every variant ctor has     │
  │     Info.Variant; every closure has captures recorded).  │
  │                                                          │
  │   Sequential within itself — the union has already been  │
  │   collected, the validators are deterministic.           │
  └────────────────────────┬─────────────────────────────────┘
                           ▼
                  Info + Diagnostics
                           │
                           ▼
                     Desugar / lower
```

### Invariants the barriers fix

Each barrier publishes a guarantee that downstream work can
assume without re-checking:

| After barrier | Downstream can assume |
|---------------|----------------------|
| **A** | Every top-level name resolves to a `Symbol#N`. |
| **B** | Every type / signature has a canonical `Type`. The semantic world is immutable for the rest of the run. |
| **C** | Every expression has a type; every context-sensitive construct (try / break / spawn / …) has been validated; the Dynamic-doesn't-leak invariant holds. |
| **D** | Every match is exhaustive; every interface satisfaction is verified; every codegen precondition is asserted. |

### Why barriers, not phases

A linear "Phase 1, 2, 3, …" framing suggests strict ordering and
sequential execution. The truth is messier: most of the work
*inside* a barrier is embarrassingly parallel, and the barriers
themselves are about **what data is known**, not about a
particular order of file traversals. This framing also makes
the parallelism story explicit (§8) and avoids the trap of
adding a "Phase 8" for the next concern — a new concern goes
into the barrier whose invariants it needs, not into a new
slot at the bottom.

### Not a closed set

The barrier model is a working frame, not a permanent
commitment. Likely additions, marked as such when they land:
per-block borrow-style "definite assignment" (currently codegen
relies on Go to catch use-before-init), purity / `defer`
ordering, and post-binding `comparable` constraint flow. New
work folds into the barrier whose invariants it depends on.

## 5. Type representation

Internal `Type` is a closed sum (Go-side `type Type interface{}`
with a fixed set of concrete cases — `Prim`, `Named`, `Slice`,
`Map`, `Set`, `Stack`, `Func`, `Tuple`, `Generic`, `Dynamic`,
`Any`, `Unit`, `Never`). Stays Tide-shaped: a `Status` sum type
is `Named{Name: "Status", Decl: …}`, not the Go `Status struct`
shape codegen emits.

Why a closed sum rather than open interfaces: every type rule
in `type-system.md` is a pattern match over `Type`, and missing
a case is a programming bug worth catching at compile time.

Type unification is **invariant** in v1 — no subtyping, no
covariance. The only **implicit** widening rule is the D18
`Dynamic` intro at reflect-parameter sites; everywhere else
equal-or-error. `reflect.box(v)` is the **explicit** lifting
form — a regular call, not a widening rule. The two together
make up the allowed-introduction whitelist in §6.1. Generic
instantiation uses simple substitution.

Go's compiler does not enforce exhaustive type-switches on the
`Type` interface — closed-sum-ness is a convention we maintain
with `default: panic("unhandled Type kind: " + ...)` arms in
every switch plus the atomic-fixture rule (every concrete case
must be exercised). Adding a new `Type` case requires touching
each switch site; the panic is the safety net when the audit
misses one.

## 5.1. Spec-mirror discipline (D17)

Each spec artefact maps to exactly one place in sema:

| Spec source | Sema artefact |
|-------------|---------------|
| `name-resolution.md` rule | a branch in `resolve.go` |
| `type-system.md` typing rule (T-…) | a case in `body.go` (typing inside Barrier C) or `signatures.go` (signature shape, Barrier B) |
| `type-system.md` exhaustiveness | `exhaust.go` (Barrier D) |
| `type-system.md` context rule (T-Try / T-Break / …) | a case in `context.go` (Barrier D) |
| `diagnostics.md` E-code | a `Diag.Code` literal at the originating site |

When the spec adds a rule, the corresponding sema case lands in
the same PR (D17 paired edit). When a sema case is added without
a spec citation, the audit is supposed to catch it — the
atomic-fixture rule (CLAUDE.md "Every spec artefact carries
coverage") provides the cross-check.

## 6. Diagnostics

Every `Diag` carries a `Code` (E0xxx per `diagnostics.md`), a
`Span` from the offending AST node, and a human-readable
message in Tide terminology (D10) — class fields by their
declared names, sum variants by their declared names,
primitives with the Tide spelling. Raw Go-side names never
leak.

The CLI prints `repo-relative-path:line:col: error[E0xxx]:
<message>` — same shape as the existing parser / lexer errors,
so the user sees one consistent error format end-to-end.

## 6.1. Dynamic doesn't leak — the introduction whitelist

`Dynamic` is the runtime-erased wrapper introduced by RFC-0003
and governed by D18. Once it enters a binding, every later
operation has to reason about it specially, and that reasoning
is brittle. The invariant we hold is:

> A `Dynamic` value is introduced **only** at sites listed
> below. Everywhere else, an attempt to widen a concrete `T`
> into `Dynamic` is an error.

This is the kind of invariant that is *much* easier to install
in the first pass than to recover later — once `Dynamic` shows
up in a few places it's hard to convince yourself there isn't a
fourth or fifth path you forgot.

**Allowed introduction sites:**

1. `reflect.box(v)` — the explicit boxing call. Sema lifts its
   single argument from `T` to `Dynamic`.
2. Argument passed to a function in the `reflect` module whose
   corresponding parameter is declared `Dynamic` (e.g.
   `reflect.typeOf(counter)`). Implicit widening fires at
   exactly this call site, nowhere else.

**Explicitly forbidden (per `diagnostics.md`):**

| Site | What sema rejects | Code |
|------|-------------------|------|
| `var d: Dynamic = some_int` | Direct assignment of concrete `T` to a `Dynamic` binding. | `E0209` |
| `return some_int` from a `(): Dynamic` function | Return widening. | `E0209` |
| `[some_int, other_int]: []Dynamic` | Collection-element widening. The user writes `[reflect.box(x), …]`. | `E0209` |
| Generic inference filling in `Dynamic` | A user type parameter `T` unifies to `Dynamic`. | `E0211` |
| `let x: int = d` where `d: Dynamic` | Implicit narrowing — must go through `reflect.unbox<T>`. | `E0210` |
| `Any ↔ Dynamic` implicit conversion | Cultural-line invariant from RFC-0003 / D18. | `E0212` |

**Where the check fires.** Barrier C (`body.go`) inspects every
place inference picks a type; if the picked type is `Dynamic`
*and* the site is not on the allowed list above, emit the
appropriate code and abort the affected subtree. `dynamic.go`
holds the shared whitelist + matcher so every body's check is
deterministic and identical.

**Elimination is symmetric.** `Dynamic` leaves the type system
only through `reflect.unbox<T>(d): Result<T>` (per
`builtins.md`). No other narrowing — including pattern matching
on `Dynamic` — is admitted. Codegen can rely on this: every
`Dynamic` value either lives inside the `reflect` boundary or
has been observably unwrapped via `unbox`.

**Why this list lives here and not just in the spec.** The
RFC and `type-system.md` describe the rule abstractly; this
section describes how sema *enforces* it operationally — which
walkers check, which `Info` fields are inspected, which
diagnostics fire. If a future reflection feature widens the
allowed list (e.g. mutation methods), the discussion happens in
the RFC; this file gets the paired implementation update.

## 7. Integration with the pipeline

`cmd/tide` calls sema between parse and codegen. On any error
the program does not reach codegen — `tide build` / `tide run`
exit 1 with the diagnostics on stderr.

Hooks for the REPL:
- `tide repl` runs sema per turn before codegen. A sema error
  rolls back the session input just like a parse error does
  today; the existing `rejected` set in `replSession` catches
  retypes.
- Sema runs against the entire rendered session, not just the
  delta — small sessions stay fast, and the user sees errors
  that span turns (e.g., a stmt referencing a `let` shadowed
  in an earlier turn).

The codegen package gradually sheds its ad-hoc local trackers
(`g.varKind`, `g.class`, the variant lookup map) as sema becomes
the single source of truth. The migration is staged per tracker
so each step has a trivial rollback — the actual sequencing
(which tracker moves first, which PR owns it) is a pipeline
concern that lives in the dev tracker, not in this document.

## 8. Concurrency hazards inside a barrier

Barrier C is parallel-per-body; Barrier B has parallel
sub-stages. Each of the following needs explicit care.

1. **Type aliases / recursive types.**
   ```
   type A = B
   type B = A
   ```
   Detected only when looked at the SCC level. `construct.go`
   runs an SCC analysis over the alias graph before any
   per-alias work; cycles emit a deterministic error.

2. **Generic-instantiation cache / interner.**
   Repeated `Map<rune, int>` references should hash to a
   single `Type` value. Concurrent body checks contend on the
   interner. The implementation uses a `sync.Map`-backed
   intern table keyed by canonical type-key strings; lookup is
   read-mostly so contention is bounded.

3. **Interface satisfaction cache.**
   `class X implements I` is checked once; lazy lookups from
   different bodies must return the same verdict. `satisfy.go`
   memoises checks in a deterministic-order map; cache miss
   triggers a single check, hits return the cached result.

4. **Diagnostics ordering.**
   Parallel body checks naturally emit diagnostics in
   nondeterministic order. Decision: **sort all diagnostics by
   source span before returning from `sema.Check`**. The cost
   is one O(n log n) pass; the benefit is reproducible test
   output and predictable user experience. The alternative —
   declaring order unstable — would make every error-test
   fragile.

5. **Inference across declaration boundaries — a rule, not a
   runtime hazard.** This entry differs from items 1–4: it
   describes a deliberate language-level constraint that
   *prevents* a hazard from existing in the first place. If
   sema permitted inference in public function signatures
   (`func f(x) = …`), Barrier B would need to wait for
   Barrier C of dependencies, breaking the
   immutable-semantic-world guarantee. **Rule, kept
   deliberately strict:** every exported function / method
   signature is fully explicit. Local inference is
   body-local. This keeps bodies independent and Barrier C
   parallel.

## 9. Limits in v1

- **No flow-sensitive narrowing.** `if x is Some { ... x.value ... }`
  is desirable but needs flow typing; v1 requires `match`.
- **No row polymorphism / structural types.** Record types are
  nominal (D14); structural matching is out of scope.
- **No effect / async tracking.** Concurrency is uncolored
  (D7); sema treats `spawn` / `scope` as ordinary stmts.
- **No exhaustiveness over open types.** v1 covers nullary +
  payload sum variants. Refinement against record / class
  field values is out.
- **One file at a time.** Multi-file user programs need
  cross-file module resolution which v1 doesn't ship. Bindings
  against the Go stdlib (`import fmt`, `import strings`, …)
  already work — that path is handled by the binding layer,
  not by sema. For user-authored multi-file programs sema
  typechecks a single `*ast.File` plus the predeclared
  surface.

These aren't permanent decisions — they're "not in v1". Each
becomes its own future RFC if pressure builds.
