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
source it was derived from. When a single source node fans out
to multiple IR nodes (e.g., `try` ⟿ `match`-with-return), all
fragments share the source `Span` so the eventual `//line`
directives at codegen point back to the original `.td` line.
This is the contract sema and codegen rely on for diagnostics
(D10).

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
  where x resolves to a class field f or instance method m
  and the surrounding context is an instance-method body
                                                      ⟿
  Field {
    receiver: This { type: C },
    name: x,
  }
```

Reads of bare `n` inside `class Counter.inc() { count = count + 1 }`
become `this.count` in the IR. The resolver has already detected
the bind; desugaring just makes it explicit so codegen does not
need to re-walk scopes.

Writes to bare `n` are already blocked by E0502 at name
resolution; they cannot reach desugaring.

## Stage 2 — Short closure expansion

```
[[ ShortClosure { params: ps, body: e } ]]
                                                      ⟿
  ClosureLit {
    params: ps,
    return: typeof(e),
    body: Block { stmts: [], trailing: Some(e) },
  }
```

Closures already typed by `T-Closure` see no shape change in
the IR for the explicit form; only the short `(x, y) => x + y`
form gets normalised to a full `ClosureLit` with a single-tail
`Block`.

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
[[ Try { inner: e } ]]
  where typeof(e) = Result<T, E>
  and the enclosing function returns Result<U, E>
                                                      ⟿
  Match {
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
[[ Try { inner: e } ]]
  where typeof(e) = Option<T>
  and the enclosing function returns Option<U>
                                                      ⟿
  Match {
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

After this stage, the IR contains **no** `Try` nodes. T-Try-*
diagnostics (E0402, E0403) have already fired in sema.

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
    panic at runtime — non-exhaustiveness check already happened in sema,
    so this branch is unreachable; emit `panic("unreachable")`.
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

After this stage, the IR contains **no** `Match` nodes (the
source-level shape is gone); codegen sees only `MatchIR`. A
runtime `panic("unreachable")` is inserted only where the
algorithm guarantees the branch is dead.

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
    append: VariantExpr { tag: Ok, payload: [UnitLit], type: Result<unit, E> }
  else:
    append: VariantExpr { tag: Ok, payload: [trailing'], type: Result<T, E> }
  Block { stmts: stmts', trailing: <the Ok-wrapped value above> }
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
emits the same shape for empty and non-empty cases.

```
[[ BraceLit { kind: Map<K, V>, entries: [(k_1, v_1), ..., (k_n, v_n)] } ]]
                                                      ⟿
  let m = Map.new<K, V>() in
  m.set(k_1, v_1); ...; m.set(k_n, v_n); m

[[ BraceLit { kind: Set<T>, entries: [e_1, ..., e_n] } ]]
                                                      ⟿
  let s = Set.new<T>() in
  s.add(e_1); ...; s.add(e_n); s

[[ BraceLit { kind: Stack<T>, entries: [e_1, ..., e_n] } ]]
                                                      ⟿
  let st = Stack.new<T>() in
  st.push(e_1); ...; st.push(e_n); st
```

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
  algorithm produces a `MatchIR` switching on literal values
  rather than variant tags; the structure is the same. There is
  no separate `SwitchIR`.

## IR shape — canonical node list

The IR after all stages contains:

- **Expressions**: every AST `Expr` variant *except*
  `Try`, `Match`, `BraceLit{Map|Set|Stack}`, `ShortClosure`,
  `VariantExpr` (in implicit form — only fully-typed
  `VariantExpr` remains). The new IR-only forms are
  `MatchIR`, `ScopeIR`, `SpawnIR`, `ForRangeIR`, `ForMapIR`,
  `ForSetIR`, `ForChanIR`, `TopContextExpr` (background context).
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
