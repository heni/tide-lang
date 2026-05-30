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

What sema owns:

1. **Name resolution.** Every `Ident` / `NamedType` / qualified
   name resolves to a `Symbol` — local binding, top-level decl,
   imported module, or builtin. Errors with `E0301`/`E0302` per
   `diagnostics.md`.
2. **Type checking.** Every expression gets a type; every
   typing rule from `type-system.md` is enforced (T-Let,
   T-Assign, T-Call, T-Match, …). Errors with `E0303`+ codes.
3. **Exhaustiveness.** `match` arms cover the scrutinee type.
   `E0303`.
4. **Dynamic discipline.** D18 / RFC-0003: `Dynamic` introduced
   only at `reflect.*` parameter sites, eliminated only via
   `reflect.unbox`. `E0209`–`E0212`.

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
├── resolve.go          — name-resolution pass (Phase R)
├── typecheck.go        — typing-rules pass (Phase T)
├── exhaust.go          — match-exhaustiveness pass (Phase E)
├── dynamic.go          — Dynamic intro/elim enforcement (Phase D)
├── diag.go             — Diag construction with .td coordinates
└── info.go             — the AST-keyed side-table
```

Files map 1:1 to passes; each pass is its own walker so they
can be tested in isolation. Tests live under
`tests/sema/<pass>/` with the per-pass fixture contract
mirroring the existing `tests/codegen/` shape.

## 4. Pass order

```
        AST in
           │
           ▼
  ┌────────────────┐
  │ Phase R        │  resolve every Ident / NamedType to a
  │  resolve.go    │  Symbol; report E0301 / E0302
  └────────┬───────┘
           │ Info has names; no types yet
           ▼
  ┌────────────────┐
  │ Phase T        │  walk every Expr, derive its Type;
  │  typecheck.go  │  enforce typing rules; E0303+
  └────────┬───────┘
           │ Info has types
           ▼
  ┌────────────────┐
  │ Phase E        │  walk every MatchExpr; verify the pattern
  │  exhaust.go    │  matrix covers the scrutinee type
  └────────┬───────┘
           │
           ▼
  ┌────────────────┐
  │ Phase D        │  walk every reflect.* call / Dynamic
  │  dynamic.go    │  reference; enforce intro/elim
  └────────┬───────┘
           │
           ▼
       Info + Diags
```

Each phase consumes the previous phase's `Info` plus the AST.
A phase that detected fatal errors short-circuits all later
phases on the affected subtree — but other subtrees keep going,
so the user sees a coherent error batch.

Phase E runs **before** Phase D by convention, not by
dependency: neither uses the other's output. Putting
exhaustiveness first keeps the user-visible error order
"shape" → "Dynamic discipline" rather than the reverse, which
felt more natural in review.

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
| **PR-Sema-1** | Skeleton: Scope, Symbol, Type, Diag; Phase R; `sema.Check` entry; wire into `cmd/tide`. Surface: E0301 / E0302 only. |
| **PR-Sema-2** | Phase T over the subset of typing rules currently exercised by `tests/codegen/`. E0303 family for literal type errors (mismatched assignment, bad call arity, bad operand types). Generic instantiation with **explicit** type arguments (`Map<rune, int>.new()` already in the corpus). Until Phase E lands, `match` is type-checked but exhaustiveness is **not** enforced — to keep the gap sound, Phase T requires every `match` to carry a wildcard `_` arm. The requirement is removed by PR-Sema-3. |
| **PR-Sema-3** | Phase D (Dynamic intro/elim) + Phase E (exhaustiveness). E0209–E0212 + E0303-exhaustive. Drops the Sema-2 wildcard-required rule. Migrates codegen's `varKind` to read from `Info.Symbol.Kind`. |
| **PR-Sema-4** | Type-arg **inference** at call sites (the implicit `reflect.box(counter)` shape) + `comparable` constraint enforcement for `Map<K, _>` / `Set<K>` keys. Migrates codegen's `g.class` / variant maps to `Info.Type` / `Info.Variant`. |

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
