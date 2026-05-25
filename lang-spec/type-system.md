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
to `Any` implicitly at call sites that expect `...Any`. `Any`
does not unify with other types narrowing; the resolver and sema
together enforce that user-authored Tide code never introduces
an `Any`-typed parameter (per D11/G23).

## Type judgements — expressions

### Literals

```
(T-Int)        Γ ⊢ IntLitExpr     : int
(T-Float)      Γ ⊢ FloatLitExpr   : float64
(T-String)     Γ ⊢ StringLitExpr  : string
(T-Rune)       Γ ⊢ RuneLitExpr    : rune
(T-Bool)       Γ ⊢ BoolLitExpr    : bool
(T-Unit)       Γ ⊢ UnitLit        : unit
```

Integer literals default to `int`; a literal in a context that
expects `int8..int64`/`uint..uint64`/`byte`/`rune` is narrowed
when in range, error E0204 if out of range.

### Identifiers and receivers

```
(T-Var)        Γ, x : T ⊢ x : T

(T-This)       Γ, this : C ⊢ this : C    [inside class C instance method]

(T-Scope)      [scope-block context provides scope : Scope]
               Γ ⊢ scope : Scope
```

`Scope` is a built-in type with one method `.context :
context.Context`.

### Conversions

```
(T-Conv)       Γ ⊢ e : T1    T2 is a primitive numeric or byte/rune/string
               ──────────────────────────────────────────────────────
                            Γ ⊢ T2(e) : T2
```

Legal conversions follow Go's rules (truncation/rounding, byte ↔
rune ↔ int*; `string(r)` for a single rune to its UTF-8
encoding; `byte(c)` for narrowing). E0205 if the source type
isn't admissible.

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

(T-Ord)        Γ ⊢ a : T   Γ ⊢ b : T   T ∈ {numeric, string, rune, bool}
               ──────────────────────────────────────────────────────────
                            Γ ⊢ a op b : bool                  op ∈ {<, <=, >, >=}

(T-Bool)       Γ ⊢ a : bool   Γ ⊢ b : bool       op ∈ {&&, ||}
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

(T-Var-Decl)   T' explicitly annotated
               ──────────────────────────────────────────────
                      Γ, x : T' ⊢ var x : T' : unit

(T-Assign)     Γ ⊢ lv : T    lv is writable     Γ ⊢ e : T
               ──────────────────────────────────────────
                          Γ ⊢ lv = e : unit
```

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
               annotation T explicit
               ─────────────────────────
                  Γ ⊢ []T{} : []T

(T-Slice-Lit-Nonempty)
               Γ ⊢ e_i : T  for each i ∈ 1..n   n >= 1
               ─────────────────────────────────────────
                       Γ ⊢ [e_1, ..., e_n] : []T

(T-Make-Slice) Γ ⊢ n : int    annotation T explicit
               ────────────────────────────────────
                  Γ ⊢ makeSlice<T>(n) : []T

(T-Index)      Γ ⊢ s : []T    Γ ⊢ i : int
               ──────────────────────────
                       Γ ⊢ s[i] : T

(T-Index-Map)  Γ ⊢ m : Map<K, V>    Γ ⊢ k : K
               ──────────────────────────────
                  Γ ⊢ m.get(k) : Option<V>
                  Γ ⊢ m.set(k, v) : unit     [v : V]
                  Γ ⊢ m.has(k) : bool
                  Γ ⊢ m.delete(k) : unit

(T-Slice)      Γ ⊢ s : []T   low/high : int (or absent)
               ──────────────────────────────────────────
                       Γ ⊢ s[low:high] : []T
```

Sets and stacks follow the same pattern; full method signatures
in `builtins.md`.

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
(P-Wild)         Γ |- _    : T          ⇒ no bindings
(P-Lit-Int)      Γ |- n    : int        if n : int literal
(P-Ident)        Γ |- x    : T          ⇒ binds x : T
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
(T-Try-Result) Γ ⊢ e : Result<T, E>
               enclosing function returns Result<U, E>      (E unifies)
               ────────────────────────────────────────────────────
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

(T-For)        Γ ⊢ iter : Iterable<T>    Γ, pat : T ⊢ body : unit
               ──────────────────────────────────────────────────
                       Γ ⊢ for pat in iter { body } : unit

(T-While)      Γ ⊢ c : bool    Γ ⊢ body : unit
               ────────────────────────────────
                       Γ ⊢ while c { body } : unit
```

`Iterable<T>` is satisfied by `[]T`, `Map<K, V>` (with iter
type `(K, V)`), `Set<T>`, `RangeExpr` (`int`), and `RecvChan<T>`
(channel-as-iterator until close). Indexed iteration `for (i, v)
in s` types `(i, v) : (int, T)` over a `[]T`.

### Functions, closures, calls

```
(T-Func-Decl)  params (x_i : T_i), return R, body Block
                 Γ, x_i : T_i ⊢ body : R
               ───────────────────────────
                  Γ ⊢ func f(...): R { body } : func(T_1, ..., T_n): R

(T-Closure)    params (x_i : T_i), return R, body Block
                 Γ, x_i : T_i ⊢ body : R
                 if any x_i lacks annotation, T_i is inferred from the
                 expected closure type at the call site
               ───────────────────────────
                  Γ ⊢ (x_i: T_i) => body : func(T_1, ..., T_n): R

(T-Call)       Γ ⊢ f : func(T_1, ..., T_n): R    Γ ⊢ arg_i : T_i
               ─────────────────────────────────────────────────
                  Γ ⊢ f(arg_1, ..., arg_n) : R

(T-Call-Generic)
               Γ ⊢ f : ∀A_1, ..., A_k. func(T_1, ..., T_n): R
               type-arguments τ_1, ..., τ_k explicitly given
                  Γ ⊢ arg_i : T_i[A → τ]
               ──────────────────────────────────────────────
                  Γ ⊢ f<τ_1, ..., τ_k>(arg_1, ..., arg_n) : R[A → τ]
```

Type-parameter inference (`f(arg)` without explicit `<τ>`) uses
**unify**: solve the constraint set `arg_i ≡ T_i[A → ?]` for `?`
under occurs-check, then substitute.

### Scope, spawn, channels

```
(T-Scope)      Γ ⊢ body : Result<T, E>  (or terminates with first Err(E))
                 spawn blocks inside body each return Result<unit, E>
                 parent (if given) : context.Context
                 body's trailing expression has type T   (or `Block` value is unit if no trailing)
               ─────────────────────────────────────────────────────
                  Γ ⊢ scope<T, E>(parent?) { body } : Result<T, E>

(T-Spawn)      Γ ⊢ body : Result<unit, E>     (only legal inside a scope<_, E>)
               ────────────────────────────────────────────────────────────
                  Γ ⊢ spawn { body } : unit  (registers in enclosing scope)

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
```

`Channel<T>` widens to `SendChan<T>` / `RecvChan<T>` implicitly
at parameter sites; the reverse is not allowed.

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
                       Γ ⊢ C.m : func(C, ...): R

(T-Class-Method-Static)
               C declares static method m(params): R
                 Γ, params ⊢ body : R
               ──────────────────────────────────────
                       Γ ⊢ C.m : func(...): R         [no implicit C receiver]
```

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
               instantiation C<τ_1, ..., τ_k> well-formed
               ───────────────────────────────────────────
                       Γ ⊢ C<τ_1, ..., τ_k> well-formed type

(T-Interface-Gen)
               same shape; type-args substitute into method signatures
```

## refEq

```
(T-RefEq)      Γ ⊢ a : C    Γ ⊢ b : C    C is a class type
               ───────────────────────────────────────────
                  Γ ⊢ refEq(a, b) : bool
```

Calling `refEq` on non-class arguments → **E0206 refEq requires
class operands**.

## Type errors — quick index

The full catalog lives in `diagnostics.md` (forthcoming). Codes
touched by this file:

- **E0201** — Type mismatch (wherever a rule's premise about
  type equality fails).
- **E0202** — Wrong arity (call, variant payload, tuple
  destructure).
- **E0203** — Wrong return type (function body doesn't match
  declared return).
- **E0204** — Integer literal out of range for the inferred
  narrow numeric type.
- **E0205** — Illegal type conversion.
- **E0206** — `refEq` requires class operands.
- **E0301** — Type used as value (from name-resolution, listed
  here because the same code fires in type-checking too).
- **E0303** — Non-exhaustive match (with witness value).
- **E0304** — Unreachable arm (a pattern is shadowed by an
  earlier arm).
- **E0401** — `==`/`!=` on non-comparable type.
- **E0402** — `try` used outside Result/Option-returning
  function.
- **E0403** — Error type of `try`'s sub-expression does not
  match the enclosing function's error type.
- **E0404** — `break`/`continue` outside a loop.
- **E0405** — `spawn` outside a `scope` block.
