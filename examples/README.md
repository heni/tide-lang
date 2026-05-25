# Examples — the v1 acceptance suite

These programs **define what v1 must be able to express** (D12 — see
`../docs/design-decisions.md`). Each is chosen to *force* specific language
features. An example compiling and running is the definition of done for the
features it exercises.

Examples are written feature-first: the program comes before the
implementation that makes it compile. Before any compiler code exists, every
example is hand-written as a complete `.td` program against the spec — a
paper validation of `../docs/language-spec.md`. Files present today are
early **target sketches**; their syntax is illustrative and will be
completed and tightened by that exercise.

`★` marks **showcase** examples — the ones that demonstrate Tide's value over
plain Go (sum types, exhaustive matching, ergonomic errors, uncolored
concurrency, interface conformance).

## `leetcode/` — core language

Algorithmic problems that exercise the type system, control flow, generics,
recursion, and data structures with little or no standard library. These are
the Phase 1–2 acceptance tests for the language core.

| Example | Forces | Phase |
|---|---|---|
| `two_sum` | slices, hash maps, iteration | 2 |
| `valid_parentheses` | a generic stack, strings/runes, `match` on characters | 2 |
| `invert_binary_tree` ★ | generic **sum type**, recursion, exhaustive `match` | 2 |
| `reverse_linked_list` | references, mutation, `Option`-typed links | 2 |
| `merge_intervals` | structs, sorting with a comparator (function values) | 2 |

The binary-tree problem is the headline: a recursive generic discriminated
union with pattern matching is exactly where Tide's type system pays off over
Go, which has no sum types.

## `interview/` — modeling and error handling

Slightly more "real" than LeetCode — problems about structuring code, modeling
domains, and handling failure.

| Example | Forces | Phase |
|---|---|---|
| `fizzbuzz` | toolchain smoke: ranges, `if`/`else`, arithmetic, output | 1 |
| `rpn_calculator` ★ | `Result` / `try` / `match`; sum-typed tokens; errors as values | 2 |
| `vending_machine` ★ | a **state machine** — exhaustive `match` over a sum-typed state | 2 |
| `lru_cache` | a generic `class` with methods; map plus ordering | 3 |
| `config_loader` | typed structs, the `encoding/json` binding, error handling | 3 |

The RPN calculator and the state machine prove that `Result`-based errors and
exhaustive matching are ergonomic in practice, not just on paper.

## `services/` — the runtime pitch

Backend programs that exercise the standard-library bindings and the
concurrency model. This is where the actual product claim — "the Go runtime" —
is demonstrated.

| Example | Forces | Phase |
|---|---|---|
| `wc` | a CLI: `os`/args, `io`, file reading, exit codes | 3 |
| `healthcheck_server` ★ | `net/http`; **Go interface conformance** (a Tide type as `http.Handler`) | 3–4 |
| `todo_api` | JSON REST CRUD; DTO structs; `Result` mapped to HTTP status codes | 4 |
| `parallel_fetcher` ★ | **structured concurrency**, channels, `context` cancellation, timeouts | 5 |
| `graceful_server` ★ | `net/http` + `os/signal` + `context` + `select`: graceful shutdown | 5 |

`healthcheck_server` is the critical binding test: a Tide type satisfying Go's
`http.Handler` interface, with no hand-written glue. `parallel_fetcher` proves
the uncolored concurrency model (D7) end to end — the place the Go runtime
actually beats a single-threaded event loop.

## `concurrency/` — canonical Go-runtime patterns

The strongest part of Tide's runtime is the part the syntax doesn't make
obvious: goroutines, channels, `select`, structured-concurrency scopes. The
`services/` examples touch concurrency in two places; this folder makes
the runtime case directly with one program per canonical pattern.

| Example | Forces | Phase |
|---|---|---|
| `pipeline` | directional channel types, range-to-close, three-stage producer/transform/consumer | 5 |
| `worker_pool` | fan-out / fan-in, bounded parallelism, scope-joined workers | 5 |
| `pubsub` | per-subscriber channels under a mutex, drop-on-overflow via `select` + `default` | 5 |
| `rate_limited` | `time.tick`, `time.after`, non-blocking `select` arms | 5 |
| `nested_scopes` ★ | nested structured concurrency, cancellation propagation via `context` | 5 |
| `select_showcase` | every `select` case form (receive-bind, drop, send, timeout) | 5 |

See [`concurrency/README.md`](concurrency/README.md) for the per-example
write-up.

## `aoc/` — broader paper validation via Advent of Code

The v1 acceptance suite above forces the *core* of the language. AoC
ports drive breadth — input parsing, integer arithmetic, slicing, ad-hoc
data structures — across many small programs. AoC examples are not part
of the v1 ship gate, but every construct they use must still be in
[`../docs/language-spec.md`](../docs/language-spec.md), so they keep
honest pressure on the spec. See [`aoc/README.md`](aoc/README.md) for the
per-day breakdown.

## `timus/` — selected Timus problems

A second breadth pass: classical competitive-programming problems from
the [Timus archive](http://acm.timus.ru/), chosen to exercise territory
AoC misses — stdin scan-style input, `uint64` / `int64` arithmetic,
byte-level string manipulation, floating-point math via `math.log10`.
Files are self-contained: stdin → compute → stdout. See
[`timus/README.md`](timus/README.md).

## `agents/` — real-project architectural sketches

Single-file Tide ports of real-project architectures, chosen to test
how the language scales to a non-toy shape. Each subfolder targets
one Python or Go project the user has built and distills its load-
bearing patterns into a single `.td` file plus a `README.md` mapping
those patterns back to the source project.

| Project | Forces |
|---|---|
| [`agents/counterstack`](agents/counterstack/README.md) — Pentix arena agent | sum-typed wire protocol, TCP + JSON Lines transport via the new `net` + `bufio` bindings, structured-concurrent reader/writer/decision-loop, `interface Strategy` |

## `borgo/` — Tide ports of Borgo snapshot tests

Apples-to-apples comparison with Tide's closest existing competitor.
[Borgo](https://github.com/borgo-lang/borgo) is a small statically
typed language that compiles to Go (Rust-flavoured syntax, ML-family
types). Five of its snapshot programs ported to Tide, with
side-by-side notes. See [`borgo/README.md`](borgo/README.md).

## How to use this suite

- Implement features against the next example in phase order.
- When an example compiles and runs, record it as a checkbox in this list.
- v1 ships when every example above compiles, runs, and produces correct
  output — and the `★` showcases need **no** manual Go shims.
