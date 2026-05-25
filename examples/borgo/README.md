# `borgo/` — Tide ports of Borgo codegen-emit test programs

[Borgo](https://github.com/borgo-lang/borgo) is the closest existing
competitor to Tide: a small statically typed language that compiles to
Go, with a Rust-flavoured syntax and ML-family type system. Its
[snapshot test suite](https://github.com/borgo-lang/borgo/tree/main/compiler/test/snapshot/codegen-emit)
is a small, focused corpus — one program per language feature — that
makes for a direct apples-to-apples test of where Tide's surface
differs and where it falls short.

This directory ports five Borgo snapshot programs to Tide, each with a
header comment pointing at the original `.exp` file and a short
side-by-side. The point is **comparison**, not coverage:

| Tide file | Borgo source | What the comparison shows |
|---|---|---|
| [`interfaces.td`](interfaces.td) | `interfaces.brg` | Tide's `class X implements I { ... }` groups the data and the conformance in one place; Borgo splits `struct X` from `impl (x: X) {...}`. Interface composition: Tide uses `extends`, Borgo uses `impl Foo` as an interface-body row. |
| [`match_on_tuples.td`](match_on_tuples.td) | `match-on-tuples.brg` | Tuple-match is essentially identical. Tide forbids arity-1 tuples and uses `panic(msg)` where Borgo uses `@unreachable()`. |
| [`errors_as_types.td`](errors_as_types.td) | `errors-as-custom-types.brg` | A class `implements error` mirrors Borgo's `impl (f: FooErr) { fn Error() }`. Tide uses `try` where Borgo uses `?`. Tide's nominal interfaces (D14) mean you don't get the implicit `FooErr → error` widening Borgo does. |
| [`defer_demo.td`](defer_demo.td) | `defer-statements.brg` | Identical — both adopt Go's `defer` (G27). |
| [`concurrency.td`](concurrency.td) | `concurrency.brg` | Borgo mirrors Go directly: `Channel.new()` returns a `(sender, receiver)` pair, `spawn (|| {...})()` IIFE, explicit `sync.WaitGroup.Add/Done/Wait`. Tide replaces all of this with structured concurrency: `scope<T, E>` joins, `spawn { ... }` registers, `makeChannel<T>(buf)` returns a bidirectional channel that widens to `SendChan<T>` / `RecvChan<T>` at parameter sites. |

## Spec additions surfaced by this port

- `panic(msg: string)` and `os.exit(code: int)` are now explicitly
  marked as **diverging built-ins** in `docs/language-spec.md`
  §Error handling — they never return and unify with any expected
  type at the call site (so they can occupy a `match` arm or any
  other typed position).

The two big architectural differences between Tide and Borgo that this
batch makes visible:

1. **Structured concurrency over `sync.WaitGroup`.** Borgo exposes
   Go's manual barrier; Tide buries it inside `scope`. Each style has
   its own audience: Borgo readers transitioning from Go see the
   familiar primitives; Tide readers see the goroutine machinery
   already folded into the language.
2. **Class-on-class versus struct-plus-impl.** Borgo follows Rust's
   data/impl split; Tide follows Java/Swift's data-with-methods. The
   ergonomic difference is small, the conceptual difference is real —
   Tide's grouping says "this data has these methods", Borgo's says
   "this data can be extended later with these methods."
