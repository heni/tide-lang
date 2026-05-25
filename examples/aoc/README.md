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
| d05 | merge sorted intervals, two-section parser | tuple `(int, int)` (G24), `t.0`/`t.1` field access, sort by comparator, `&&` short-circuit (G47), `continue` (G46) |
| d07 | 1-D wavefront with splitters and timelines | `Set<int>` (G48), slice index-assignment `s[i] = v` (G45 extension), byte-indexed string access `s[i]` |
| d11 | path-count DFS with memoisation | `Map<string, []string>` adjacency, `Map<string, int>` cache, `Set<string>` (G48) for cycle detection, recursive `Result`-returning DFS with `try` |

## Pending

Days deferred to later batches:

- **d04, d09** — `itertools`-flavoured combinatorics; expressible in Tide
  but want a binding sketch first.
- **d08** — DSU + `math.sqrt`; expressible after the `math` binding lands.
- **d12** — needs `regexp` binding.
- **d10** — `numpy`/`pulp`/`tqdm`; out of scope.

## 2024 (Rust originals)

Each 2024 day depends on a project-internal `macro-utils` crate for
input parsing. Porting that helper layer to Tide is a separate batch;
it will land once the 2025 set is complete and reveals the right
Tide-side parsing-helper shape.
