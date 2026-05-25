# Reserved words, operators, and punctuation

This file is the **canonical, exhaustive** list of Tide's reserved
lexical surface. The lexer is generated from it; if a token does not
appear here, the lexer does not produce it and the parser does not
recognise it. Prose in `../docs/language-spec.md` mirrors this list;
on a disagreement, this file wins.

Updates require a paired update to the test corpus
(`../tests/lexer/`), the grammar (`grammar.ebnf`), and any affected
examples.

## Reserved keywords

These identifiers are **always reserved** — they cannot be used as
binding names, parameter names, field names, type names, or method
names.

### Declaration and control flow

```
import    type      class      interface
implements          extends    static
func      let       var        if         else
for       in        while      return
match     try       defer      spawn      scope      select
break     continue
```

`newtype` is reserved-in-principle for nominal newtypes (D11 open
issue — the v1 working placeholder is `newtype X = T`), but does not
appear in the corpus yet. It returns to this list with the PR that
ships its concrete syntax.

### Type literals

```
true      false     unit
```

`true` and `false` are the two `bool` values. `unit` is the type with
exactly one value, also written `()`.

### Receiver

```
this
```

`this` is the explicit form of the implicit receiver inside an
instance method (see `../docs/language-spec.md` §Classes). Outside class
methods, `this` is a syntax error.

## Contextual keywords

These tokens are keywords **only in specific positions**; outside
those positions they are ordinary identifiers.

| Token | Where it's a keyword | Elsewhere |
|---|---|---|
| `_`     | `let _`, `var _`, `for _ in`, function-parameter, `match` patterns | identifier |
| `case`  | inside `select { ... }` | identifier |
| `default` | inside `select { ... }` | identifier |

## Built-in identifiers (predeclared, NOT keywords)

These are predeclared in the top-level scope; user code may shadow
them but doing so is bad style. Full signatures live in
`builtins.md`.

- Types: `bool`, `int`, `int8`..`int64`, `uint`..`uint64`,
  `float32`, `float64`, `byte`, `rune`, `string`, `Any`,
  `error`.
- Generic types: `Result`, `Option`, `Map`, `Set`, `Stack`,
  `Channel`, `SendChan`, `RecvChan`.
- Variant constructors: `Ok`, `Err`, `Some`, `None`.
- Functions: `panic`, `error`, `refEq`, `makeChannel`, `makeSlice`.
- Conversion: `int`, `int64`, ..., `float64`, `byte`, `rune`, `string`
  also act as conversion functions (`int(x)`, `float64(n)`, ...).

## Operators

### Binary

| Operator | Meaning |
|---|---|
| `+`  | numeric add / string concat |
| `-`  | numeric subtract |
| `*`  | numeric multiply |
| `/`  | numeric divide (integer or float per operands) |
| `%`  | numeric remainder |
| `==` | equality |
| `!=` | inequality |
| `<`  | less-than (comparable primitives only) |
| `<=` | less-than-or-equal |
| `>`  | greater-than |
| `>=` | greater-than-or-equal |
| `&&` | logical AND, short-circuit |
| `\|\|` | logical OR, short-circuit |
| `\|` | sum-type variant separator (`type X = \| A \| B(...)`) and pattern alternative inside `match` (`'(' \| '[' \| '{' => ...`). Not an expression-level operator |

### Unary

| Operator | Meaning |
|---|---|
| `!` | logical NOT |
| `-` | numeric negation |

### Assignment and binding

| Operator | Meaning |
|---|---|
| `=`  | initialiser in a `let` / `var` declaration; assignment to a `var` binding or `var` field; right-hand side of a record / map literal field (`k: v` form). Not a comparison operator |
| `let` / `var` | binding declaration (keywords above; not operators) |

There is no compound assignment in v1 (`+=`, `-=`, ... are not
recognised). Write `x = x + 1` explicitly.

### Receive operator

| Operator | Meaning |
|---|---|
| `<-ch` | channel receive — **only** legal inside a `select` case |

## Punctuation

```
(  )       parens — grouping, parameter list, call args
[  ]       brackets — slice literal, slice indexing/slicing
{  }       braces — block, record literal, class body, scope body
<  >       angle brackets — generic type / function arguments
.          field access, method call, tuple-position access (t.0)
,          separator in lists / args / tuples
;          statement terminator (optional — newline normally suffices)
:          type annotation (`x: int`), record / map literal field (`k: v`),
           variant payload field (`name: T`), slice slicing (`s[a:b]`)
->         return-type arrow — **not used in v1** (function return is `:`).
           Reserved for future use.
=>         arm separator in `match` and in short-closure literals
           (`(a, b) => a < b`)
..         half-open range (`a..b`)
..=        inclusive range (`a..=b`)
...        variadic parameter and variadic spread (e.g. `args: ...Any`)
@          reserved for future use (annotations / attributes — not in v1)
```

## Lexical conflict resolution

- **`<` and `>` as comparison vs generic brackets** — the parser uses
  one-token lookahead and context: in type-expression position and
  immediately after a type / function name, `<` opens a generic
  argument list; in expression position it is comparison. Ambiguous
  cases (`f<a, b>(c)`) follow Go's "if the next non-whitespace after
  `>` is `(` or a left-paren-leading expression, treat as generic
  arguments" rule.
- **`..` and `..=` versus member access** — both are punctuated forms
  (`a..b`, `a..=b`); the lexer is greedy and produces the longest
  match. Member access stays `.` between non-digit identifiers; tuple
  position `t.0` is `.` followed by an integer literal.
- **`-` unary vs binary** — disambiguation by parser context; the
  lexer produces a single `-` token either way.
- **`!` unary only** — there is no postfix `!`; `!a` is unary
  negation, never a method invocation.

## What is NOT a keyword (deliberately)

The following words look reserved in other languages but are
ordinary identifiers in Tide; user code is free to shadow them
(though bad style):

- `goto`, `do`, `enum`, `struct`, `pub`, `fn`, `async`, `await`,
  `yield`, `throw`, `catch`, `finally`, `with`, `assert`, `nil`,
  `null`, `undefined`, `new`, `delete`, `self`, `super`.

`async`/`await`/`yield` are explicitly cut by D7. `nil`/`null`/
`undefined` are absent because `Option<T>` is the nullable type
(D2). `goto`, `do`, `super`, `new`, `delete` have no role in the
v1 surface.
