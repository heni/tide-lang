# corpus-status — the conformance scoreboard, in Tide

The corpus build/diagnostic scoreboard, written in Tide. Self-host/dogfood
milestone: the tool that measures the corpus is itself written in the
language, built and run by the compiler. (It began as a port of an interim
Python script, now retired.)

Lives outside the measured corpus globs (`examples/`, `user_tests/`) so the
tool never scores itself.

## Build & run

```
tide build -o /tmp/corpus-status tools/corpus-status
cd <repo-root> && /tmp/corpus-status
```

The tool `chdir`s to the git root, so it can be launched from anywhere inside
the repo. It regenerates `examples/auto-status.json` + `examples/STATUS.md`.

## Status (incremental port)

- **`build_ok`** — each example's furthest build-pipeline stage
  (parse / sema / emit / build), the JSON snapshot, and the `STATUS.md`
  render. **Done.**
- **`diag_ok`** — the negative-case (`[[error]]`) metric: patch-apply,
  build, and check the emitted diagnostic against the ideal. **Done** —
  with this, `collect` (the default write path) produces a snapshot
  byte-identical to the Python tool (`build_ok` + `diag_ok` + misses).
- **`--check`** (floor enforcement + snapshot/`STATUS.md` freshness) and
  **`--history`** (the trend, reconstructed from `git log` of the JSON
  snapshot as JSONL). **Done.**
- **`run_ok`** — the behavioural metric: actually *run* each `run-pass`
  example and check its exit code (and stdout against an `expected_output`
  sidecar when present). Examples declare their invocation in `example.toml`
  (`args` / `stdin` / `expected_output` / `expected_exit`, all relative to
  the example's own directory); `no-run` examples are excluded. A per-example
  timeout guards against a non-terminating example. **In progress.**

### `--no-run` and the run cache

`--no-run` skips the (slow) run pass entirely — the fast `build_ok`/`diag_ok`
inspection. The run pass is backed by a cache (`examples/run-cache.json`)
keyed on **sha256 of each example's emitted Go** plus its invocation: a
compiler change that alters an example's codegen re-runs it (regressions are
caught), while unchanged Go is skipped. The cache is committed and portable
(emit is deterministic across machines); **expect it to churn in any PR that
changes codegen** for run-pass examples — that is the cost of a CI-shared,
correctness-preserving cache.

This tool is the authoritative gate: CI builds it with `tide` and runs
`--check` on every PR and push to `main`.

## Layout

- `bindings.td` — curated FFI (`extern`) surface: the vendored `corpusexec`
  subprocess adapter (`std/vendor/corpusexec`), plus `os`/`path/filepath`/
  `regexp`/`strings` bindings the built-in tables don't cover.
- `main.td` — the logic and entry point.

### Why a vendored subprocess adapter

Tide's `(T, error) → Result<T, error>` boundary lift discards the value when
the error is non-nil, but a subprocess's combined output is meaningful
precisely when the process *fails* (a build diagnostic). The thin vendored
Go module `std/vendor/corpusexec` reshapes the boundary into an opaque handle
carrying both output and exit code, each reachable through a value-returning
method the FFI binds cleanly.
