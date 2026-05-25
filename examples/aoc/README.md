# `aoc/` — Advent of Code, paper-validated in Tide

Real puzzle solutions from [Advent of Code](https://adventofcode.com/),
hand-ported to Tide as a second pass of paper validation. The v1
acceptance suite forces the *core* of the language; AoC drives
breadth — input parsing, integer arithmetic, slicing, ad-hoc data
structures — across dozens of small programs. Where AoC surfaces a
gap, it lands as a new G-entry in the spec audit log.

## 2025 (Python originals)

| Day | Puzzle | Forces (notable) |
|---|---|---|
| d01 | rotation/modulo over a 100-position dial | string slicing `s[a:b]` (G45), `strconv.atoi`, modular arithmetic, `continue` (G46) |
| d02 | bad-ID predicates over comma-separated ranges | `(string) => bool` first-class function value, `type Range = (int, int)` tuple alias, `sort.sorted` over `[]string`, `continue` (G46) |
| d03 | monotonic-stack DP for largest L-digit subsequence | `for (idx, d) in line` (G25), slice slicing `xs[a:b]`, `&&` short-circuit (G47), `continue` (G46) |
| d04 | iterative 2-D maze cleanup over 8-neighbourhood | `[][]byte` 2-D grid with mutation `grid[r][c] = byte('.')`, nested `r,c` iteration with negative-range deltas `-1..=1`, byte-literal comparison via `byte('@')` (G49) |
| d05 | merge sorted intervals, two-section parser | tuple `(int, int)` (G24), `t.0`/`t.1` field access, sort by comparator, `&&` short-circuit (G47), `continue` (G46) |
| d07 | 1-D wavefront with splitters and timelines | `Set<int>` (G48), slice index-assignment `s[i] = v` (G45 extension), rune iteration `for (i, c) in str`, `.copy()` on a slice |
| d08 | union-find over star pairs sorted by 3-D distance | `class DSU` with array-backed parent/size tables, `math.sqrt`, primitive numeric conversion `float64(int)` (G49), sort by tuple field `(a, b) => a.2 < b.2`, recursive method with path-compression write via the implicit-receiver rule (`id[a] = find(id[a])`) |
| d09 | maximum-area axis-aligned bounding rectangles | nested-tuple type `type Rect = (Corner, Corner)`, chained tuple field access `r.1.0`, axis-aligned segment / rectangle interior intersection, the `for/else` idiom written explicitly as a "no-break-seen" flag |
| d11 | path-count DFS with memoisation | `Map<string, []string>` adjacency, `Map<string, int>` cache, `Set<string>` (G48) for cycle detection, recursive `Result`-returning DFS with `try` |

## Pending

Days deferred or out of scope:

- **d10** — `numpy`, `pulp` (LP solver), `tqdm`; out of scope.
- **d12** — needs `regexp` binding.

## 2024 (Rust originals)

Each 2024 day depends on a project-internal `macro-utils` crate for
input parsing. Porting that helper layer to Tide is a separate batch;
it will land once the 2025 set is complete and reveals the right
Tide-side parsing-helper shape.
