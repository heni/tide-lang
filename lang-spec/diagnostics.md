# Diagnostics — error and warning catalog

The closed catalog of every diagnostic the v1 Tide compiler can
emit. Each entry has: a stable code, a one-line description, the
authoritative rule (from `type-system.md`, `name-resolution.md`,
`grammar.ebnf`, `desugaring.md`, or `lowering-go.md`), the
severity (error / warning), and the recommended quick fix.

This file is the **single source of truth** for error codes.
Other formal docs reference codes here by number; introducing a
new code requires a paired edit here and at the rule's home.

**Authority.** This file is the contract. The text of each
`message` field is part of the contract — fixtures
(`test-contract.md` §`--- ERRORS ---`) compare against it
verbatim. Cross-refs to: rules that fire each code.

## Numbering scheme

```
E01xx — lexer / parser / name-resolution
E02xx — type system (general)
E03xx — pattern matching
E04xx — control flow (try, return, break, continue, defer, scope, spawn)
E05xx — class scope / shadowing
E06xx — special names (`scope`, `this`, `_`)
E07xx — desugaring (internal)
E08xx — codegen / lowering (internal)
E09xx — REPL input
E10xx — foreign bindings (Go FFI)
```

Warnings use the same number space but are flagged in the
severity column.

## Severity legend

- **E** — Error. Halts compilation; fixture `EXIT` is non-zero.
- **W** — Warning. Reported on stderr; compilation continues
  (fixture `EXIT` is zero).
- **I** — Internal compiler error. Should never reach the user
  under correct input; if it does, it's a compiler bug. Halts
  compilation; the message includes "internal:" prefix; fixture
  `EXIT` is non-zero.

## Catalog

### E01xx — Lex / parse / name resolution

| Code | Sev | Message | Authoritative rule | Fix |
|---|---|---|---|---|
| E0101 | E | Unexpected character | `grammar.ebnf` lexical part | The character cannot start any token; remove it or quote it inside a string / rune literal. |
| E0102 | E | Unterminated literal | `grammar.ebnf` StringLit / RuneLit / BlockComment | Close the literal with the matching delimiter (`"`, `'`, or `*/`). |
| E0103 | E | Unknown name | `name-resolution.md` §Resolution algorithm | Declare the name, import the package, or fix the typo. |
| E0104 | E | Ambiguous variant name | `name-resolution.md` §Variant constructors | Use the qualified form `Type.Variant`. |
| E0105 | E | Duplicate field name | `type-system.md` §WF-Body-Record | Rename one of the colliding fields. |
| E0106 | E | Duplicate variant name | `type-system.md` §WF-Body-Sum | Rename one of the colliding variants. |
| E0107 | E | Reserved identifier prefix | `grammar.ebnf` Ident (`_tide_` prefix rejected) / `lowering-go.md` §Identifier encoding | Rename the identifier — `_tide_…` is reserved for codegen. |
| E0108 | E | Type used as value | `name-resolution.md` §Generic type-argument resolution | Use the type in a type position, or call `.new(...)` on a class, or use a brace literal. |
| E0109 | E | Malformed numeric literal | `grammar.ebnf` IntLit / FloatLit | A digit is missing or invalid for the radix (e.g. `0o9`, `0x`, bare `1e`). |
| E0110 | E | Malformed escape sequence | `grammar.ebnf` EscapeChar | Use one of the v1 escapes: `\n \t \r \\ \" \' \0 \xNN \uNNNN`. |
| E0111 | E | Malformed rune literal | `grammar.ebnf` RuneLit | A rune literal must contain exactly one character or escape sequence between single quotes. |
| E0112 | E | Unexpected token | `grammar.ebnf` syntactic part | The parser was looking for a different shape; check the surrounding construct. |
| E0113 | E | Duplicate top-level declaration | `name-resolution.md` §Scopes (package scope) | Two top-level `func`, `class`, `type`, or `interface` declarations in the package share a name (within one file or across two files of the same directory). Rename one or fold them together. |
| E0114 | E | Cyclic type alias | `type-system.md` §Alias resolution | The alias chain loops back on itself (`type A = B; type B = A`). Break the cycle by inlining one side or introducing a fresh nominal type. |
| E0115 | E | A variadic parameter must be the last parameter | `grammar.ebnf` §Param / `ffi.md` §Variadic | Move the `...T` parameter to the end of the list — only the final parameter may be variadic. |

### E02xx — Type system

| Code | Sev | Message | Authoritative rule | Fix |
|---|---|---|---|---|
| E0201 | E | Type mismatch | `type-system.md` (any rule with type-equality premise; unify) | Adjust the value or annotation to align types. |
| E0202 | E | Wrong arity | `type-system.md` T-Call, T-Variant-Payload, T-Tuple, P-Tuple | Supply the expected number of arguments / fields. |
| E0203 | E | Wrong return type | `type-system.md` T-Func-Decl | Match the function's declared return type or change the annotation. |
| E0204 | E | Integer literal out of range | `type-system.md` §Literals (narrowing) | Use a wider integer type or a literal within range. |
| E0205 | E | Illegal type conversion | `type-system.md` T-Conv (`ConvOK`) / `builtins.md` §Conversion functions | The pair isn't in `ConvOK`; for string ↔ int parse with `strconv.atoi` / format with `strconv.itoa`. |
| E0206 | E | `refEq` requires two operands of the same class or opaque foreign handle | `type-system.md` T-RefEq / `builtins.md` §Free functions / `ffi.md` §ExternType | Compare two values of the same class type or the same opaque handle; for cross-type comparison there is no v1 equivalent (rewrite the logic). |
| E0207 | E | Wrong type arity on generic instantiation | `type-system.md` WF-Named | Provide the expected number of type arguments. |
| E0208 | E | Cannot infer literal type | `type-system.md` §Slices, maps, sets, stacks (BraceKind=Unknown) | Add an explicit type annotation at the use site. |
| E0209 | E | `Dynamic` widening requires `reflect.box` | `type-system.md` T-Dyn-NoWiden / `builtins.md` §reflect | Wrap the value in `reflect.box(v)`. The only site that widens implicitly is a `reflect.*` parameter of formal type `Dynamic`. |
| E0210 | E | `Dynamic` narrowing requires `reflect.unbox` | `type-system.md` T-Dyn-NoNarrow / `builtins.md` §reflect | Recover a concrete type with `match reflect.unbox<T>(d) { Ok(t) => ..., Err(_) => ... }`. There is no implicit `Dynamic → T` cast. |
| E0211 | E | `Dynamic` in inferred type-parameter position | `type-system.md` §Dynamic (generic flow side condition) | Unification would set a user type parameter to `Dynamic` — rewrite the call so `T` is a concrete type, and pass the dynamic value through `reflect.box` / `reflect.unbox` explicitly. |
| E0212 | E | `Any` and `Dynamic` cannot be implicitly converted | `type-system.md` §Dynamic (cross-reference) / `builtins.md` §Special types | These are deliberately separate types — to go from one to the other, narrow to a concrete `T` first and then re-box. |
| E0213 | E | Spread argument `...` requires a variadic parameter | `type-system.md` T-Spread / `ffi.md` §Variadic | Use `...e` only as the final argument of a call whose last parameter is `...T`; otherwise pass the slice's elements individually. |

### E03xx — Pattern matching

| Code | Sev | Message | Authoritative rule | Fix |
|---|---|---|---|---|
| E0303 | E | Non-exhaustive match | `type-system.md` §match (exhaustive) | Add the missing arm(s) shown in the witness. |
| E0304 | E | Unreachable arm | `type-system.md` §match (Maranget) | Remove the dead arm; an earlier pattern already covers it. |
| E0305 | E | Float-literal patterns are not allowed | `type-system.md` §patterns | Replace with a wildcard + guard condition (`if x == 3.14`). |

### E04xx — Control flow

| Code | Sev | Message | Authoritative rule | Fix |
|---|---|---|---|---|
| E0401 | E | `==`/`!=` on non-comparable type | `type-system.md` T-Cmp / `builtins.md` §Comparable | Compare a field-wise; for class identity use `refEq`. |
| E0402 | E | `try` outside Result/Option-returning function | `type-system.md` T-Try-Result / T-Try-Option | Change the function return type, or replace `try` with explicit `match`. |
| E0403 | E | Error type of `try`'s sub-expression does not match the enclosing function's error type | `type-system.md` T-Try-Result | Make the error types equal, or wrap explicitly with `match`. |
| E0404 | E | `break`/`continue` outside a loop | `type-system.md` T-Break / T-Continue | Move the statement inside `for` / `while`. |
| E0405 | E | `spawn` outside a `scope` block | `type-system.md` T-Spawn | Wrap the call in `scope<T, error> { ... }`. |
| E0406 | E | `defer` argument must be a call | `type-system.md` T-Defer | Use a call expression, optionally wrapping in a closure: `defer (() => { ... })()`. |
| E0407 | E | `scope` error parameter must be `error` in v1 | `type-system.md` T-ScopeExpr / `lowering-go.md` §ScopeIR / SpawnIR | Use `scope<T, error>`; v2 will lift this restriction (typed-error adapter). |

### E05xx — Class scope and shadowing

| Code | Sev | Message | Authoritative rule | Fix |
|---|---|---|---|---|
| E0501 | E | `this` outside an instance-method body | `name-resolution.md` §Implicit receiver | Move the reference into an instance method, or drop `this`. |
| E0502 | E | **reserved** (v1 — Write-shadow of a field; shadow diagnostics deferred) | `name-resolution.md` §Shadowing — write-shadow | Rename the parameter / local, or qualify the write: `this.f = ...`. |
| E0503 | W | **reserved** (v1 — Soft shadow; shadow diagnostics deferred) | `name-resolution.md` §Soft shadows | Rename to make the shadow intent explicit, or accept the warning. |

### E06xx — Special names

| Code | Sev | Message | Authoritative rule | Fix |
|---|---|---|---|---|
| E0601 | E | `scope` outside a `scope { ... }` block | `name-resolution.md` §Special names | Use `scope` only inside the lexical body of a `scope` block. |

### E07xx — Desugaring (internal)

| Code | Sev | Message | Authoritative rule | Fix |
|---|---|---|---|---|
| E0701 | I | internal: non-exhaustive match reached desugaring | `desugaring.md` §Stage 5 | Compiler bug; file an issue with the offending `.td` file. |

### E08xx — Codegen / lowering (internal)

| Code | Sev | Message | Authoritative rule | Fix |
|---|---|---|---|---|
| E0801 | I | internal: un-desugared IR node reached codegen | `lowering-go.md` §Errors | Compiler bug; file an issue. |
| E0802 | I | internal: `Never`-typed value at a Go-typed position | `lowering-go.md` §Errors | Compiler bug; file an issue. |
| E0803 | I | internal: type-arg substitution failed | `lowering-go.md` §Errors | Compiler bug; file an issue. |

### E09xx — REPL input

Codes raised by `tide repl` (RFC-0003) when an input is not
admissible at the prompt. Coordinates use the synthetic file
`repl` followed by line:col within the input buffer.

| Code | Sev | Message | Authoritative rule | Fix |
|---|---|---|---|---|
| E0901 | E | Top-level control-flow not supported at the REPL prompt | RFC-0003 §What the REPL accepts | Wrap `if` / `for` / `while` / `match` in a function and call it. The function body still admits these constructs. |
| E0902 | E | `main` is owned by the REPL | RFC-0003 §What the REPL accepts | Drop the `func main() { ... }` wrapper — paste the body directly at the prompt. The REPL synthesises `main` itself. |
| E0903 | E | Unknown meta-command | RFC-0003 §Meta-commands | The set is `:help :quit :reset :imports :show :write[!] :type :inspect :load`. Type `:help` for the full list. |
| E0904 | E | **reserved** (`:write` target file already exists — `:write` not yet implemented) | RFC-0003 §Meta-commands | Use `:write! <file.td>` to overwrite, or pick a different name. |
| E0905 | E | **reserved** (Last-value binding is unbound — `_` / `_error` not yet implemented) | RFC-0003 §Auto-printing (`_` / `_error`) + §Open questions #2 (unbound-on-fresh-session) | Evaluate an expression first — `_` is bound to the last result; `_error` to the last runtime error. A fresh session has neither. |

### E10xx — Foreign bindings (Go FFI)

Codes raised by the `extern` foreign-binding surface (`ffi.md`). The
E06xx "special names" category is already taken, so FFI uses a fresh
E10xx range.

| Code | Sev | Message | Authoritative rule | Fix |
|---|---|---|---|---|
| E1001 | E | Cannot construct opaque foreign handle | `ffi.md` §ExternType / `type-system.md` T-Extern | An `extern type` has no visible layout — obtain the handle from an `extern func` (or an `extern impl` method) instead of a literal / constructor call. |
| E1002 | E | Cannot destructure opaque foreign handle | `ffi.md` §ExternType / `type-system.md` T-Extern | A handle has no fields/components to bind; use its `extern impl` methods/fields via member access instead of a tuple / record pattern. |

## Diagnostic formatting

Every diagnostic is emitted in this canonical format:

```
<path>:<line>:<col>: <severity-label>[<code>]: <message>
```

with optional secondary lines indented two spaces (snippet of
source, caret, fix hint). Example:

```
src/parser.td:42:14: error[E0201]: Type mismatch
  expected `int`, found `string`
  consider parsing with `strconv.atoi(...)` and `try`
```

Severity labels: `error` for E, `warning` for W, `internal` for
I. The bracketed code is mandatory and stable; fixture
comparison (`test-contract.md`) uses the code, not the message
alone.

For REPL inputs (codes E09xx) `<path>` is the literal string
`repl`; `<line>:<col>` is the position within the input buffer.

## Coverage invariant

Every rule that names a diagnostic code in another formal file
MUST have a row in this catalog with the same code and a
compatible message. The Formal-L closing audit cross-checks
every E-code reference in `lang-spec/` against this file —
unreferenced codes are flagged, undocumented codes (referenced
but missing) block the audit.

The reverse is NOT required: this file may add codes that
aren't yet referenced anywhere (reserved for future use), as
long as they're marked **reserved** in the message column.
Reserved codes: **E0502** / **E0503** (shadow diagnostics) — the
codes are allocated and `name-resolution.md` describes the
intended rules, but v1 does not yet enforce them (they need a
dedicated shadow-tracking name-resolution pass; no v1 program
requires them). **E0904** / **E0905** (REPL `:write` target-exists
and last-value-unbound) — reserved until their features (`:write`,
the `_` / `_error` bindings) land. Every other catalog row is live.
