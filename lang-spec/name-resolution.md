# Name resolution

How identifiers in a Tide program resolve to declarations. The
formal contract for the resolver pass; sema reads this to know
what every `Ident` AST node means before type checking.

**Authority.** This file is the contract. On disagreement with
`../docs/language-spec.md`, this file wins. Cross-refs to
`grammar.ebnf` (productions), `ast.md` (node kinds), and
`builtins.md` (predeclared scope, forthcoming).

## Scopes

Five scope kinds, **nested** in the order below. Resolution walks
them inside-out — the closest match wins.

1. **Local block scope.** Bindings created by `let` / `var`
   inside a `Block` are visible from their declaration point to
   the end of the enclosing `Block`.
2. **Function / method scope.** Parameters of the enclosing
   `FuncDecl` or `Method`. Inner blocks nest underneath; a
   parameter is visible in the entire body.
3. **Class scope (instance methods only).** Field and method
   names of the enclosing `ClassDecl`. The receiver `this` is
   bound here; `ScopeRef` (`scope`) is *not* a class-scope name
   — it's a control-flow construct (see §Special names).
4. **File scope.** Top-level `TopLevelLet` constants, `FuncDecl`,
   `TypeDecl`, `ClassDecl`, `InterfaceDecl`, and imported
   package aliases from `Import` declarations in the same file.
5. **Predeclared (built-in) scope.** Identifiers shipped by the
   language itself — `int`, `string`, `bool`, `Option`,
   `Result`, `Ok`, `Err`, `Some`, `None`, `error`, `Any`,
   `panic`, `refEq`, `makeChannel`, `makeSlice`, conversions
   (`int`, `float64`, …). Full list in `builtins.md`.

## Resolution algorithm

For each unqualified `Ident` AST node, lookup proceeds:

```
for scope in (local, function/method, class, file, predeclared):
    if scope contains a binding named ident.name:
        bind ident → that declaration
        return
emit E0103 Unknown name → ident.span
```

Qualified names (`QualifiedIdent` of length ≥ 2 in `NamedType`
and `Call.callee`) resolve the head per the same algorithm; the
remaining segments resolve as **member lookups**:

- A package head's member is a top-level declaration of that
  package (or the bound Go-stdlib package, for stdlib calls).
- A class-type head's `.method` member is a static method of
  that class.
- A class-instance head's `.field` / `.method` member is a
  declared field or instance method.

## Implicit receiver — instance methods

Inside the body of an instance method, identifier lookup
**includes class scope** between function/method scope and file
scope. Specifically:

```
local → param → class (fields + instance methods) → file → predeclared
```

So a bare `count` inside `class Counter { var count: int;
inc() { count = count + 1 } }` resolves to the field `count`
without needing `this.count`.

`this` is bound in class scope as a special name that yields the
current instance. It carries the receiver's type. `ThisExpr`
nodes are legal only inside instance-method bodies; the resolver
emits **E0501 `this` outside an instance method** otherwise.

Static methods have **no** class scope — they cannot reference
`this`, fields, or instance methods without an explicit
receiver expression (`Counter.new(0)` etc.). Inside a static
method body, `this` triggers E0501.

## Shadowing — diagnostics

The implicit-receiver rule makes one shadowing case dangerous
enough to be a hard error.

### Write-shadow of a class field — **E0502 (error)**

If, inside an instance method, a parameter or method-body local
introduces a name `n` that **also** names a field of the
enclosing class, then a **write** to bare `n` is a compile
error. The compiler will not guess whether the developer meant
the field or the local.

```td
class Counter {
  var n: int
  set(n: int) { n = 0 }   // ERROR E0502: writing to a bare `n` while param `n` shadows the field
}
```

Fixes:

- Rename the parameter / local: `set(v: int) { n = v }`.
- Or qualify the write: `set(n: int) { this.n = n }`.

A **read** of bare `n` in the same shadow region is fine — the
closest binding (param / local) wins, which is almost always
what the developer wants.

### Soft shadows — **W0503 (warning)**

Three cases of less-dangerous overlap emit a warning, not an
error:

- A method parameter or local shares a name with a **method**
  of the enclosing class that is never called inside the
  function body.
- A nested-block local shadows an outer local in the same
  function (`let x = 1; { let x = 2; ... }`).
- A method-body local shadows a free function in scope.

In each case, the closest binding wins (predictable C-style
shadowing). The warning exists because re-reads of the source
by a human will often pick the *outer* binding, so the writer
should rename or annotate.

## Special names

- **`this`** — receiver of an instance method. See above.
- **`scope`** — inside the lexical body of a `scope { ... }`
  block (G40), `scope` is a bound identifier that yields the
  current scope handle. `scope.context` is the cancellable
  context. Outside a scope block, `scope` *as a value* triggers
  **E0601 `scope` outside a scope block**.
  
  Disambiguation: `scope` followed by `<` / `(` / `{` parses as
  `ScopeExpr` (the value-block construct); `scope` followed by
  `.` parses as `ScopeRef` (an identifier-shape primary
  expression).
- **`_`** — discard. Resolves to nothing (cannot be read). Use
  in patterns, parameter positions, `let _ = expr` for side
  effects, `for _ in xs`. Multiple `_`s in the same scope do
  **not** shadow each other (each is a distinct anonymous
  binding).

## Variant constructors

A variant of a sum type lives in **two** scopes simultaneously:

- The sum type's namespace: `Direction.Up`, fully qualified.
- The file scope, unqualified: `Up`, when no other binding with
  that name exists in the resolved chain.

The resolver prefers the unqualified form when unambiguous. When
two sum types in the same file expose a same-named variant, only
the qualified form is legal — the unqualified form triggers
**E0104 Ambiguous variant name**.

Pattern positions follow the same rule, with one nuance: an
**unqualified** `Up` in a `Pattern` position resolves to a
known variant *if* such a variant exists in any in-scope sum
type — even if a fresh `IdentPat` binding would otherwise be
valid (`AltPat` cases like `Up | Left` are unambiguous in this
sense). The grammar admits both `VariantPat` and `IdentPat` for
the same source shape; the resolver picks `VariantPat` if a
matching variant is in scope, else `IdentPat`.

## Imports and module-level scope

`import fmt` introduces `fmt` into file scope; qualified member
access (`fmt.println`) resolves through the package's
declaration list. v1 has no `as` alias for imports —
`fmt.println` always uses the package's natural name.

A `Decl` is visible to **the entire file**, including positions
before its declaration in source order. (No forward-reference
errors; the file scope is built in one pass before any body is
resolved.)

## Forward references

- **Top-level declarations:** all visible everywhere in the
  file — no forward-reference errors.
- **Block-local `let` / `var`:** visible only from their
  declaration point onward. A reference to an unresolved local
  shadowed by an outer file-scope decl falls back to file scope.
- **Class fields / methods:** visible to each other regardless
  of order in the class body (the class scope is also built in
  one pass).
- **Type parameters of a generic decl** (`class LRU<K, V>` or
  `func f<T>(...)`): visible in the entire decl body, including
  the parameter and return type signatures.

## Generic type-argument resolution

Inside a generic decl, the type parameters are bindings in a
**type-only sub-scope** of class / function scope. A type
parameter `T` resolves only when looked up in type position; if
a value expression refers to `T`, the resolver emits
**E0301 Type used as value**.

## Resolver errors — quick index

The full catalog lives in `diagnostics.md` (forthcoming). The
ones above:

- **E0103** — Unknown name. Identifier resolves nowhere.
- **E0104** — Ambiguous variant name. Unqualified variant
  collides across two in-scope sum types.
- **E0301** — Type used as value. A type-parameter or named
  type appears where an expression is required.
- **E0501** — `this` outside an instance-method body.
- **E0502** — Write-shadow of a field by a method param or
  local; a bare write to that name is a hard error.
- **E0503** — Soft-shadow warnings (local/local,
  param/method-name, local/free-function). Emitted as a
  warning, not an error.
- **E0601** — `scope` reference outside a `scope { ... }`
  block.
