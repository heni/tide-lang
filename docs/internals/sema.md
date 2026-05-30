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
  `typecheck.go`; the lowered nodes type-check via the regular
  typing rules.
- Sema does **not** lower types into Go form — codegen handles
  the Go-side encoding (`lang-spec/lowering-go.md`). Sema's type
  representation is Tide-side: `int`, `Map<rune, int>`,
  `Status`, `Option<T>`.

What sema owns (see §4 for the seven-phase pipeline):

1. **Name resolution.** Every `Ident` / `NamedType` / qualified
   name resolves to a `Symbol` — local binding, top-level decl,
   imported module, field, method, variant, or builtin. After
   Phase 1 an `IdentExpr` no longer means "the string `foo`";
   it means "this resolved `LocalSymbol#42`" or
   "`TypeSymbol#17`". Errors with `E0301`/`E0302`.
2. **Type construction.** Every `NamedType` in source
   (`User`, `Option<User>`, `Map<string, int>`) is built into a
   canonical Tide-side `Type` value the rest of sema can
   compare, substitute through generics, and pattern-match on.
3. **Type inference.** Fills in types the source omits:
   `let x = foo()`, variant constructors (`Ok(42)` ⇒
   `Result<int, _>`), the empty `[]` literal (from context).
4. **Trait / interface satisfaction.** Validates that a
   structural type satisfies the Tide-side interface and (where
   it surfaces through a binding) the Go-side interface.
   Dangerous area — sema does the work, codegen reads the
   verdict.
5. **Exhaustiveness.** `match` arm patterns cover every value
   of the scrutinee. `E0303` with witness on miss.
6. **Effect / context validation.** Not effect *types* in the
   academic sense — just "is this construct legal at this
   context?". `try` only inside a function returning
   `Result`/`Option`; `return` only inside a function;
   `break`/`continue` only inside a loop; `spawn` only inside
   a `scope`. Errors via `E03xx`.
7. **Desugaring preconditions.** After sema, the lowering
   passes (`try` → early-return, `match` → switch / IIFE,
   `scope` → `errgroup`) must succeed *mechanically* — no
   additional analysis. Sema's job in this phase is to leave
   the AST + `Info` in a state where every downstream rewrite
   is a deterministic shape transformation.

Cross-cutting: the **Dynamic-doesn't-leak** invariant (§7) runs
inside Phase 3 (inference) and Phase 4 (satisfaction) — it's
not a separate phase because it gates introduction at exactly
the same sites those phases inspect.

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

```
internal/sema/
├── doc.go              — package overview
├── check.go            — entry point: sema.Check
├── env.go              — Scope, Symbol; the lexical environment
├── types.go            — Type representation + unification helpers
├── resolve.go          — Phase 1: name resolution
├── construct.go        — Phase 2: NamedType → canonical Type
├── infer.go            — Phase 3: type inference + checking
├── satisfy.go          — Phase 4: trait / interface satisfaction
├── exhaust.go          — Phase 5: match-exhaustiveness
├── context.go          — Phase 6: try / return / break / spawn context legality
├── shape.go            — Phase 7: desugaring-precondition assertions
├── dynamic.go          — Dynamic-doesn't-leak invariant (cross-phase)
├── diag.go             — Diag construction with .td coordinates
└── info.go             — the AST-keyed side-table
```

Each phase lives in its own file with its own walker so phases
can be tested in isolation. Tests live under
`tests/sema/<phase>/` with the per-phase fixture contract
mirroring the existing `tests/codegen/` shape.

## 4. The seven phases

Each phase consumes the previous phases' `Info` plus the AST.
A phase that detects a fatal error short-circuits all later
phases on the affected subtree — but other subtrees keep
going, so the user sees a coherent error batch.

```
        AST in
           │
           ▼
  ┌──────────────────────────────────────────────┐
  │ Phase 1 — Name resolution                    │
  │  resolve.go                                  │
  │  imports · locals · fields · methods ·       │
  │  variants                                    │
  │  ⇒ every IdentExpr / NamedType references a  │
  │    Symbol#N, not a string                    │
  │  E0301 / E0302                               │
  └────────┬─────────────────────────────────────┘
           │
           ▼
  ┌──────────────────────────────────────────────┐
  │ Phase 2 — Type construction                  │
  │  construct.go                                │
  │  source `Option<User>` ⇒ canonical           │
  │    Generic{ owner: Option, args: [User] }    │
  │  Substitutes nothing yet; just builds        │
  │  comparable type objects.                    │
  └────────┬─────────────────────────────────────┘
           │
           ▼
  ┌──────────────────────────────────────────────┐
  │ Phase 3 — Type inference + checking          │
  │  infer.go                                    │
  │  `let x = foo()` ⇒ x : <returnType(foo)>     │
  │  `Ok(42)`        ⇒ Result<int, _>            │
  │  `[]`            ⇒ from context              │
  │  Every typing rule from `type-system.md`     │
  │  enforced here.                              │
  │  E0303 family                                │
  └────────┬─────────────────────────────────────┘
           │
           ▼
  ┌──────────────────────────────────────────────┐
  │ Phase 4 — Trait / interface satisfaction     │
  │  satisfy.go                                  │
  │  Structural Tide records ↔ Tide interfaces;  │
  │  also ↔ Go-side interfaces surfaced through  │
  │  bindings. Dangerous area: structural types  │
  │  + Go nominal interfaces.                    │
  │  E0304 family                                │
  └────────┬─────────────────────────────────────┘
           │
           ▼
  ┌──────────────────────────────────────────────┐
  │ Phase 5 — Exhaustiveness                     │
  │  exhaust.go                                  │
  │  patterns(arms) covers values(scrutinee) ?   │
  │  Maranget's algorithm; emits a witness on    │
  │  miss.                                       │
  │  E0303-exhaust                               │
  └────────┬─────────────────────────────────────┘
           │
           ▼
  ┌──────────────────────────────────────────────┐
  │ Phase 6 — Effect / context validation        │
  │  context.go                                  │
  │  Not effect types — just "is this construct  │
  │  legal here?":                               │
  │    try     — only inside Result/Option fn    │
  │    return  — only inside fn                  │
  │    break   — only inside loop                │
  │    continue — only inside loop               │
  │    spawn   — only inside scope               │
  │  E03xx                                       │
  └────────┬─────────────────────────────────────┘
           │
           ▼
  ┌──────────────────────────────────────────────┐
  │ Phase 7 — Desugaring preconditions           │
  │  shape.go                                    │
  │  Asserts the AST + Info are now in a state   │
  │  where every downstream lowering             │
  │  (try → early-return, match → switch/IIFE,   │
  │   scope → errgroup) is a *mechanical* shape  │
  │  transformation needing zero further         │
  │  analysis. Failures here are sema bugs, not  │
  │  user errors — they assert internal          │
  │  invariants for codegen.                     │
  └────────┬─────────────────────────────────────┘
           │
           ▼
       Info + Diags
```

The **Dynamic-doesn't-leak** check is not a numbered phase —
it's a cross-cutting invariant enforced inside Phase 3 (every
inferred type is checked against the introduction whitelist)
and Phase 4 (every satisfaction widening is checked against
the same list). See §7.

**The seven phases are a working frame, not a closed set.**
The split was chosen so each concern owns exactly one file and
one walker — easier to spec, easier to test, easier to land
incrementally. A future concern that doesn't cleanly fold into
an existing phase gets its own phase rather than a clause
buried in another file. Likely additions, marked as such when
they land: per-block borrow-style "definite assignment"
(currently codegen relies on Go to catch use-before-init),
purity / `defer` ordering, and post-binding `comparable`
constraint flow.

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
covariance. The only widening rule is the D18 `Dynamic` intro
at reflect parameter sites; everywhere else equal-or-error.
Generic instantiation uses simple substitution.

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
| `type-system.md` typing rule (T-…) | a case in `typecheck.go` |
| `diagnostics.md` E-code | a `Diag.Code` literal in `diag.go` |

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

**Explicitly forbidden:**

| Site | What sema rejects |
|------|-------------------|
| `var d: Dynamic = some_int` | Direct assignment of a concrete `T` to a `Dynamic` binding (`E0210`). |
| `return some_int` from a `(): Dynamic` function | Return widening (`E0211`). |
| `[some_int, other_int]: []Dynamic` | Collection element widening (`E0212`). The user writes `[reflect.box(x), …]`. |
| Generic inference filling in `Dynamic` | A type parameter `T` is never inferred to `Dynamic`. If the inference reaches `Dynamic` there's a bug somewhere — `E0209` with witness. |

**Where the check fires.** Phase 3 inspects every place
inference picks a type; if the picked type is `Dynamic` *and*
the site is not on the allowed list above, emit `E0209`–`E0212`
and abort the affected subtree. Phase 4 makes the same check
when a structural-satisfaction step would widen a concrete `T`
into a `Dynamic`-typed slot.

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
so each step has a trivial rollback:

| codegen tracker | replacement | landed in |
|-----------------|-------------|-----------|
| `g.varKind` (local binding → "Map"/"Set"/"Stack") | `Info.Symbol.Kind` | PR-Sema-3 |
| `g.class` (class declaration table) | `Info.Type` (class symbols) | PR-Sema-4 |
| `g.variant` (variant-constructor lookup) | `Info.Variant` | PR-Sema-4 |

PR-Sema-1 wires the side-table but keeps codegen using its own
trackers; each later PR replaces one tracker and removes the
corresponding field from `gen`.

## 8. Phased delivery

| PR | Scope |
|----|-------|
| **PR-Sema-1** | Skeleton: `Scope`, `Symbol`, `Type`, `Diag`, `Info`; **Phase 1** (name resolution); `sema.Check` entry; wire into `cmd/tide build` / `run`. Surface: E0301 / E0302. |
| **PR-Sema-2** | **Phase 2** (type construction) + **Phase 3** (inference + typing rules) over the subset currently exercised by `tests/codegen/`. Generic instantiation with **explicit** type arguments (`Map<rune, int>.new()` — already in the corpus). E0303 family. Until Phase 5 lands, `match` is type-checked but exhaustiveness is **not** enforced — to keep the gap sound, Phase 3 requires every `match` to carry a wildcard `_` arm. The requirement is removed by PR-Sema-3. Folds the Dynamic-doesn't-leak check into Phase 3 introduction sites (E0209–E0212). |
| **PR-Sema-3** | **Phase 5** (exhaustiveness) + **Phase 6** (context legality for `try` / `return` / `break` / `continue` / `spawn`). Drops the Sema-2 wildcard-required rule. Migrates codegen's `varKind` to read from `Info.Symbol.Kind`. |
| **PR-Sema-4** | **Phase 4** (trait / interface satisfaction) — separate PR because structural ↔ Go-nominal interface bridging is its own design problem. Folds the Phase 4 half of the Dynamic-doesn't-leak check (satisfaction widening). |
| **PR-Sema-5** | **Phase 7** (desugaring preconditions) + type-arg **inference** at call sites (the implicit `reflect.box(counter)` shape) + `comparable` constraint enforcement for `Map<K, _>` / `Set<K>` keys. Migrates codegen's `g.class` / variant maps to `Info.Type` / `Info.Variant`. Removes the last "without sema we don't know" comments in `internal/codegen/codegen.go`. |

Phases land **before** the codegen migration in each PR — i.e.,
each Sema-N adds checks but leaves codegen unchanged. The
migration step is a separate PR per pass to keep diffs small
and the rollback story trivial.

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
