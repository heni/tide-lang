# Language Specification (working draft)

> **Draft.** A sketch, not a frozen spec. Several surface-syntax questions
> are still open and are being resolved as the example acceptance suite
> forces them. Expect churn through the early phases. The acceptance suite
> in [`../examples/README.md`](../examples/README.md) is the working ground
> truth — anything the suite uses must be specified here.

## Lexical

- Line comments `//`; block comments `/* ... */`.
- Identifiers: letter or `_`, then letters/digits/`_`.
- Statements are newline-terminated; semicolons optional. Newlines inside
  open brackets — `(...)`, `[...]`, `{...}`, `<...>` — do **not** terminate
  a statement, so multi-line literals and call expressions work
  unsurprisingly. Trailing commas are permitted (and recommended) in
  multi-line literals.
- Source files use the `.td` extension.
- String literals: `"..."`. Standard escapes (`\n`, `\t`, `\\`, `\"`,
  `\xNN`, `\uNNNN`).
- Rune literals: single-quoted character: `'a'`, `'('`, `'\n'`, `'ÿ'`.
  A rune literal is of type `rune`.
- Integer literals: `42`, `0xFF`, `0o755`, `0b1010`, with `_` separators
  permitted: `1_000_000`.
- Float literals: `3.14`, `1e9`, `2.5e-3`.

## Imports

```td
import fmt
import encoding/json
```

An import brings a Go (bound) or Tide package into scope under its package
name. Members are accessed package-qualified: `fmt.println`, `json.parse`.
All imports must appear at the top of the file, before any declarations.

## Types

Primitives: `string`, `bool`, `int`, `int8..int64`, `uint..uint64`,
`float32`, `float64`, `byte`, `rune`.

`unit` is the type with exactly one value, also written `()`. Functions
with no declared return type return `unit`; codegen erases it.

`Any` is an escape type used **only** at the binding boundary for Go's
`interface{}`/`any` parameters (e.g. variadic formatters). Concrete types
implicitly widen to `Any` at call sites that expect it. Going back to a
concrete type requires a typed `match` (form TBD). User-authored Tide code
should not introduce `Any`-typed parameters in its own functions.

Struct shapes use `type` and are **records** (value types, structural —
see Records below):

```td
type User = {
  id: string
  name: string
}
```

**Tuples** are anonymous product types: `(int, string)`, `(int, int, int)`.
Construct with `(a, b, ...)`, destructure with `let (a, b) = pair`, match
with the same pattern, access by position `t.0`, `t.1` (discouraged for
arity > 2 — prefer a record). Tuples must have arity ≥ 2; `(a)` is just
a parenthesised expression. Equality is structural.

Nominal newtypes — distinct types over an underlying type, with their own
method set. Required because Tide must faithfully represent types like
`time.Duration` (a Go `type Duration int64` with methods). Syntax TBD;
working placeholder `newtype UserId = string`.

Generics use angle brackets: `List<T>`, `Map<string, int>`, `func<T>(...)`,
`class LRU<K, V> { ... }`. Type parameters are unconstrained in v1
(bounded generics — `<T extends Comparable>` — are park material).

There is **no `any`**. Untyped dynamic values do not exist.

## Bindings

`let name: T = expr` — immutable binding; cannot be reassigned.
`var name: T = expr` — mutable binding; may be reassigned with `name = ...`.

Type annotations are optional when the type can be inferred from the
right-hand side or from a target context:

```td
let n = 42                  // inferred int
let xs: []int = []          // empty literal needs context
var cur: Option<Node> = head
```

**Discard pattern.** `_` is a write-only binding that evaluates its
right-hand side and ignores the value:

```td
let _ = sideEffect()
```

Reading `_` is a compile error. Multiple `_`s in the same scope do not
shadow each other.

## Collections

### Slices: `[]T`

- Type: `[]T` (`[]int`, `[]string`, `[]Interval`, ...).
- Literal: `[1, 2, 3]` (element type inferred from context).
- Empty literal: `[]int{}` (explicit type required when context is
  insufficient).
- `s.len(): int`.
- `s.push(v): []T` — returns a new slice header (may grow underlying
  storage). Idiomatic re-assignment: `s = s.push(v)` when `s` is `var`.
- Indexing: `s[i]` — out-of-bounds panics at the `.td` site.
- Safe access: `s.get(i): Option<T>`.
- Slicing: `s[a:b]: []T` — half-open view into the same backing array.
  `s[a:]` and `s[:b]` are shorthand for `s[a:s.len()]` and `s[0:b]`.
  Out-of-bounds panics.
- Index assignment: `s[i] = v` — write at index `i`; out-of-bounds
  panics. Legal when `s` is a `var` binding or a `var` field on a
  class. Strings are immutable — index assignment on a string is a
  compile error.
- Copy: `s.copy(): []T` — returns a new slice with the same elements,
  independent backing storage. Useful when you want to mutate without
  aliasing the original (e.g. the per-step working copy in a
  wavefront).
- Iteration: `for v in s` (value), `for (i, v) in s` (index and value).

### Maps: `Map<K, V>`

- Type: `Map<K, V>`.
- Empty literal: `Map<K, V>{}`.
- Map literal with entries: `Map<string, int>{ "a": 1, "b": 2 }` —
  quoted keys disambiguate a map literal from a record literal.
- `m.get(k): Option<V>` — `Option`-wrapped to remove Go's
  zero-value-with-ok pitfall.
- `m.set(k, v)`, `m.has(k): bool`, `m.delete(k)`, `m.len(): int`.
- Iteration: `for k in m` (keys, order unspecified — matches Go) and
  `for (k, v) in m` (entries).

### Sets: `Set<T>`

A built-in class (reference type), the obvious sibling of `Map<K, V>`
and `Stack<T>`. Two examples in the AoC port (`d07`, `d11`) use a set
for membership tests; re-implementing one per example wastes pages.

- Construct empty: `Set<T>{}`.
- Literal with members: `Set<int>{ 1, 2, 3 }` — comma-separated values
  without `:`, distinguishing it from a map literal.
- `s.add(v): unit` — insert; idempotent.
- `s.has(v): bool`.
- `s.delete(v)`.
- `s.len(): int`.
- Iteration: `for v in s` — order unspecified (matches Go's
  map-backed set idiom).

Set operations (`union`, `intersect`, `difference`) are park material;
v1 needs only the four core ops.

### Stacks: `Stack<T>`

A thin generic built-in. The two acceptance examples that need a stack
(`interview/rpn_calculator`, `leetcode/valid_parentheses`) would
otherwise duplicate the same data structure.

- Construct: `Stack<T>{}`.
- `s.push(v): unit` — mutates the receiver (`Stack<T>` is a class —
  reference type, no reassignment needed).
- `s.peek(): Option<T>`.
- `s.len(): int`.
- `s.pop(): Result<T, error>` — error is `"stack underflow"`. Returning
  `Result` (not `Option`) so `try s.pop()` is the idiomatic shrink-and-use
  form inside a `Result`-returning function.

## Strings

`string` is a sequence of bytes by storage (matches Go), but iterates as
**runes** by default:

- `for c in s` — iterates `rune` values.
- `for (i, c) in s` — iterates byte-index/rune pairs (matches Go's
  `for i, r := range s`).
- `s.len(): int` — **byte** length (matches Go and the underlying memory
  representation).
- `s.bytes(): []byte` — view as bytes.
- `s.runes(): []rune` — collect runes into a slice; use `.runes().len()`
  for rune count.
- Concatenation: `+`. No implicit conversion from non-string operands —
  use `strconv.itoa(n)` etc., or rely on `Any`-widening variadic
  formatters like `fmt.println(...)`.
- **Indexing and slicing are byte-based** (mirrors Go):
  - `s[i]: byte` — byte at byte index `i`. Out-of-bounds panics.
  - Safe form: `s.byteAt(i): Option<byte>`.
  - `s[a:b]: string` — substring from byte indices `[a, b)`. `s[a:]`
    and `s[:b]` are shorthand for `s[a:s.len()]` and `s[0:b]`. Slicing
    a multi-byte UTF-8 sequence at a non-boundary is allowed and
    produces an invalid UTF-8 string; rune-safe slicing goes through
    `.runes()`.
- **Equality.** `==` compares strings by content. Reverse slicing
  (`s[::-1]`) and stepped slicing are not in v1.

## Sum types

A `type` whose right-hand side is a union of variants. Variants are
nullary or carry **positional** named-typed fields:

```td
type Tree<T> =
  | Leaf
  | Node(value: T, left: Tree<T>, right: Tree<T>)

type Event =
  | InsertCoin(amount: int)
  | Select(item: Item)
  | Refund
```

- Construction: nullary `Leaf`; payload-bearing `Node(v, l, r)` —
  positional, matching the declaration order.
- Pattern: same positional shape — `Node(v, l, r)`, `Leaf`,
  `InsertCoin(n)`, `_` (wildcard).
- `Result<T, E>` (`Ok(T) | Err(E)`) and `Option<T>` (`Some(T) | None`)
  are built-in sum types using the same machinery.

Named-payload variants (e.g. `Node{value: T, left: Tree<T>}`) are park
material; positional is the one obvious form for the typical small
payloads.

## Pattern matching

`match` is an **expression**. Each arm is an expression; the arms must
unify to one type. An arm body may use a block `{ ... <trailing
expression> }`; the trailing expression is the arm's value.

```td
let v = match getUser(id) {
  Ok(user) => user.name,
  Err(e)   => "<unknown: " + e.error() + ">",
}
```

`match` is **exhaustive**; a non-exhaustive `match` is a compile error.

Patterns support:

- Literals (`'('`, `42`, `"key"`).
- Wildcards (`_`).
- Alternatives (`'(' | '[' | '{'`).
- Variants with positional payloads (`Some(x)`, `Node(v, l, r)`,
  `Dispensing(_, change)`).
- Tuples (`(Idle, InsertCoin(n))`, `(s, e)`).
- Records by name (`User{ id, name }`) — punning omitted in v1.

A `match` used at statement position discards its value; the arms must
all type to `unit`.

## Expressions

- **Blocks** are expressions: `{ stmt; stmt; ... trailingExpr }`. The
  block's value is the trailing expression's value, or `unit` if there
  is no trailing expression. `return` and `try` short-circuit.
- **`if` / `else`** is an expression: arms must unify. An `if` without
  `else` has type `unit` and may only appear at statement position.
- **`match`** is an expression (see above).

**Equality.** `==` and `!=`:

- Strings, primitive numbers, booleans, runes — by content.
- Tuples, records, sum-type values — by structural / value equality
  (recursive component-wise).
- Class instances — by **reference** identity (two distinct instances
  with identical fields are not `==`). For explicit reference equality,
  the built-in `refEq<T>(a, b): bool` is the readable spelling (G26).

**Boolean operators.** All three short-circuit; both operands and the
result must be `bool`. There is no implicit truthiness — `0`, `""`,
`None`, and empty collections are *not* `false`.

- `a && b` — AND. `b` is not evaluated if `a` is `false`.
- `a || b` — OR. `b` is not evaluated if `a` is `true`.
- `!a`    — negation.

Precedence (high → low): `!`, `&&`, `||`. Same as Go and C.

**Numeric and comparison operators.** `+ - * / %` on numeric types;
`< <= > >=` on comparable primitive types (numbers, strings, runes,
bools by `false < true`).

## Functions

```td
func add(a: int, b: int): int {
  return a + b
}
```

Top-level functions use the `func` keyword. Methods inside a `class` or an
`interface` declaration do not (see Classes, Behavioral types).

**Function types** (for first-class function values):

- Type: `func(T, U): R` (parens around params, colon-return).
- Zero-param form: `func(): R`.
- Unit return: `func(T, U)` — the `: unit` is omitted. `func()` is
  shorthand for `func(): unit` — typical for cleanup/cancel callbacks
  (e.g. the second element of `context.withTimeout(...)`).
- Closure literal: `func(a: T, b: U): R { body }` — same shape, anonymous.
- Short closure: `(a, b) => a < b` — when parameter types are inferable
  from context (e.g. a comparator argument).
- Variadic: `func print(args: ...Any)`. At the call site, individual
  arguments — including concrete values *and* interface values — widen
  to `Any`.
- Parameters may use the discard pattern `_` (e.g. `func cb(_: int,
  name: string)`). Useful when implementing an interface that demands
  a parameter the body does not need.

## Error handling

`Result<T, E>` is the error type. `error` is a built-in interface
(structurally identical to Go's `error`: one method `error(): string`),
so `(T, error)`-returning Go functions bind directly to `Result<T,
error>`. The built-in `error(msg: string): error` constructs a basic
error. A `class` may declare `implements error` for typed errors.

`try e` propagates the error arm early:

- In a `Result<_, E>`-returning function: `try e` where `e: Result<T,
  E>` evaluates to `T` on `Ok(T)`; on `Err(E)` the function returns
  `Err(E)`. (No implicit error conversion in v1; the error types must
  match.)
- In an `Option<T>`-returning function: `try e` where `e: Option<U>`
  evaluates to `U` on `Some`; `None` returns `None` from the function.

For inspection rather than propagation, use `match`. Mapping Go's
`errors.Is`/`errors.As`/`%w` wrapping onto Tide error values is a later
design item.

## Control flow

```td
if x > 0 { ... } else if x < 0 { ... } else { ... }

for v in items     { ... }   // value iteration
for (i, v) in xs   { ... }   // index + value (slices)
for (k, v) in m    { ... }   // entries (maps)
for c in str       { ... }   // runes (strings)
for i in 1..=100   { ... }   // inclusive range
for i in 0..n      { ... }   // half-open range
while cond         { ... }

return                     // unit return
return value
```

Range forms: `a..b` half-open `[a, b)`; `a..=b` inclusive `[a, b]`. No
stepped ranges in v1.

The discard pattern `_` is valid in any `for` binder position:
`for _ in 1..=n` (iterate n times, ignore the index), `for (_, v) in s`
(value-only over a slice), `for (k, _) in m` (keys-only over a map).

**`break` and `continue`.** Inside any `for` or `while` loop:

- `continue` — skip to the next iteration of the enclosing loop.
- `break`    — exit the enclosing loop.

There is no labelled / multi-level form in v1. Inside a `select` case
body, `continue` and `break` refer to the enclosing loop (not the
select).

`defer expr` queues `expr` (typically a method call) to run when the
enclosing **function** returns, in LIFO order. Function-scoped, not
block-scoped (matches Go).

## Records

Records (`type X = {...}`) are **structural** value types (D14 — see
`design-decisions.md`): two records with the same fields are
interchangeable. They are copied on assignment, like Go structs.

**Construction.** Named fields, all required unless the field type is
`Option<T>`:

```td
let u = User{ id: "x", name: "y" }
```

No positional form (rejected: short records' "obvious" order is exactly
the case people get wrong). Anonymous record literals — `{ x: 1, y: 2 }`
— are accepted only when the target type is inferable from context.

## Classes

Classes are **reference types** (D14, G16): assignment copies the
reference; mutating a `var` field through any reference is visible
through every reference to the same instance.

```td
class Counter {
  var n: int

  increment()  { this.n = this.n + 1 }
  value(): int { return this.n }

  static new(): Counter { return Counter{ n: 0 } }
}
```

**Field declarations** use `let` or `var` at the top of the class body:

- `let field: T` — set once at construction; later assignment is a
  compile error.
- `var field: T` — assignable through `inst.field = value` from any
  reference. Visibility (public/private) is not yet a concept; all
  fields are effectively public in v1.

**Methods** — declared without `func`:

```td
class MyReader implements io.Reader {
  read(p: []byte): Result<int, error> { ... }
}
```

`this` is the implicit receiver. The pointer-vs-value-receiver
distinction is a codegen concern, not a surface concern.

**Static methods** — declared with `static`, called as `ClassName.name(...)`
or `ClassName<T>.name(...)`:

```td
class LRU<K, V> {
  static new(capacity: int): LRU<K, V> { ... }
}

let cache = LRU<string, int>.new(2)
```

**Generic classes** — `class Name<T1, T2> { ... }`. Type parameters are
in scope throughout the class body.

**Interface conformance** — explicit, declared, checked:

```td
interface Reader {
  read(p: []byte): Result<int, error>
}

class MyReader implements Reader {
  read(p: []byte): Result<int, error> { ... }
}
```

Interface method declarations omit `func`, like methods in a class.

There is no implicit or accidental satisfaction (D14). `implements`
works for both Tide-defined interfaces and bound Go interfaces, so a
Tide class can explicitly implement e.g. `io.Reader` or `http.Handler`.
Codegen emits a static conformance assertion; a mismatch fails in
Tide's checker, not in generated Go. Method-set semantics follow Go's
(value- vs pointer-receiver).

**Interface composition** — an interface may aggregate other interfaces
with `extends`. The composed interface requires the union of the method
sets:

```td
interface ReadCloser extends Reader, Closer { }

// Equivalent to writing read() and close() out explicitly. Extra
// methods may be added in the body:
interface CountingReader extends Reader {
  bytesRead(): int
}
```

`extends` is interface-only — classes use `implements` to declare
conformance, not to inherit. There is no class inheritance in v1.

**Anonymous interface as a type** — an interface shape can be used
inline as a type expression, the same way records can:

```td
type Signal = interface {
  signal(): unit
  string(): string
}
```

The shape is anonymous and nominal-by-the-declaration-site: two
distinct `type X = interface { ... }` aliases with identical method
sets are *different* types (D14). For ad-hoc structural matching in
generic code, use a named `interface` declaration plus `extends`.

For the bound-Go side, method names are uniformly rewritten:
`ServeHTTP` (Go, exported) is exposed in the binding as `serveHTTP`,
and the Tide class declares `serveHTTP`. Codegen translates back to
`ServeHTTP` for the synthesized Go method. This is a binding-layer
convention, not a language rule.

**Reference equality.** Class instances support `refEq<T>(a: T, b: T):
bool` (built-in, defined only for class `T`). For records and variants,
`==` is field-wise structural equality.

## Concurrency

Concurrency is uncolored (D7 — see `design-decisions.md`): no `async`,
no `await`.

### Channels

Three channel types:

- `Channel<T>` — bidirectional. The type returned by `makeChannel<T>()`.
- `SendChan<T>` — send-only view. A `Channel<T>` widens to `SendChan<T>`
  implicitly at a call site or assignment that expects it; the reverse
  is not allowed.
- `RecvChan<T>` — receive-only view. Same widening rules.

The directional views exist so binding signatures can faithfully reflect
Go's `chan<- T` and `<-chan T` parameter shapes (e.g. `signal.notify`
takes a `SendChan<os.Signal>`; `context.Context.done()` returns a
`RecvChan<unit>`). They also let user code declare intent at the
producer/consumer boundary.

```td
let ch = makeChannel<int>()           // Channel<int>, unbuffered
let bc = makeChannel<Event>(16)       // Channel<Event>, capacity 16

ch.send(1)
let x = ch.recv()                     // blocks until a value or close
let v = ch.tryRecv()                  // Option<T>, non-blocking
ch.close()

for v in ch { ... }                   // ranges until closed

// Directional widening:
func produce(out: SendChan<int>) { out.send(42) }
func consume(in: RecvChan<int>)  { let _ = in.recv() }
let pipe = makeChannel<int>()
produce(pipe)
consume(pipe)
```

`SendChan<T>` supports `.send(v)` and `.close()`. `RecvChan<T>` supports
`.recv()`, `.tryRecv()`, and `for v in c`.

### `spawn`

`spawn { ... }` runs a block concurrently (compiles to a goroutine). The
block is a **function-shaped scope**: `return Ok(v)` / `return Err(e)`
inside the block returns from the spawn (not from the surrounding
function), and `try expr` inside the block early-returns with `Err`. The
block's result type is the scope's task result (`Result<T, E>` for a
`scope<T, E>`).

Spawns may only appear inside a structured-concurrency `scope` (below)
— there is no detached/orphaned goroutine form in v1.

### `select`

```td
select {
  case s = <-sigs        => { ... },    // receive into a binding
  case <-ctx.done()      => { ... },    // receive, drop the value
  case events.send(e)    => { ... },    // send
  default                => { ... },    // optional non-blocking case
}
```

The `<-ch` receive operator is **only** valid inside a `select` case; in
plain code use the method form `ch.recv()`. Symmetry: `ch.send(v)` works
both inside and outside `select`.

### Structured concurrency

```td
let pages = try scope<Page, error> {
  for u in urls {
    spawn { try fetch(u, timeout) }
  }
}
```

A `scope<T, E> { ... }` runs the spawned tasks, joins them when the
block ends, and:

- Returns `Result<[]T, E>` when each spawn returns `Result<T, E>`.
- Returns `Result<unit, E>` when spawns are fire-and-forget (return
  `Result<unit, E>`).
- The first `Err` cancels its siblings via the scope's context and is
  the scope's result.

**`scope.context`** is an identifier bound only in the **lexical body**
of a `scope { ... }` block — including in any nested `spawn` blocks. A
function called from inside a spawn does *not* inherit `scope.context`
dynamically; if it needs the scope's context, pass it as a parameter.
Inside the lexical scope, `scope.context: context.Context` is the
cancellable context to pass into bound Go calls.

**Nested scopes and cancellation.** A `scope<T, E>` accepts an optional
`context.Context` argument that becomes the scope's parent:

- `scope<T, E> { ... }` — root scope. Internal context derives from
  `context.background()`.
- `scope<T, E>(parent) { ... }` — inner scope. Internal context
  derives from `parent`. When `parent.done()` fires (e.g. because the
  outer scope cancelled), this scope cancels every running spawn.

The explicit-parent form is how cancellation propagates across scope
boundaries without breaking the lexical-binding rule above: callees that
open inner scopes take `ctx: context.Context` as a parameter and pass
it as `scope<T, E>(ctx) { ... }`.

## Examples

Target programs that v1 must compile are catalogued in
[`../examples/README.md`](../examples/README.md). Each example is the
definition of done for the features it exercises (D12).
