# Language Specification (working draft)

> **Draft.** A sketch, not a frozen spec. Several surface-syntax questions are
> still open — they are being resolved as the example acceptance suite forces
> them. Expect churn through the early phases.

## Lexical

- Line comments `//`; block comments `/* ... */`.
- Identifiers: letter or `_`, then letters/digits/`_`.
- Statements are newline-terminated; semicolons optional.
- Source files use the `.td` extension.

## Imports

```td
import fmt
import encoding/json
```

An import brings a Go (bound) or Tide package into scope under its package
name. Members are accessed package-qualified: `fmt.println`, `json.marshal`.

## Types

Primitives: `string`, `bool`, `int`, `int8..int64`, `uint..uint64`, `float32`,
`float64`, `byte`, `rune`.

Struct shapes use `type`:

```td
type User = {
  id: string
  name: string
}
```

Nominal newtypes — distinct types over an underlying type, with their own
method set (required — D11; needed for e.g. `time.Duration`). Syntax TBD;
placeholder `newtype UserId = string`.

Generics use angle brackets: `List<T>`, `Map<string, int>`, `func<T>(...)`.

There is **no `any`**. Untyped dynamic values do not exist.

## Sum types

A `type` whose right-hand side is a union of variants. Variants are nullary or
carry named fields:

```td
type Tree<T> =
  | Leaf
  | Node(value: T, left: Tree<T>, right: Tree<T>)
```

`Result<T, E>` (`Ok(T) | Err(E)`) and `Option<T>` (`Some(T) | None`) are
built-in sum types using the same machinery.

## Functions

```td
func add(a: int, b: int): int {
  return a + b
}
```

## Error handling

`Result<T, E>` is the error type. `try` propagates the error arm early:

```td
func load(path: string): Result<string, error> {
  let data = try os.readFile(path)
  return Ok(data)
}
```

For inspection rather than propagation, use `match`. Mapping Go's `errors.Is` /
`errors.As` / `%w` wrapping onto Tide error values is a Phase-2 design item.

## Pattern matching

```td
match getUser(id) {
  Ok(user) => fmt.println(user.name),
  Err(e)   => fmt.println("error:", e),
}
```

`match` is exhaustive; a non-exhaustive `match` is a compile error. Patterns
support literals, tuples, and `|` alternatives, with `_` as wildcard.

## Control flow

```td
if x > 0 { ... } else if x < 0 { ... } else { ... }

for i in 1..=100 { ... }      // inclusive range
for item in items { ... }     // iteration
while cond { ... }
```

## Records and behavioral types

Tide distinguishes data from behavior (decision D14).

**Records** — `type X = {...}` data shapes — are **structural**: two records
with the same fields are interchangeable.

**Behavioral types** — `class`es and Tide-defined `interface`s — are
**nominal** with **explicit, declared conformance**. A type satisfies an
interface only when it declares `implements` and the checker verifies the
method set. There is no implicit or accidental satisfaction.

```td
interface Reader {
  read(p: []byte): Result<int, error>
}

class MyReader implements Reader {
  read(p: []byte): Result<int, error> { /* ... */ }
}
```

`implements` works for both Tide-defined interfaces and bound Go interfaces, so
a Tide class can explicitly implement e.g. `io.Reader`. Codegen emits a static
conformance assertion, so a mismatch fails in Tide's checker, not in generated
Go. Method-set semantics follow Go (value- vs pointer-receiver).

## Concurrency

```td
let ch = makeChannel<int>()
spawn {
  ch.send(1)
}
let x = ch.recv()
```

No `async` (decision D7). Structured scopes and cancellation: see
`docs/architecture.md` section 4.

## Examples

Target programs that v1 must compile are catalogued in `examples/README.md`.
