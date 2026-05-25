# `raku/` — Raku-inspired spec stress tests

[Raku](https://raku.org/) (formerly Perl 6) has the single most
exhaustive language spec test suite ever assembled —
[`roast`](https://github.com/Raku/roast) — organised by Synopsis
chapter (`S02-lexical-conventions`, `S03-operators`, …). The full
suite would be insane to port; the *idea* of it — drive the spec into
its corners and see what holds — is worth borrowing.

The cut list is huge, on purpose. Raku's distinctive features (sigils,
twigils, multi-dispatch, slangs, Whatever-stars, junctions as
first-class types) sit on Tide's permanent rejection list under D2,
D7, and D14. We are not porting *Raku*; we are using *roast* as an
ideas-pump and writing the closest Tide expression of a few patterns
Raku tests rigorously.

## What this directory has

| File | Inspired by | What it stress-tests in Tide |
|---|---|---|
| [`set_algebra.td`](set_algebra.td) | `S03-operators/set_*` | Whether `Set<T>` (G48) plus the basic four ops is enough to reconstruct the full algebra (∪, ∩, −, △, ⊆) in user code. It is — but the reconstruction is a hint that future Tide could ship `Set<T>` value-equality (`setEq` → `==`) and a small built-in operator set. Park material. |
| [`deep_destructure.td`](deep_destructure.td) | `S05-pattern-matching` | Composing the existing pattern rules (G8 match, G19 variant, G24 tuple, §Records) at depth: variant of record of (tuple, sum). Spec composes cleanly modulo one fold-back from the first review: variant payload slots **must carry a field name** (`Move(info: StepInfo)`, never `Move(StepInfo)`), matching the existing suite. Also confirms that **record-field punning is intentionally not in v1**, so `User{ id, name }` patterns must be written as `User{ id: id, name: name }` — verbose but unambiguous. |
| [`error_chain.td`](error_chain.td) | `S04-exceptions` (`fail`, `try`, `CATCH`) | Three-stage `Result<T, ParseError>` pipeline using prefix `try` for early-return, single `class ParseError implements error` carrying stage + detail. Demonstrates that Tide's `Result` + `try` covers what Raku's exception throw / catch covers, with the type-level discipline Raku's runtime checks dynamically. |

## What's NOT here

Deliberately. These Raku features stay on the cut list:

- **Junctions** (`S03-junctions`) — `any(1, 2, 3) == 2` style parallel
  boolean. Tide makes the user write `[1, 2, 3].contains(2)` or
  similar. The compactness loss is small; the magic-elimination is
  large.
- **Sigils / twigils** — `$x`, `@y`, `%z`, `$*FOO`, `$?LINE`. Tide
  uses bare names. (D2.)
- **Multi-dispatch** (`S06-multi`) — same method name, multiple
  signature-based implementations. Conflicts with D14 (nominal
  conformance, explicit `implements`) and accidental compatibility
  is exactly what D14 forbids.
- **Slangs** (compile-time grammar reflection) — out of scope at
  every conceivable level.
- **Whatever-stars** (`*+1`, `*.method`) — implicit-currying lambdas
  with `*` placeholder. Tide uses explicit `(x) => x + 1`.
- **`fail`** (sentinel return) — Tide returns `Err(...)` explicitly.

## Spec audit conclusions

Walking three Raku-flavoured stress patterns through the existing
Tide spec turned up:

- **`Set<T>` value equality (no surfacing yet, candidate).** `setEq`
  by hand works; if `==` ever needs to be value-equal on sets,
  it's a small extension (operator overloading on built-ins).
- **Deep pattern destructuring composes** — no new spec needed.
- **Error-chain ergonomics are tight** — `try` + `class … implements
  error` carries the same context Raku's exceptions do, statically.

These are positive results: the v1 spec is roughly the right size,
and the things Tide cuts (junctions, multi-dispatch, sigils) are
deliberately absent without leaving an actual ergonomic hole.
