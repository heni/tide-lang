# `timus/` — selected Timus problems, paper-validated in Tide

Hand-ported solutions from the [Timus archive](http://acm.timus.ru/),
chosen to span territory the v1 acceptance suite and AoC don't cover —
stdin scan-style input parsing, big-integer arithmetic on `uint64` /
`int64`, byte-level string manipulation, floating-point arithmetic on
`math.log10` and `math.floor`.

Each file is self-contained: read input from stdin, compute, print.
A failing read prints to stderr and exits 1.

## Batch 1

| Problem | Link | Forces |
|---|---|---|
| 1335 — Sense of Beauty | [p1335.td](p1335.td) | `fmt.scan<uint64>()` (the bound form of Go's `fmt.Scan(&v)`), uint64 arithmetic, multi-arg `fmt.println` for space-separated output |
| 1349 — Pythagoreans' Trousers | [p1349.td](p1349.td) | `if`/`else if`/`else` chain at statement position |
| 1820 — Ural Steaks | [p1820.td](p1820.td) | `fmt.scan2<int64, int64>()` two-value scan returning a tuple, int64 arithmetic, ceiling-divide `(n*2 + k - 1) / k`, `int64(n)` literal conversion (G49) |
| 1683 — Cutting a Polyglot | [p1683.td](p1683.td) | slice accumulator with `push`, sequential greedy cut, trailing-space output with `fmt.print` |
| 1605 — Mirror Power-of-Two | [p1605.td](p1605.td) | `math.log10` (new in the math binding row), `int(math.floor(...))` truncation, `float64(int)` widening, mixed-type arithmetic |
| 1404 — Easy to Hack! | [p1404.td](p1404.td) | `s.bytes()`, byte arithmetic with `byte(21)` / `byte(26)` constants (G49), `strings.fromBytes` round-trip, mutable `[]byte` accumulator |

## Pipeline note

Timus port lands **after AoC** and **before counterstack-champion**.
The 2024 Rust-with-macros AoC port is parked — `macro-utils` is a
crate-internal parsing helper layer, and the same parsing patterns
fall out of Tide more naturally inline (as Timus and AoC-2025 already
demonstrate).
