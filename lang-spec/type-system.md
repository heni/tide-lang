# Type system

Typing rules for Tide, in sequent notation where possible, with
pseudo-code for the few algorithms (unify, exhaustiveness)
that don't fit naturally. The contract for the sema pass that
runs after name resolution.

**Authority.** This file is the contract. Prose mirror in
`../docs/language-spec.md` may lag; on disagreement this file
wins.

## Notation

```
Γ ⊢ e : T
```

means: in environment Γ, expression `e` has type `T`. Γ maps
identifiers to types. Inference rules are sequents:

```
premises
─────────────
 conclusion
```

Side conditions in italics. `(T-Foo)` is the rule name —
referenced by diagnostics (forthcoming `diagnostics.md`).

`Never` is the bottom type: it unifies with every other type at
the call site and has no inhabitants. `unit` has exactly one
inhabitant `()`.

`Any` is the binding-boundary escape type. Concrete types widen
to `Any` implicitly at call sites that expect `...Any` (variadic
formatting parameters on bound stdlib functions like
`fmt.println`). `Any` does **not** narrow back to a concrete
type — a value of type `Any` cannot be used where a concrete `T`
is expected, with no implicit cast. The resolver and sema
together enforce that user-authored Tide code never introduces
an `Any`-typed parameter (per D11/G23).

`Dynamic` is the user-facing runtime-erased wrapper used by the
reflection API (per RFC-0003 and D18). `Dynamic` is deliberately
**separate** from `Any`: `Any` is the internal FFI escape, kept
out of user code; `Dynamic` is the explicit handle users reach
for when they want to hand a value to `reflect.*`. The two are
never silently promoted into one another. Introduction and
elimination rules for `Dynamic` are formalised in §Dynamic
below.

`Never` is a subtype of every type: `Never <: T` for all `T`. A
`DivergingExpr` (return/break/continue/panic/`os.exit`) has type
`Never`, so it unifies with whatever the surrounding expression
expects. This is what lets `let x = if c { 5 } else { return };`
type-check (`return : Never <: int`).

## Type formation (WF-T)

Before any expression rule can mention a `TypeExpr`, the type
expression itself must be **well-formed**. The judgement is
`Γ ⊢ T wf` ("`T` is a well-formed type under environment Γ");
expression rules implicitly require each type appearing in their
conclusion to be well-formed.

```
(WF-Prim)      P ∈ PrimitiveName               (per ast.md PrimitiveName enum)
               ─────────────────────────────────
                       Γ ⊢ P wf

(WF-Named)     N resolves to a TypeDecl, ClassDecl, InterfaceDecl,
               or built-in generic; arity matches type args
                          for each i: Γ ⊢ τ_i wf
               ──────────────────────────────────────────────────
                       Γ ⊢ N<τ_1, ..., τ_k> wf

(WF-Tuple)     n >= 2     for each i: Γ ⊢ T_i wf
               ──────────────────────────────────
                       Γ ⊢ (T_1, ..., T_n) wf

(WF-Slice)     Γ ⊢ T wf
               ────────────────
                       Γ ⊢ []T wf

(WF-Func)      for each i: Γ ⊢ T_i wf      Γ ⊢ R wf
               ──────────────────────────────────────
                       Γ ⊢ func(T_1, ..., T_n): R wf

(WF-Inline-Itf)
               each method m_j has signature sig_j with all
               argument and return types well-formed in Γ
               ──────────────────────────────────────────────
                       Γ ⊢ interface { m_1 sig_1; ... } wf
```

Arity mismatch on a generic instantiation → **E0207 Wrong type
arity**. Unknown type name → E0103 (from name resolution).

## Type judgements — expressions

### Literals

```
(T-IntLit)     Γ ⊢ IntLitExpr     : int
(T-FloatLit)   Γ ⊢ FloatLitExpr   : float64
(T-StringLit)  Γ ⊢ StringLitExpr  : string
(T-RuneLit)    Γ ⊢ RuneLitExpr    : rune
(T-BoolLit)    Γ ⊢ BoolLitExpr    : bool
(T-UnitLit)    Γ ⊢ UnitLit        : unit
```

Integer literals default to `int`; a literal in a context that
expects `int8..int64`/`uint..uint64`/`byte`/`rune` is narrowed
when in range, error E0204 if out of range.

### Identifiers and receivers

```
(T-Var)        Γ, x : T ⊢ x : T

(T-This)       Γ, this : C ⊢ this : C    [inside class C instance method]

(T-ScopeRef)   [scope-block context provides scope : Scope]
               Γ ⊢ scope : Scope                   [ScopeRef AST node]

(T-Paren)      Γ ⊢ e : T
               ──────────────────
                       Γ ⊢ (e) : T               [ParenExpr — pure wrapper]
```

`Scope` is a built-in type with one method `.context :
context.Context`.

### Conversions

```
(T-Conv)       Γ ⊢ e : T1    (T1, T2) ∈ ConvOK
               ─────────────────────────────────
                       Γ ⊢ T2(e) : T2
```

`ConvOK` is the closed set of legal source→target conversions in
v1:

| `T2` (target) | Legal `T1` (sources) |
|---|---|
| `int`, `int8`, `int16`, `int32`, `int64` | any numeric primitive, `byte`, `rune` |
| `uint`, `uint8`, `uint16`, `uint32`, `uint64`, `byte` | any numeric primitive, `byte`, `rune` |
| `rune` | any integer primitive, `byte` |
| `float32`, `float64` | any numeric primitive |
| `string` | `[]byte`, `rune` (UTF-8 encoding), any integer (codepoint) |

Any other pair fails as **E0205 Illegal type conversion**. Notably
`string → int` is **not** in `ConvOK` (use `strconv.atoi`).
Truncation / rounding semantics follow Go's rules at runtime.

### Dynamic — the reflection wrapper

`Dynamic` is a predeclared type used by the `reflect` module
(per RFC-0003). The type rules below are the spec contract that
sema enforces; together they implement D18-P2 ("`Dynamic` is
explicit and viral only by spelling").

**Well-formed type.** `Dynamic` is a predeclared identifier
(`keywords.md`); `Γ ⊢ Dynamic wf` holds in every environment.
It carries no type arguments. `Dynamic` may appear in any type
position a user names it explicitly — parameter, return,
field, slice / map / set element, type argument. The rules
below constrain *only* introduction (how a `Dynamic` value is
produced) and elimination (how it is consumed back to a
concrete type); they do not restrict where the spelling
`Dynamic` is allowed.

**Introduction at reflect-call sites (implicit widening).** The
sole site of implicit widening is a call to a function in the
`reflect` module whose corresponding formal parameter has type
`Dynamic`. Nothing else widens implicitly — not assignment,
not return, not collection-literal element position.

```
(T-Dyn-Intro-Reflect)
               Γ ⊢ f : (P_1, ..., P_n) → R
               f is a function declared in module `reflect`
               P_k = Dynamic
               Γ ⊢ a_k : T_k                              (T_k is any well-formed type)
               (every other a_i type-checks against P_i normally)
               ─────────────────────────────────────────────────────────
                                Γ ⊢ f(a_1, ..., a_n) : R
```

The rule applies *only* if `f`'s qualified name resolves to the
`reflect` module. A function with the same shape declared
outside `reflect` does **not** trigger widening — the argument
must already be `Dynamic` or the call is rejected with
**E0209**.

**Variadic generalisation.** A variadic formal `vs: ...Dynamic`
admits the same implicit widening per spread element: each
positional argument supplied to the variadic slot may be a
concrete `T` (which widens to `Dynamic`) or a `Dynamic` (which
passes through). Forwarding a slice via the spread operator
(`vs...`) keeps its static element type; an `[]int` spread does
**not** silently widen to `[]Dynamic` — the user must build the
`[]Dynamic` explicitly (each element wrapped in `reflect.box`).

**Introduction via `reflect.box` (explicit).** For any site
where the implicit rule does not fire — building a `[]Dynamic`
literal, returning a `Dynamic` from a non-reflect function,
storing a `Dynamic` in a field — the user writes
`reflect.box(v)` explicitly:

```
(T-Dyn-Box)    Γ ⊢ v : T
               ──────────────────────────────────
                       Γ ⊢ reflect.box<T>(v) : Dynamic
```

The type parameter `<T>` is inferred from the argument when
the call site is unambiguous.

**Elimination via `reflect.unbox` (only path).** A value of
type `Dynamic` cannot be used where a concrete `T` is
expected. The only recovery is the type-checked unwrap:

```
(T-Dyn-Unbox)  Γ ⊢ d : Dynamic
               ──────────────────────────────────────
                       Γ ⊢ reflect.unbox<T>(d) : Result<T>
```

`Result<T>` is the predeclared error sum (see `builtins.md`):
`Ok(t)` when the runtime descriptor of `d` matches `T`, and
`Err(e)` otherwise. No `Dynamic → T` cast exists outside this
call.

**No-narrowing rule.** Using a `Dynamic`-typed value where a
concrete type is expected is **E0210 Dynamic narrowing requires
reflect.unbox**:

```
(T-Dyn-NoNarrow)
               Γ ⊢ e : Dynamic     context expects T ≠ Dynamic     T ≠ Any
               ───────────────────────────────────────────────────────────
                                  E0210
```

**No-widening rule (outside reflect).** Passing a concrete `T`
where `Dynamic` is expected, *outside* the `T-Dyn-Intro-Reflect`
admitted sites, is **E0209 Dynamic widening requires reflect.box**:

```
(T-Dyn-NoWiden)
               Γ ⊢ e : T     T ≠ Dynamic     context expects Dynamic
               site is not a reflect.* parameter of formal type Dynamic
               ──────────────────────────────────────────────────────────
                                  E0209
```

In particular, every element of a `[]Dynamic` literal, every
value-position of a `Map<_, Dynamic>` entry, and every Set/Stack
element of `Dynamic` element type whose static type is not
already `Dynamic` must be wrapped in `reflect.box(_)`. There is
no inference path that promotes a concrete-element collection
literal to a `Dynamic`-element collection.

**Generic flow.** Type parameters of user-authored generic
declarations are **never** inferred to `Dynamic`. If unification
would yield `T = Dynamic`, sema rejects with **E0211 Dynamic in
inferred type-parameter position** — the user must rewrite the
call to pass through `reflect.box` / `reflect.unbox` explicitly.
This preserves D18-P3 (no universal `Value` lowering through
generics).

**Cross-reference.** The `Any` paragraph at the top of this
file describes Tide's other "top-ish" type. `Any` and `Dynamic`
share no implicit-conversion path in either direction; mixing
them across a call boundary is **E0212 Any/Dynamic cannot be
implicitly converted** (the user must `reflect.box` to go
either way).

### Arithmetic and logical operators

```
(T-Add-Num)    Γ ⊢ a : T   Γ ⊢ b : T   T ∈ numeric primitives
               ─────────────────────────────────────────────
                          Γ ⊢ a + b : T

(T-Add-Str)    Γ ⊢ a : string   Γ ⊢ b : string
               ───────────────────────────────
                     Γ ⊢ a + b : string

(T-Arith)      Γ ⊢ a : T   Γ ⊢ b : T   T ∈ numeric primitives   op ∈ {−, *, /, %}
               ───────────────────────────────────────────────────────────────────
                                    Γ ⊢ a op b : T

(T-Cmp)        Γ ⊢ a : T   Γ ⊢ b : T   T comparable               op ∈ {==, !=}
               ────────────────────────────────────────────────────────────────
                                  Γ ⊢ a op b : bool

(T-Ord)        Γ ⊢ a : T   Γ ⊢ b : T   T ∈ Ord
               ──────────────────────────────────────────────────────────
                            Γ ⊢ a op b : bool                  op ∈ {<, <=, >, >=}

               where Ord = {numeric primitives, string, rune, bool}
               — closed; see builtins.md §Comparable / Ord. Tuples
               and records are NOT in Ord (use field-wise comparison).

(T-Logical)    Γ ⊢ a : bool   Γ ⊢ b : bool       op ∈ {&&, ||}
               ───────────────────────────────────────────────
                              Γ ⊢ a op b : bool

(T-Not)        Γ ⊢ a : bool   ⊢ !a : bool

(T-Neg-Num)    Γ ⊢ a : T   T ∈ numeric primitives    ⊢ -a : T
```

"Comparable" excludes class types under `==`/`!=` (use `refEq`)
and excludes function values, channels, maps, sets, stacks, and
slices in general; tuples and records are comparable iff each
component is.

### Bindings and assignment

```
(T-Let)        Γ ⊢ e : T    annotation T' (if present) satisfies T' = T
               ────────────────────────────────────────────────────────
                      Γ, x : T ⊢ let x : T' = e : unit         (annotation T'
                                                                optional)

(T-Var-Init)   Γ ⊢ e : T     annotation T' (if present) satisfies T' = T
               ─────────────────────────────────────────────────────────
                      Γ, x : T ⊢ var x : T' = e : unit

[ Bare `var x : T` with no initialiser is rejected at the AST
  / sema-stage level per G1 — v1 requires every `var` binding to
  carry an explicit initial value (e.g. `var n: int = 0`). No
  zero-value defaulting. ]

(T-Assign)     Γ ⊢ lv : T    lv is writable     Γ ⊢ e : T
               ──────────────────────────────────────────
                          Γ ⊢ lv = e : unit
```

Compound assignment `lv <op>= e` (with `<op>` one of `+ - * / %`
per `grammar.ebnf` AssignOp) is **surface sugar** for
`lv = lv <op> e` and is desugared at parser stage (see
`desugaring.md` §Compound assignment). It typechecks under the
same `T-Assign` after the rewrite, so `lv` must be writable and
the result of `lv <op> e` must have the same type as `lv`.

"Writable lvalue" is one of: a `var` binding, a `var` field of
a class instance, or `s[i]` / `m[k]` index assignment on a
mutable backing. (Slice index-assignment is legal on any slice
binding because slice index-write mutates the backing array, not
the header — see `../docs/language-spec.md` §Collections.)

### Tuples and records

```
(T-Tuple)      Γ ⊢ e_i : T_i  for each i ∈ 1..n   n >= 2
               ─────────────────────────────────────────
                  Γ ⊢ (e_1, ..., e_n) : (T_1, ..., T_n)

(T-Tuple-Field)
               Γ ⊢ e : (T_0, ..., T_(n-1))    0 <= k < n
               ────────────────────────────────────────
                          Γ ⊢ e.k : T_k

(T-Record-Lit) [class/record type R with declared fields
                {f_1 : T_1, ..., f_n : T_n}; FieldInit names
                exhaust required fields]
                          for each i: Γ ⊢ value_i : T_i
               ───────────────────────────────────────────────
                  Γ ⊢ R { f_1: value_1, ..., f_n: value_n } : R

(T-Field)      Γ ⊢ e : R   R has declared field f : T
               ──────────────────────────────────────
                            Γ ⊢ e.f : T
```

### Slices, maps, sets, stacks

```
(T-Slice-Lit-Empty)
               annotation T explicit                              [SliceLit form `[]T{}`]
               ─────────────────────────
                  Γ ⊢ []T{} : []T

(T-Slice-Lit-Annot)
               Γ ⊢ e_i : T  for each i ∈ 1..n   n >= 0           [SliceLit form `[]T{e_1, ..., e_n}`]
               ─────────────────────────────────────────
                       Γ ⊢ []T{e_1, ..., e_n} : []T

(T-Slice-Lit-Nonempty)
               Γ ⊢ e_i : T  for each i ∈ 1..n   n >= 1           [SliceLit form `[e_1, ..., e_n]` —
                                                                  T inferred from elements; all e_i
                                                                  must agree]
               ─────────────────────────────────────────
                       Γ ⊢ [e_1, ..., e_n] : []T

(T-Make-Slice) Γ ⊢ n : int    annotation T explicit
               ────────────────────────────────────
                  Γ ⊢ makeSlice<T>(n) : []T

(T-Index-Slice)
               Γ ⊢ s : []T    Γ ⊢ i : int
               ──────────────────────────
                       Γ ⊢ s[i] : T

(T-Index-Map)  Γ ⊢ m : Map<K, V>    Γ ⊢ k : K
               ──────────────────────────────
                       Γ ⊢ m[k] : V                  [primary `IndexExpr` — runtime panic on miss;
                                                      total-API via `m.get` / `m.has` in builtins.md]

(T-Slice)      Γ ⊢ s : []T   low/high : int (or absent)
               ──────────────────────────────────────────
                       Γ ⊢ s[low:high] : []T

(T-Map-Lit)    BraceKind = Map<K, V>    n >= 0
               for each entry (k_i, v_i): Γ ⊢ k_i : K   Γ ⊢ v_i : V
               ─────────────────────────────────────────────────────
                       Γ ⊢ Map<K, V>{ k_1: v_1, ..., k_n: v_n } : Map<K, V>

(T-Set-Lit)    BraceKind = Set<T>    n >= 0
               for each entry e_i: Γ ⊢ e_i : T
               ──────────────────────────────────────
                       Γ ⊢ Set<T>{ e_1, ..., e_n } : Set<T>

(T-Stack-Lit)  BraceKind = Stack<T>    n >= 0
               for each entry e_i: Γ ⊢ e_i : T
               ──────────────────────────────────────
                       Γ ⊢ Stack<T>{ e_1, ..., e_n } : Stack<T>

(T-Range)      Γ ⊢ lo : int    Γ ⊢ hi : int
               ──────────────────────────────────────
                       Γ ⊢ lo..hi : Iterable<int>
                       Γ ⊢ lo..=hi : Iterable<int>             [inclusive form]
```

`BraceLit` with `BraceKind = Unknown` (a bare `{}` whose type
must be inferred from context) defers to **bidirectional**
checking — the expected type at the use-site (annotation, return
position, or argument slot) supplies `K`/`V`/`T`. When no
expected type is reachable, sema emits **E0208 Cannot infer
literal type**, asking for an explicit annotation. Built-in
operations on collection types (`.get`, `.set`, `.has`, `.push`,
`.pop`, etc.) are typed by `T-Call` against their predeclared
signatures in `builtins.md`; this file does not re-state them.

### Sum-type construction and patterns

```
(T-Variant-Nullary)
               D is a sum type with nullary variant V
               ──────────────────────────────────────
                            Γ ⊢ V : D

(T-Variant-Payload)
               D = ... | V(f_1 : T_1, ..., f_n : T_n) | ...
                       for each i: Γ ⊢ e_i : T_i
               ──────────────────────────────────────────
                  Γ ⊢ V(e_1, ..., e_n) : D
```

Patterns are typed in the *opposite* direction — the scrutinee
type flows in, the pattern binds variables:

```
(P-Wild)         Γ |- _              : T            ⇒ no bindings
(P-Unit)         Γ |- ()             : unit         ⇒ no bindings   [UnitPat]
(P-Lit-Int)      Γ |- IntLitPat n    : T            if T ∈ {int, int8..int64, uint..uint64, byte, rune}
                                                    and n fits in T
(P-Lit-String)   Γ |- StringLitPat s : string       ⇒ no bindings
(P-Lit-Rune)     Γ |- RuneLitPat r   : rune         ⇒ no bindings
(P-Lit-Bool)     Γ |- BoolLitPat b   : bool         ⇒ no bindings
(P-Ident)        Γ |- x              : T            ⇒ binds x : T
(P-Tuple)        Γ |- (p_1, ..., p_n) : (T_1, ..., T_n)
                 if for each i: Γ |- p_i : T_i
(P-Variant)      Γ |- V(p_1, ..., p_n) : D
                 if D has variant V(f_1 : T_1, ..., f_n : T_n)
                 and for each i: Γ |- p_i : T_i
(P-Record)       Γ |- R{ f_1 : p_1, ..., f_n : p_n } : R
                 if R has fields f_i : T_i, and Γ |- p_i : T_i
(P-Alt)          Γ |- a_1 | a_2 | ... | a_k : T
                 if each a_i types against T and binds the same
                 set of variables with the same types
```

**FloatLitPat is illegal in v1.** A `FloatLitPat` AST node
(produced by the parser per `ast.md:364`) is rejected by sema
with **E0305 Float literal patterns not allowed** — float equality
is unreliable and there is no clear semantics. Use a guard
condition (`if x == 3.14` inside the arm body, with a wildcard
pattern) when needed.

### `match` and exhaustiveness

```
(T-Match)      Γ ⊢ subject : T
               for each arm pat_i => body_i:
                 Γ |- pat_i : T  ⇒ Γ_i
                 Γ, Γ_i ⊢ body_i : U
               patterns(pat_1, ..., pat_n) is exhaustive over T
               ─────────────────────────────────────────────────
                     Γ ⊢ match subject { ... } : U
```

The arm bodies must all unify to the same type `U`. A
`DivergingExpr` body has type `Never`, which unifies with any
`U`.

Exhaustiveness algorithm (Maranget's): the pattern matrix must
**cover** every value of the scrutinee type. Failure → **E0303
Non-exhaustive match**, with a witness value showing what's
uncovered.

```
exhaustive(T, [p_1, ..., p_n]) iff
  cover(T, [p_1, ..., p_n])

cover(T, pats) iff
  for each constructor C of T:
    let specialized = specialise(C, pats)
    if specialized is empty: return false  (witness: C(...))
    if T's component types T_1, ..., T_k are nonempty:
      for each i: cover(T_i, project_i(specialized)) must hold
  return true
```

Wildcard / ident patterns trivially cover.

### Try, return, break, continue, panic

```
(T-Try-Result) Γ ⊢ e : Result<T, E_inner>
               enclosing function returns Result<U, E_outer>
               E_inner = E_outer                              (per G11 — no implicit conversion)
               ─────────────────────────────────────────────
                          Γ ⊢ try e : T

(T-Try-Option) Γ ⊢ e : Option<T>
               enclosing function returns Option<U>
               ──────────────────────────────────────
                          Γ ⊢ try e : T

(T-Return)     Γ ⊢ e : T    enclosing function declared return-type T (or unit)
               ────────────────────────────────────────────────────────────────
                          Γ ⊢ return e : Never

(T-Return-Unit) enclosing function declared return-type unit
               ──────────────────────────────────────────────
                          Γ ⊢ return : Never

(T-Break)      [enclosing loop exists]
               ─────────────────────────
                  Γ ⊢ break : Never

(T-Continue)   [enclosing loop exists]
               ─────────────────────────
                  Γ ⊢ continue : Never

(T-Panic)      Γ ⊢ msg : string
               ─────────────────────────
                  Γ ⊢ panic(msg) : Never
```

`os.exit(code)` is a regular call typed by its binding signature
which returns `Never`; no dedicated rule.

**Negative cases.** `try` in a function that returns neither
`Result<_, _>` nor `Option<_>` fires **E0402 `try` outside a
Result/Option function**. `break`/`continue` outside a loop
fires **E0404**. `spawn` outside a `scope` block fires
**E0405** (see also T-Spawn below).

### Defer

```
(T-Defer)      Γ ⊢ call : T              call AST shape is `Call`
               ──────────────────────────────────────────────────
                       Γ ⊢ defer call : unit
```

`defer` admits only a call expression as its argument (per G27);
any other expression shape fires **E0406 defer argument must be
a call**. The return type `T` of the deferred call is discarded.

### Control-flow expressions

```
(T-If-Stmt)    Γ ⊢ c : bool   Γ ⊢ t : unit    Γ ⊢ e : unit (when present)
               ────────────────────────────────────────────────────────
                  Γ ⊢ if c { t } else { e } : unit
                  Γ ⊢ if c { t }            : unit          [no else; t : unit]

(T-If-Expr)    Γ ⊢ c : bool   Γ ⊢ t : T    Γ ⊢ e : T
               ──────────────────────────────────────
                  Γ ⊢ if c { t } else { e } : T

(T-Block)      Γ ⊢ s_1 : unit, ..., s_n : unit    Γ ⊢ tail : T  (or absent ⇒ T = unit)
               ──────────────────────────────────────────────────────────────────
                       Γ ⊢ { s_1; ...; s_n; tail } : T

(T-For)        Γ ⊢ iter : I where IterElem(I) = T    Γ, pat : T ⊢ body : unit
               ──────────────────────────────────────────────────
                       Γ ⊢ for pat in iter { body } : unit

(T-While)      Γ ⊢ c : bool    Γ ⊢ body : unit
               ────────────────────────────────
                       Γ ⊢ while c { body } : unit
```

`IterElem(I)` is a closed mapping from iterable source-types to
their per-step element type, defined in `builtins.md` under
`Iterable<T>`. The v1 mapping is:

| Source type `I` | `IterElem(I)` |
|---|---|
| `[]T` | `T` (or `(int, T)` if `pat` is a 2-tuple pattern — indexed iteration) |
| `string` | `rune` (UTF-8 codepoint iteration) |
| `Map<K, V>` | `(K, V)` (insertion order) |
| `Set<T>` | `T` (insertion order) |
| `Iterable<int>` (a `RangeExpr`) | `int` |
| `RecvChan<T>` | `T` (loop ends on channel close) |

`Stack<T>` is **not** iterable in v1 — drain via `pop()` in a
loop. See `builtins.md` §Stack.

`Iterable<T>` itself is **not** an open user-extensible
interface in v1 — it is the closed set above. D11 parks the
typeclass extension. Indexed iteration `for (i, v) in s` over
`[]T` triggers the alternative `IterElem` clause; this is the
only place where the pattern shape influences `IterElem`.

### Functions, closures, calls

```
(T-Func-Decl)  params (x_i : T_i), return R, body Block
                 Γ, x_i : T_i ⊢ body : R
               ──────────────────────────────────────────────────────
                  Γ ⊢ FuncDecl f                    binds f : func(T_1, ..., T_n): R
                                                    in the enclosing file scope

(T-Closure)    params (x_i : T_i), return R, body Block               [ClosureLit]
                 Γ, x_i : T_i ⊢ body : R
                 if any x_i lacks annotation, T_i is inferred from the
                 expected closure type at the call site
               ─────────────────────────────────────────────
                  Γ ⊢ (x_i: T_i) => body : func(T_1, ..., T_n): R

(T-Call)       Γ ⊢ f : func(T_1, ..., T_n): R    for each i: Γ ⊢ arg_i : S_i
                                                              S_i = T_i, or S_i <: T_i
                                                              via T-Chan-Widen
               ─────────────────────────────────────────────────────────
                  Γ ⊢ f(arg_1, ..., arg_n) : R

(T-Call-Generic)
               Γ ⊢ f : ∀A_1, ..., A_k. func(T_1, ..., T_n): R
               type-arguments τ_1, ..., τ_k explicitly given
                  Γ ⊢ arg_i : T_i[A → τ]
               ──────────────────────────────────────────────
                  Γ ⊢ f<τ_1, ..., τ_k>(arg_1, ..., arg_n) : R[A → τ]
```

Type-parameter inference (`f(arg)` without explicit `<τ>`) uses
**unify** (Hindley-Milner Algorithm-W skeleton):

```
unify(t1, t2):
  t1, t2 = apply_subst(σ, t1), apply_subst(σ, t2)
  match (t1, t2):
    (TVar α, TVar β) if α == β     → σ
    (TVar α, t)                    → if α ∈ ftv(t): fail occurs-check
                                     else: σ ∘ {α ↦ t}
    (t, TVar α)                    → symmetric
    (NamedT(N, [a_i]),
     NamedT(M, [b_i])) if N == M   → fold unify over zip(a_i, b_i)
    (TupleT([a_i]), TupleT([b_i])) → fold unify over zip(a_i, b_i)
    (FuncT([a_i], r1),
     FuncT([b_i], r2))             → fold unify over zip(a_i, b_i),
                                     then unify(r1, r2)
    (SliceT(a), SliceT(b))         → unify(a, b)
    _                              → fail mismatch
```

Substitution `σ` accumulates left-to-right; failure on any pair
emits **E0201 Type mismatch**.

### Scope, spawn, channels

```
(T-ScopeExpr)  Γ ⊢ body : Block, typed under the scope's frame:
                 - body's trailing expression has type T;
                   it is implicitly lifted to Ok(trailing) : Result<T, E>
                   (no trailing expression ⇒ T = unit and the implicit
                   value is Ok(()))
                 - every `spawn { ... }` inside body satisfies T-Spawn
                   with the same E parameter
                 - parent, if given, has type context.Context
                 - E = error                                  (v1 restriction; see below)
               ────────────────────────────────────────────────────
                  Γ ⊢ scope<T, E>(parent?) { body } : Result<T, E>

(T-Spawn)      Γ ⊢ body : Result<unit, E_spawn>     [body is a Block, typed as in T-ScopeExpr]
               enclosing scope's error parameter E_outer is in scope
               E_spawn = E_outer                                  (same error channel)
               ─────────────────────────────────────────────────────
                  Γ ⊢ spawn { body } : unit          [registers in enclosing scope's errgroup]

(T-MakeChannel)
               annotation T explicit              cap : int (if given)
               ───────────────────────────────────────────────────────
                       Γ ⊢ makeChannel<T>(cap?) : Channel<T>

(T-Chan-Send)  Γ ⊢ ch : Channel<T>    Γ ⊢ v : T
               ─────────────────────────────────
                       Γ ⊢ ch.send(v) : unit
(T-Chan-Recv)  Γ ⊢ ch : Channel<T>
                       Γ ⊢ ch.recv() : T
                       Γ ⊢ ch.tryRecv() : Option<T>
(T-Chan-Close) Γ ⊢ ch.close() : unit

(T-Chan-Widen) Γ ⊢ ch : Channel<T>
               ──────────────────────────────────────
                       Channel<T> <: SendChan<T>
                       Channel<T> <: RecvChan<T>     [implicit, one-way; consumed by T-Call
                                                      arg-position matching]
```

The widening is one-way: there is no rule taking `SendChan<T>`
or `RecvChan<T>` back to `Channel<T>`. `T-Call` applies the
widening when an argument of declared type `SendChan<T>` /
`RecvChan<T>` is supplied a value of type `Channel<T>`. No other
subtyping relations are admitted in v1 (besides `Never <: T`).

### Select

```
(T-Select)     for each case c_i (Recv|Send|Default):
                 Γ ⊢ channel_i : Channel<T_i>   (or SendChan<T_i>, RecvChan<T_i>)
                 Γ (extended with bound x : T_i if Recv-with-binding)
                   ⊢ body_i : unit
               ───────────────────────────────────────────────────
                     Γ ⊢ select { ... } : unit
```

A `select` with no `default` blocks; with `default` it doesn't.
Both are well-typed.

## Top-level declarations

```
(T-Top-Let)    TopLevelLet has initialiser e, optional annotation T'
                 Γ ⊢ e : T    T' (if present) = T
               ──────────────────────────────────────────────────────
                  Γ ⊢ TopLevelLet x : T' = e        binds x : T (or T')
                                                    in file scope as immutable

(T-Top-Func)   same shape as T-Func-Decl, binding goes to file scope.

(T-Top-Type)   TypeDecl T = TypeBody         (TypeBody well-formed under Γ)
               ────────────────────────────────────────────────────────
                  Γ ⊢ TypeDecl T              binds T as a type alias /
                                              nominal definition (see WF-Body
                                              below for the alternatives)

(T-Top-Class)  ClassDecl C { ... }           (ClassBody well-formed)
               ────────────────────────────────────────────────────────
                  Γ ⊢ ClassDecl C             binds C as a class type

(T-Top-Iface)  InterfaceDecl I { ... }       (InterfaceBody well-formed)
               ────────────────────────────────────────────────────────
                  Γ ⊢ InterfaceDecl I         binds I as an interface type
```

Top-level `let` is immutable (G14) and requires an initialiser
(no bare `let x: T` at file scope; the parser rejects, sema
never sees it). There is no top-level `var` in v1.

## Type body / interface body well-formedness

The right-hand side of `TypeDecl` is a `TypeBody` (per
`ast.md:107–112`), one of:

```
(WF-Body-Alias)         Γ ⊢ T wf
                        ─────────────────────────
                                Γ ⊢ Alias T wf

(WF-Body-Record)        for each field f_i : T_i      Γ ⊢ T_i wf
                                  field names pairwise distinct   (E0105 otherwise)
                        ───────────────────────────────────────────
                                Γ ⊢ Record { f_1: T_1, ..., f_n: T_n } wf

(WF-Body-Tuple-Alias)   n >= 2     for each i: Γ ⊢ T_i wf
                        ─────────────────────────────────────────────
                                Γ ⊢ TupleAlias (T_1, ..., T_n) wf

(WF-Body-Sum)           variant names pairwise distinct          (E0106 otherwise)
                                for each variant V_j:
                                  - nullary, or
                                  - V_j(f_1: T_{j,1}, ..., f_{m_j}: T_{j,m_j})
                                    with each T_{j,k} wf
                        ────────────────────────────────────────────────────
                                Γ ⊢ Sum { V_1 | ... | V_k } wf
```

Class body well-formedness:

```
(WF-Class-Body)         for each field decl `var|let f : T`        Γ ⊢ T wf
                        for each method decl m with params and return R
                                method names + field names pairwise distinct
                                each method body type-checks under
                                  the class-scope frame (see T-Class-Method-* below)
                        ────────────────────────────────────────────────────
                                Γ ⊢ ClassBody wf
```

Interface body:

```
(WF-Iface-Body)         for each method signature (m_j, params_j, R_j):
                                Γ ⊢ each param-type wf       Γ ⊢ R_j wf
                                method names pairwise distinct
                        for each extended interface I_k     I_k is a declared interface
                        ────────────────────────────────────────────────────
                                Γ ⊢ InterfaceBody wf
```

Inline interface types (`interface { ... }` appearing inline in
a `TypeExpr` position) are well-formed by **(WF-Inline-Itf)**.

## Class membership, interface conformance

```
(T-Class-Field)
               C declares field f : T    var or let modifier known
               ──────────────────────────────────────────────────
                       Γ, this : C ⊢ this.f : T

(T-Class-Method-Inst)
               C declares instance method m(params): R
                 Γ, this : C, params ⊢ body : R
               ─────────────────────────────────────
                       Γ ⊢ C.m as a source-level value : func(C, ...): R

(T-Class-Method-Static)
               C declares static method m(params): R
                 Γ, params ⊢ body : R
               ──────────────────────────────────────
                       Γ ⊢ C.m : func(...): R         [no implicit C receiver]
```

**Note on T-Class-Method-Inst.** The `func(C, ...): R` shape is
the type Tide gives to an instance method when it is
referenced **as a free value** (`let f = C.m`, currently rare
in the corpus). The receiver becomes the first parameter at the
source level — Tide does not have separate "method-value" vs
"method-expression" syntax like Go. Most uses are
`obj.m(args)`, typed via `T-Field` → `T-Call` against the
method's parameter list (no explicit receiver argument).

### `implements`

```
(C-Implements) class C declares `implements I_1, I_2, ...`
               for each I_j and each method (name, sig) ∈ I_j:
                 C declares instance method `name` with signature `sig`
               ───────────────────────────────────────────────────
                       C : I_j  for each declared I_j
```

Method-set match is **exact** for v1 — no implicit conversion of
`(T, error)` returns vs `Result<T, error>` etc. The binding
generator handles any Go-side `(T, error)` ↔ `Result<T, error>`
mapping; user-side conformance is by-the-letter.

### Generic class / interface

```
(T-Class-Gen)  class C<A_1, ..., A_k> { fields, methods }
               instantiation C<τ_1, ..., τ_k>: each τ_i is WF-T well-formed,
                 arity k matches
               ───────────────────────────────────────────────────
                       Γ ⊢ C<τ_1, ..., τ_k> wf

(T-Interface-Gen)
               same shape; type-args substitute into method signatures
```

## refEq

```
(T-RefEq)      Γ ⊢ a : C_a    Γ ⊢ b : C_b
               C_a, C_b are class types
               C_a = C_b                                  (same class)
               ──────────────────────────────────────────────────
                       Γ ⊢ refEq(a, b) : bool
```

Calling `refEq` on non-class arguments, or with operands of
**different** class types, fires **E0206 refEq requires class
operands of the same class**.

## Type errors — quick index

The full catalog lives in `diagnostics.md` (forthcoming). Codes
touched by this file:

- **E0201** — Type mismatch (wherever a rule's premise about
  type equality fails, including unify failure).
- **E0202** — Wrong arity (call, variant payload, tuple
  destructure).
- **E0203** — Wrong return type (function body doesn't match
  declared return).
- **E0204** — Integer literal out of range for the inferred
  narrow numeric type.
- **E0205** — Illegal type conversion (source/target pair not
  in `ConvOK`).
- **E0206** — `refEq` requires class operands of the same class.
- **E0207** — Wrong type arity on a generic instantiation.
- **E0208** — Cannot infer literal type for a bare-`{}` `BraceLit`
  with no contextual expected type.
- **E0209** — `Dynamic` widening requires `reflect.box` outside
  `reflect.*` parameter sites (T-Dyn-NoWiden). *(Message text:
  PR-S5 / `diagnostics.md`.)*
- **E0210** — `Dynamic` narrowing requires `reflect.unbox`
  (T-Dyn-NoNarrow). A `Dynamic` value cannot flow into a concrete
  type position without explicit unbox. *(Message text: PR-S5.)*
- **E0211** — `Dynamic` in inferred type-parameter position
  (T-Dyn-Intro-Reflect's "no implicit Dynamic in generic flow"
  side condition). *(Message text: PR-S5.)*
- **E0212** — `Any` and `Dynamic` cannot be implicitly converted
  to each other. *(Message text: PR-S5.)*
- **E0105** — Duplicate field name in a `Record` body
  (WF-Body-Record side-condition).
- **E0106** — Duplicate variant name in a `Sum` body
  (WF-Body-Sum side-condition).
- **E0108** — Type used as value (defined by name-resolution;
  also fires from this pass when a `NamedType` identifier
  appears in expression position, e.g., generic-class callee
  without a member access or brace literal).
- **E0303** — Non-exhaustive match (with witness value).
- **E0304** — Unreachable arm (a pattern is shadowed by an
  earlier arm).
- **E0305** — Float-literal patterns are not allowed (`FloatLitPat`
  in any pattern position).
- **E0401** — `==`/`!=` on non-comparable type.
- **E0402** — `try` used outside Result/Option-returning
  function (fires when T-Try-Result / T-Try-Option fail to find a
  matching enclosing function-return type).
- **E0403** — Error type of `try`'s sub-expression does not
  match the enclosing function's error type
  (`E_inner ≠ E_outer` in T-Try-Result).
- **E0404** — `break`/`continue` outside a loop (no enclosing
  loop frame in T-Break / T-Continue).
- **E0405** — `spawn` outside a `scope` block (no enclosing
  scope frame providing `E_outer` in T-Spawn).
- **E0406** — `defer` argument must be a call (T-Defer
  side-condition fails).
- **E0407** — `scope` error parameter must be `error` in v1 (T-ScopeExpr
  side-condition; relaxed once a typed-error adapter lands —
  see `lowering-go.md` §ScopeIR / SpawnIR).
