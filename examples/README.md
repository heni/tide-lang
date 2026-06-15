# Examples — the v1 acceptance suite

These programs **define what v1 must be able to express**. Each is chosen to
*force* specific language features; an example compiling and running is the
definition of done for the features it exercises. The governing contract —
purpose, metadata, acceptance criteria, the negative-case mechanism — is
[RFC-0004](../docs/rfcs/0004-corpus-charter.md); this README is its mirror.

Examples are written **feature-first**: the program comes before the
implementation that makes it compile.

## Layout

The corpus is organised **by purpose**, not by where a problem came from
(the problem's origin lives in each example's `source` metadata). Every
example is a **directory** holding its program (one or more `.td` files) and
an `example.toml` manifest:

```
examples/<category>/<name>/
  example.toml      # description, source, category, showcase, forces, status, expects…
  <name>.td         # the program (the manifest `entry` field names it)
  expected.out      # expected stdout (when expects = output-check)
  errors/           # optional: negative cases (expected-diagnostic patches)
```

`★` marks **showcase** examples — those that demonstrate Tide's value over
plain Go (sum types, exhaustive matching, ergonomic errors, uncolored
concurrency, interface conformance). `showcase` is an orthogonal flag, not a
category.

## `core-language/` — type system, control flow, data structures

| Example | Forces |
|---|---|
| `two_sum` | slices, hash maps, iteration |
| `valid_parentheses` | a generic stack, strings/runes, `match` on characters |
| `invert_binary_tree` ★ | generic **sum type**, recursion, exhaustive `match` |
| `reverse_linked_list` | references, mutation, `Option`-typed links |
| `merge_intervals` | structs, sorting with a comparator (function values) |
| `fizzbuzz` | toolchain smoke: ranges, `if`/`else`, arithmetic, output |
| `lru_cache` | a generic `class` with methods; map plus ordering |
| `deep_destructure` | deep record / tuple destructuring patterns |
| `set_algebra` | set operations over collections |
| `match_on_tuples` | `match` on tuple patterns |
| `interfaces` | interface conformance |
| `defer_demo` | `defer` semantics |
| `d01`…`d11` | Advent of Code 2025 — algorithmic control flow + data structures |
| `p1033`…`p1820` | Timus problems — algorithmic |
| `hello` | toolchain smoke test |

The binary-tree problem is the headline: a recursive generic discriminated
union with pattern matching is exactly where Tide's type system pays off over
Go, which has no sum types.

## `modeling-errors/` — failure as values

| Example | Forces |
|---|---|
| `rpn_calculator` ★ | `Result` / `try` / `match`; sum-typed tokens; errors as values |
| `vending_machine` ★ | a **state machine** — exhaustive `match` over a sum-typed state |
| `error_chain` | error chaining with `Result` |
| `errors_as_types` | errors modelled as sum-typed values |

## `stdlib-binding/` — typed bindings over the Go standard library

| Example | Forces |
|---|---|
| `config_loader` | typed structs, the `encoding/json` binding, error handling |
| `wc` | a CLI: `os`/args, `io`, file reading, exit codes |
| `healthcheck_server` ★ | `net/http`; **Go interface conformance** (a Tide type as `http.Handler`) |
| `todo_api` | JSON REST CRUD; DTO structs; `Result` mapped to HTTP status codes |
| `counterstack` | sum-typed wire protocol, TCP + JSON Lines via `net` + `bufio`, an `interface Strategy` |

## `concurrency/` — uncolored, structured concurrency

| Example | Forces |
|---|---|
| `pipeline` | directional channel types, range-to-close, three-stage producer/transform/consumer |
| `worker_pool` | fan-out / fan-in, bounded parallelism, scope-joined workers |
| `pubsub` | per-subscriber channels under a mutex, drop-on-overflow via `select` + `default` |
| `rate_limited` | `time.tick`, `time.after`, non-blocking `select` arms |
| `nested_scopes` ★ | nested structured concurrency, cancellation propagation via `context` |
| `select_showcase` | every `select` case form (receive-bind, drop, send, timeout) |
| `parallel_fetcher` ★ | **structured concurrency**, channels, `context` cancellation, timeouts |
| `graceful_server` ★ | `net/http` + `os/signal` + `context` + `select`: graceful shutdown |

## Adding an example

An example earns its place if it **forces** a construct or interaction not yet
covered, or is a **showcase** with clearer value-over-Go than what exists — and
is realistic, atomic in intent, and compiles + runs. Pure duplication of
existing coverage, kitchen-sink programs, and "more tests for already-covered
constructs" (those are atomic fixtures, not corpus examples) are rejected. See
[RFC-0004](../docs/rfcs/0004-corpus-charter.md) for the full criteria and the
`example.toml` schema.

> The per-example `forces` lists above are being formalised into each
> `example.toml` as stable `lang-spec/` artefact IDs; the manifest `forces`
> field is the machine-readable source once backfilled.

## Negative cases

Beyond "compiles and runs", an example may carry **negative cases** under
`errors/`: a marker-anchored unified-diff patch that injects a specific mistake
into the program, plus the expected diagnostic (stable code, stage, message),
registered as an `[[error]]` entry in `example.toml`. These validate that
mistakes yield legible Tide diagnostics. See RFC-0004 for the mechanism.

## Conformance status

`STATUS.md` (generated from `auto-status.json` by `scripts/corpus_status.py`)
tracks how far each example gets through the pipeline. The metric we grow is
`build_ok` — the number of examples that compile end-to-end; it never
regresses.
