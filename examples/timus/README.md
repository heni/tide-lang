# `timus/` — selected Timus problems, paper-validated in Tide

Hand-ported solutions from the [Timus archive](http://acm.timus.ru/),
chosen to span territory the v1 acceptance suite and AoC don't cover —
stdin scan-style input parsing, big-integer arithmetic on `uint64` /
`int64`, byte-level string manipulation, floating-point arithmetic on
`math.log10` and `math.floor`.

Each file is self-contained: read input from stdin, compute, print.
A failing read prints to stderr and exits 1.

## Batch 1 — input parsing and small arithmetic

| Problem | Link | Forces |
|---|---|---|
| 1335 — Sense of Beauty | [p1335.td](p1335.td) | `fmt.scan<uint64>()` (the bound form of Go's `fmt.Scan(&v)`), uint64 arithmetic, multi-arg `fmt.println` for space-separated output |
| 1349 — Pythagoreans' Trousers | [p1349.td](p1349.td) | `if`/`else if`/`else` chain at statement position |
| 1820 — Ural Steaks | [p1820.td](p1820.td) | `fmt.scan2<int64, int64>()` two-value scan returning a tuple, int64 arithmetic, ceiling-divide `(n*2 + k - 1) / k`, `int64(n)` literal conversion (G49) |
| 1683 — Cutting a Polyglot | [p1683.td](p1683.td) | slice accumulator with `push`, sequential greedy cut, trailing-space output with `fmt.print` |
| 1605 — Mirror Power-of-Two | [p1605.td](p1605.td) | `math.log10` (new in the math binding row), `int(math.floor(...))` truncation, `float64(int)` widening, mixed-type arithmetic |
| 1404 — Easy to Hack! | [p1404.td](p1404.td) | `s.bytes()`, byte arithmetic with `byte(21)` / `byte(26)` constants (G49), `strings.fromBytes` round-trip, mutable `[]byte` accumulator |

## Batch 2 — algorithmic depth

Picked by category-coverage rather than line count. Each problem hits
spec territory the first batch (and the AoC port) sidestepped:

| Problem | Link | Forces |
|---|---|---|
| 1033 — Labyrinth | [p1033.td](p1033.td) | classic BFS with slice-as-queue (`q.push(v)` to enqueue, `q[0]` + `q[1:]` to dequeue), `Map<Coord, bool>` keyed by a tuple-record (structural equality from §Expressions), sum-typed `Direction` with exhaustive `match` |
| 1242 — The Werewolves | [p1242.td](p1242.td) | `Map<int, []int>` adjacency lists, recursive DFS over an explicit graph argument, parallel `[]bool` visited flags, BLOOD-terminator parsing pattern |
| 1423 — String Tale | [p1423.td](p1423.td) | KMP prefix function + circular-search; `makeSlice<int>(n)` table mutated by index-write through a function parameter (G45 amended); byte-indexed string access |
| 1786 — Sandro's Book | [p1786.td](p1786.td) | 2-D DP table allocated as `makeSlice<[]int>(n)` + per-row `makeSlice<int>(m)`, chained `dp[i][j] = ...` writes, rune-level case/class checks, mixed `rune ↔ int` numeric conversions |
| 1133 — Fibonacci Sequence | [p1133.td](p1133.td) | `math/big` binding (new row): `big.BigInt` with a functional surface (`a.add(b)` returning a fresh BigInt) instead of Go's pointer-mutation `a.Add(x, y)`; Cramer's-rule 2×2 linear solve over BigInts |

## Pipeline note

Timus port lands **after AoC** and **before counterstack-champion**.
The 2024 Rust-with-macros AoC port is parked — `macro-utils` is a
crate-internal parsing helper layer, and the same parsing patterns
fall out of Tide more naturally inline (as Timus and AoC-2025 already
demonstrate).
