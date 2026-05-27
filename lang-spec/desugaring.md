# Desugaring — Tide AST → Tide IR

The contract for the **desugaring** pass that runs after sema
and before codegen. Takes a fully-typed Tide AST (every node
carries its `Type`, every `Ident` carries its resolved
`Binding`) and produces a **Tide IR** — a strict subset of the
AST with high-level constructs rewritten into simpler shapes.

The IR is **not** Go yet. It is still Tide-shaped — same
identifier names, same `.td` source coordinates (`Span`),
same primitive types. Codegen (`lowering-go.md`, forthcoming
Formalization-I) takes IR → Go. Desugaring exists to keep
codegen small and uniform: codegen does **not** see `try`,
does not see `match`, does not see implicit receivers, does
not see short-closure sugar.

**Authority.** This file is the contract. Cross-refs to
`ast.md` (input shape), `type-system.md` (every rewrite
preserves the typing judgement), `name-resolution.md` (the
input has all bindings resolved), `builtins.md` (target
constructors for sum-type variants, `Scope`, channels), and
`lowering-go.md` (consumer of the IR — forthcoming).

## Notation

A desugaring step is written:

```
[[ source AST shape ]]   ⟿   target IR shape
```

with side conditions in italics. Each rule preserves the
typing relation: if `Γ ⊢ source : T` per `type-system.md`, then
`Γ ⊢ desugar(source) : T`.

**Span preservation.** Every IR node carries the `Span` of the
source it was derived from. The rule when one source construct
fans out into multiple IR nodes:

- The **root** synthesised node inherits the source construct's
  full span (the `TryExpr` keyword span for a `try` rewrite, the
  `ScopeExpr` keyword span for a scope rewrite).
- **Sub-expression** nodes retain their own source spans. In
  `try e`, the rewritten `MatchExpr.subject` is `e` with `e`'s
  span; the synthesised `Return Err(...)` carries the
  `try`-construct span (not `e`'s), so a diagnostic about the
  propagated error points at the `try`, not at `e`.

This is the contract sema and codegen rely on for diagnostics
(D10).

**Fresh-name generation.** Stages 4 and 6 introduce fresh
locals (`fresh_v`, `fresh_e`, `fresh_g`, `fresh_ctx`). All
freshly generated names use the reserved prefix `$tide_`
followed by a per-pass monotonic counter; the lexer rejects
`$` in user source (`grammar.ebnf` LexicalIdent), so collisions
are impossible by construction. Codegen rewrites `$tide_NN` to
a Go-legal identifier `_tide_NN`.

## Pass ordering

Desugaring is **one pass**, structurally bottom-up, but
conceptually splits into eight stages applied in this order
(each stage observes the output of the previous):

1. **Implicit-receiver expansion** — bare field/method reads
   inside an instance method get an explicit `this.` prefix.
2. **Short closure expansion** — `(x, y) => x + y` becomes the
   full `ClosureLit` shape (single trailing expression).
3. **Variant constructor application** — `Some(x)`, `Ok(v)`,
   user-defined `Up`, `Left` become explicit `VariantExpr`
   nodes with their resolved sum-type tag.
4. **`try` expansion** — `try e` rewrites to a `match` plus a
   propagated `return Err(...)` / `return None`.
5. **`match` decision tree** — multi-arm match-on-sum becomes a
   decision tree of single-arm matches (Maranget's compilation
   form). User-facing match is preserved; this stage produces
   the IR's *canonical* `MatchIR` form.
6. **`scope` / `spawn` lowering** — `scope<T, E>` and `spawn`
   become explicit `ScopeIR` and `SpawnIR` nodes carrying the
   errgroup binding, context derivation, and trailing-expression
   Ok-wrap.
7. **BraceLit canonicalisation** — `Map<K, V>{}` and friends
   become explicit `Map.new()` / `.set(...)` / ... call chains.
8. **For-loop normalisation** — `for x in iter` rewrites to a
   canonical iterator-protocol form depending on the iter type.

Each stage's contract is described below.

## Stage 1 — Implicit receiver

```
[[ Ident x ]]
  where x resolves to a class field f of the enclosing class C
  and the surrounding context is an instance-method body
                                                      ⟿
  Field { receiver: This { type: C }, name: x }

[[ Call { callee: Ident m, args: [a_1, ..., a_n] } ]]
  where m resolves to an instance method on the enclosing class C
  and the surrounding context is an instance-method body
                                                      ⟿
  Call {
    callee: Field { receiver: This { type: C }, name: m },
    args:   [a_1, ..., a_n],
  }

[[ Ident m ]]
  where m resolves to an instance method on the enclosing class C
  in expression position (method-as-value, e.g., `let f = m`)
                                                      ⟿
  Field { receiver: This { type: C }, name: m }
```

Reads of bare `n` inside `class Counter.inc() { count = count + 1 }`
become `this.count`. Bare instance-method calls (e.g.,
`name-resolution.md` §94-105 shows `error(): string` inside a
class `implements error`) become `this.m(...)`. The resolver has
already determined the bind; this stage materialises it so
codegen does not re-walk scopes.

Writes to bare `n` that shadow a field are already blocked by
E0502 at name resolution; they cannot reach desugaring.

## Stage 2 — (no-op; parser-level normalisation)

Tide has no `ShortClosure` AST node — the parser already
normalises the short form `(x, y) => x + y` into
`ClosureLit { params, body: Block { stmts: [], trailing:
Some(x + y) } }` per `ast.md:289-292`. Desugaring therefore
sees one canonical closure shape and does nothing in this
stage.

This stage also includes **compound assignment** — `grammar.ebnf`
admits `AssignStmt = LValue ("=" | AssignOp) Expr` with
`AssignOp = "+=" | "-=" | "*=" | "/=" | "%="`, but the parser
immediately rewrites a compound form into a plain assignment:

```
[[ AssignStmt { lv, op: "<op>=", rhs } ]]
                                            ⟿
  AssignStmt { lv, op: "=",
               rhs: Binary { op: "<op>", lhs: lv, rhs: rhs } }
```

The rewrite re-uses the same `lv` AST node on both sides of the
synthesised binary; sema (PR-Sema-1) tightens the rule by
forbidding compound assignment when `lv` contains a
side-effecting subexpression (function call, `try`, etc.). Until
sema lands, the duplication is benign for the only writable
shapes v1 admits (plain identifier, field access, slice / map
index).

`const` is a surface alias for `let` and produces an identical
`LetStmt` AST node — implementers shouldn't introduce a
separate `ConstStmt` form.

This stage is kept in the pipeline list for orientation —
implementers shouldn't add a fresh "short closure" rewrite when
they encounter one in the AST; the parser has already handled
it.

## Stage 3 — Variant constructor application

```
[[ Call { callee: Ident V, args: [a_1, ..., a_n] } ]]
  where V resolves to a variant constructor of sum type D
                                                      ⟿
  VariantExpr {
    type: D,
    tag: V,
    payload: [a_1, ..., a_n],
  }

[[ Ident V ]]
  where V resolves to a nullary variant of sum type D
                                                      ⟿
  VariantExpr { type: D, tag: V, payload: [] }
```

The parser produces `Call`/`Ident` nodes for variant
applications because it cannot distinguish them syntactically
from function calls / value references. The resolver tags them
with their target. Desugaring materialises the
**typed** form so codegen can emit a tagged struct directly.

For predeclared variants (`Some`, `None`, `Ok`, `Err`), this
stage is the only place type parameters become explicit on the
IR node — `Some(3)` ⟿ `VariantExpr { type: Option<int>, tag:
Some, payload: [3] }`.

## Stage 4 — `try` expansion

`try e` desugars into a `match` that either continues with the
unwrapped value or returns the wrapped error.

```
[[ TryExpr { inner: e } ]]
  where typeof(e) = Result<T, E_inner>
  and the enclosing function returns Result<U, E_outer>
  and E_inner = E_outer                                  (per T-Try-Result, G11)
                                                      ⟿
  MatchExpr {
    subject: e,
    arms: [
      Arm { pat: VariantPat{ tag: Ok, sub: [IdentPat fresh_v] },
            body: Var fresh_v },
      Arm { pat: VariantPat{ tag: Err, sub: [IdentPat fresh_e] },
            body: Return { value: VariantExpr{ tag: Err,
                                               payload: [Var fresh_e],
                                               type: Result<U, E> } } },
    ],
  }
```

(Each `fresh_v`, `fresh_e` is a fresh local introduced by the
desugaring pass; they don't shadow user bindings.)

The Option case is symmetric:

```
[[ TryExpr { inner: e } ]]
  where typeof(e) = Option<T>
  and the enclosing function returns Option<U>
                                                      ⟿
  MatchExpr {
    subject: e,
    arms: [
      Arm { pat: VariantPat{ tag: Some, sub: [IdentPat fresh_v] },
            body: Var fresh_v },
      Arm { pat: VariantPat{ tag: None, sub: [] },
            body: Return { value: VariantExpr{ tag: None,
                                               payload: [],
                                               type: Option<U> } } },
    ],
  }
```

After this stage, the IR contains **no** `TryExpr` nodes. The
side-condition `E_inner = E_outer` is checked by sema (T-Try-Result,
G11); E0402 / E0403 have fired already if it didn't hold. Desugaring
asserts the invariant — if it fails here, that's E0701 (internal
compiler error).

## Stage 5 — `match` decision tree

`match` with N arms and possibly nested patterns rewrites into
a *decision tree* of single-arm matches. The construction
algorithm is the **specialisation** form from Maranget's paper,
the same one used for exhaustiveness checking in
`type-system.md` §match.

Sketch:

```
compile(subject, [arm_1, ..., arm_n]):
  if n == 0:
    // Non-exhaustiveness was checked in sema (E0303). If the
    // algorithm still reaches an empty arm list here, emit an
    // UnreachableIR node — lowering-go.md decides how to encode it
    // (typically `panic("unreachable")`). Keeping the abstract
    // node in IR avoids hard-coding the panic message at this
    // layer.
    return UnreachableIR
  let first_col_pats = [arm_i.pat[0] for i in 1..n]
  if all are wildcards / idents:
    bind idents → recurse into the residual matrix on the remaining columns
  else:
    pick the head constructor C of the first non-wildcard pattern;
    emit a switch on subject's tag:
      case C: specialise — recursively compile the C-rows with
              C's payload columns prepended;
      case _: recursively compile the rows that don't have C as the
              first column (the default rows).
```

The compiled form lives in a new IR node:

```
MatchIR {
  subject: Expr,
  branches: [
    BranchIR {
      tag: VariantTag | LiteralValue,
      payload_binds: [Ident],
      body: Expr,
    },
    ...
  ],
  default: Option<Expr>,                  // only when sema permitted a wildcard
                                          // arm (e.g., over open-ended ints);
                                          // for closed sum types the algorithm
                                          // emits a full cover and `default` is
                                          // None.
}
```

After this stage, the IR contains **no** `MatchExpr` nodes (the
source-level shape is gone); codegen sees only `MatchIR`. The
optional `UnreachableIR` leaf (only reached when sema's
exhaustiveness check has held) is lowered by `lowering-go.md`.

## Stage 6 — `scope` / `spawn`

`scope<T, E>(parent?) { body }` and `spawn { body }` produce
explicit IR nodes carrying the errgroup binding and context
threading. The names below mirror the lowering target
(`lowering-go.md`, forthcoming) but the IR is still
Tide-shaped.

```
[[ ScopeExpr { ty: T, err: E, parent: p?, body: B } ]]
                                                      ⟿
  ScopeIR {
    group_name: fresh_g,                      // errgroup.Group binding
    ctx_name:   fresh_ctx,                    // derived context.Context
    parent:     p ?: TopContextExpr,          // p, or context.Background()
    body:       desugar_body(B, fresh_g, fresh_ctx, T, E),
    result_ty:  Result<T, E>,
  }

desugar_body(B, g, ctx, T, E):
  let stmts'    = desugar each stmt of B with `scope` ↦ ScopeBinding{g, ctx}
  let trailing' = B.trailing
  if trailing' is None:
    // T = unit case (from T-ScopeExpr); wrap unit value.
    let wrapped = VariantExpr { tag: Ok, payload: [UnitLit],
                                type: Result<unit, E> }
  else:
    let wrapped = VariantExpr { tag: Ok, payload: [trailing'],
                                type: Result<T, E> }
  Block { stmts: stmts', trailing: Some(wrapped) }
```

So the auto-`Ok` wrap from T-ScopeExpr is materialised here —
the IR contains the explicit `Ok(...)` constructor; codegen
does not need to know about the implicit wrap.

```
[[ SpawnExpr { body: B } ]]
  inside the lexical body of a ScopeIR{g, ctx, ...}
                                                      ⟿
  SpawnIR {
    parent_group: g,
    parent_ctx:   ctx,
    body:         B'                            // B with `scope` references
                                                // resolved to {g, ctx}
  }
```

`SpawnIR` nodes appear only as statements inside `ScopeIR`
bodies; the sema-time E0405 check guarantees this.

## Stage 7 — BraceLit canonicalisation

Container literals become explicit constructor calls so codegen
emits the same shape for empty and non-empty cases. The
result is a `Block` whose statements build the container and
whose trailing expression is the container value (so the
expression context is preserved).

```
[[ BraceLit { kind: Map<K, V>, entries: [(k_1, v_1), ..., (k_n, v_n)] } ]]
  fresh local: $tide_m                                  (n >= 0)
                                                      ⟿
  Block {
    stmts: [
      LetStmt { name: $tide_m,
                init: Call { callee: Field { receiver: Map, name: new },
                             type_args: [K, V], args: [] } },
      ExprStmt { expr: Call { callee: Field { receiver: $tide_m, name: set },
                              args: [k_1, v_1] } },
      ...
      ExprStmt { expr: Call { callee: Field { receiver: $tide_m, name: set },
                              args: [k_n, v_n] } },
    ],
    trailing: Some(Var $tide_m),
  }

[[ BraceLit { kind: Set<T>, entries: [e_1, ..., e_n] } ]]
                                                      ⟿
  Block { ...                                         (analogous; Set.new<T>(),
                                                       .add per element)
        }

[[ BraceLit { kind: Stack<T>, entries: [e_1, ..., e_n] } ]]
                                                      ⟿
  Block { ...                                         (analogous; Stack.new<T>(),
                                                       .push per element)
        }
```

The empty case (`n == 0`) collapses to a `Block` with one
`LetStmt` and a trailing `Var` — no `.set` / `.add` / `.push`
statements. The IR shape is identical at the structural level.

`BraceLit { kind: Record R, ... }` keeps its shape — record
literals are a single struct-construction, not a chain of
calls. Codegen emits a single Go struct literal.

After this stage, the IR's `BraceLit` carries only record
constructions; container-literal nodes are gone.

## Stage 8 — For-loop normalisation

`for pat in iter { body }` rewrites based on `IterElem(iter)`:

```
[[ ForStmt { pat, iter, body } ]]
  where iter : []T  and  pat is not a 2-tuple
                                                      ⟿
  ForRangeIR {
    iter:        iter,
    bind:        pat,
    body:        body,
    elem_ty:     T,
    indexed:     false,
  }

[[ ForStmt { pat: TuplePat{ [pat_i, pat_v] }, iter, body } ]]
  where iter : []T
                                                      ⟿
  ForRangeIR { ..., bind: TuplePat{ [pat_i, pat_v] }, indexed: true }

[[ ForStmt { pat, iter: RangeExpr{ lo, hi, inclusive } } ]]
                                                      ⟿
  ForRangeIR {
    iter:        IntRange { lo, hi, inclusive },
    bind:        pat,
    body:        body,
    elem_ty:     int,
    indexed:     false,
  }

[ NOTE: `RangeExpr` is the ONLY form that has type
  `Iterable<int>` in v1 (see `builtins.md` §Iterable and
  `type-system.md` §T-For). There is no free function or
  variable producing `Iterable<int>`, so this rule matches
  every for-over-int-range; no fallback case for
  `for i in computeRange()` exists because no expression of
  type `Iterable<int>` can be produced outside a literal range. ]

[[ ForStmt { pat, iter } ]]
  where iter : string
                                                      ⟿
  ForRangeIR { iter, bind: pat, elem_ty: rune, indexed: false, str_runes: true }

[[ ForStmt { pat, iter } ]]
  where iter : Map<K, V>
                                                      ⟿
  ForMapIR { iter, bind: pat /* expected TuplePat */, elem_ty: (K, V) }

[[ ForStmt { pat, iter } ]]
  where iter : Set<T>
                                                      ⟿
  ForSetIR { iter, bind: pat, elem_ty: T }

[[ ForStmt { pat, iter } ]]
  where iter : RecvChan<T>
                                                      ⟿
  ForChanIR { iter, bind: pat, elem_ty: T }
```

Each `For…IR` carries the per-step element type explicitly so
codegen does not re-run `IterElem`. The shapes are distinct
because Go's `for range` has different forms over slices,
strings, maps, and channels.

## What is **not** desugared

- **Generics.** Type parameters survive into the IR. Codegen
  decides between monomorphisation and use of Go generics; this
  is `lowering-go.md`'s job.
- **`if` / `while` / blocks.** Already Go-shaped; codegen emits
  them verbatim.
- **Channel widening.** `Channel<T> <: SendChan<T>` is
  representational — the IR keeps the same value, the lowering
  pass picks the right Go type at the use site.
- **Record literals.** Single struct construction; carried into
  IR unchanged.
- **`defer call`.** Stays as `defer call` in IR; codegen emits
  Go's `defer call`.
- **`refEq(a, b)`.** Stays as `refEq` call; codegen rewrites to
  `a == b` (interface / pointer identity).
- **Pattern matches over primitive scrutinees** (e.g.,
  `match c { '(' => ..., ')' => ... }`). The decision-tree
  algorithm produces a `MatchIR` whose `BranchIR.tag` is a
  `LiteralValue` instead of a `VariantTag`. `LiteralValue`
  carries the underlying primitive shape (`int`, `rune`,
  `string`, `bool`) plus the literal value; codegen lowers to
  a Go `switch`-on-value. There is no separate `SwitchIR`.

## IR shape — canonical node list

The IR after all stages contains:

- **Expressions**: every AST `Expr` variant *except*
  `TryExpr`, `MatchExpr`, and `BraceLit { kind: Map|Set|Stack }`.
  `VariantExpr` remains in its **fully-typed** form (Stage 3
  promoted implicit Call/Ident shapes to typed `VariantExpr`).
  The new IR-only forms are: `MatchIR` (with
  `BranchIR.tag ∈ {VariantTag, LiteralValue}`), `ScopeIR`,
  `SpawnIR`, `ForRangeIR` (with optional `str_runes: bool` flag
  for string iteration), `ForMapIR`, `ForSetIR`, `ForChanIR`,
  `TopContextExpr` (background context), `UnreachableIR`.
- **Statements**: same as AST but with the additions above and
  with implicit-receiver expansion baked in.
- **Decls**: unchanged from AST.

`ast.md` should be the authoritative reference for the *source*
shape; this file documents the IR shape implicitly via the
rewrite rules. A standalone `ir.md` may be promoted from this
section if Formal-L finds the implicit description insufficient.

## Errors — quick index

Desugaring is **after** sema; nearly all errors have fired
already. The only diagnostic raised here is:

- **E0701** Internal compiler error — non-exhaustive match
  reached desugaring without an exhaustiveness diagnostic. (This
  should never fire; if it does, it's a sema bug. Reserved.)
