# RFC-0002 — Multi-file Tide programs

| Field | Value |
|---|---|
| Number | 0002 |
| Status | draft |
| Created | 2026-05-26 |
| Supersedes | — |
| Target | `lang-spec/grammar.ebnf` (import production), `lang-spec/name-resolution.md` (cross-package visibility), `lang-spec/lowering-go.md` (multi-package output tree), new `lang-spec/manifest.md` (project file), `internal/parser` + `internal/codegen` + `cmd/tide` (resolver and bundler) |

## Summary

Define how a Tide program is built from more than one `.td` source.
A package is a directory of `.td` files (Go-style); cross-package
imports use the same `import <path>` form the language already
has. A small optional manifest file `tide.toml` defines project
root and module name for projects that span more than one
package. Single-file programs (the current PR-D state) work
unchanged.

## Motivation

The compiler today reads one `.td` file, emits one `main.go`, and
runs the Go toolchain on the temporary directory. There is no
notion of a package, no cross-file imports, no way to factor
out code. Every example in `examples/` is single-file by
construction. Even the simplest realistic program (say, a CLI
with a parser layer separated from the business logic) cannot
be expressed.

The corpus also reveals that user-extensible package layout
will be needed before Phase 2's larger examples (e.g.,
`agents/counterstack/pentix_agent.td` already lives in a deep
subdirectory; once it grows past one file we have nothing).

The point of the RFC is to pick a model that:

1. Keeps simple programs simple (no manifest required for a
   single .td file or a small directory).
2. Aligns with the runtime (Go) without forcing TypeScript-style
   relative-path import churn back onto users — TS developers
   chose Tide partly to leave that behind.
3. Stays inside the existing grammar where possible — the
   current `import Ident ("/" Ident)*` production already
   handles slash-paths.

## Design

### Package = directory

A **package** is a non-empty set of `.td` files in a single
directory. All top-level declarations in those files share one
scope; no `import` is needed to reference a sibling file's
top-level name. Cross-file visibility is the natural extension
of the existing file-scope rule in `name-resolution.md`.

Build-unit selection mirrors Go:

- `tide build path/to/file.td` — compile a single-file program
  (current behaviour, unchanged).
- `tide build path/to/dir/` — compile every `.td` in that
  directory as one package, find the unique `func main()`,
  produce a binary.

If a directory has more than one `func main()` across its files,
or has none and is the build entry, it's an **E0114 No (or
multiple) main functions in package** error.

### Cross-package imports

The `import` form stays as it is today:

```
import fmt                  // stdlib (resolved against the binding registry)
import strings
import encoding/json        // stdlib, multi-segment
import myproj/utils         // user package — see "Resolution" below
import myproj/svc/store     // nested user package
```

No quoting, no relative `./` paths, no two syntactic kinds of
import. The grammar production
`Import = "import" Ident ("/" Ident)*` from
`grammar.ebnf:255-257` is unchanged.

After `import myproj/utils`, names from that package are
referenced as `utils.functionName` — the **last segment** is
the local identifier under which the package binds. (Same as
Go.)

### Resolution

When the resolver sees `import P`:

1. **Local lookup.** Walk up from the source file's directory
   to the nearest `tide.toml`. If found:
   - If `P` starts with the manifest's `name` segment (e.g.
     `import myproj/utils` with `name = "myproj"`), strip the
     prefix and look up the remaining path as a directory
     **relative to the manifest's directory**.
   - Otherwise, look up `P` directly as a directory relative
     to the manifest's directory.
2. **Stdlib lookup.** If local lookup didn't find a directory,
   check the binding registry (the same one PR-C's
   `mapFieldName` hack uses, but generalised). The v0.x
   registry hard-codes a list of supported Go stdlib packages
   (`fmt`, `os`, `strings`, `strconv`, `bufio`, `context`,
   `time`, `sync`, `encoding/json`, `net/http`, `io`, `log`,
   `net`, `math/big`). Extensions go through bindgen
   (forthcoming).
3. **Failure.** Neither local nor stdlib — emit
   **E0115 Unknown import path**.

If a project has no `tide.toml`, step 1 is skipped — the file
behaves as a single-package, stdlib-only program.

### Visibility — cross-package

Within a package, every top-level declaration is visible
everywhere in the package (intra-package rule from
`name-resolution.md` extends naturally to multiple files).

Across packages, **declaration name capitalisation decides
export**:

- `func Parse(...)` — exported, callable as `utils.Parse(...)`
  from importers.
- `func parseInternal(...)` — package-private; not visible
  to importers.

Same rule for type, class, and interface declarations. This is
mechanically simple, matches the Go runtime (no rewrite needed
at the codegen layer), and avoids inventing a `pub` keyword
that `keywords.md` explicitly forbids in v1.

Top-level `let` constants follow the same rule.

### Manifest — `tide.toml`

A minimal TOML file at the project root tells the compiler
where the project starts and what to call it. Without a
manifest, the compiler treats the source file's directory as
both the package and the project root (good enough for
single-file scripts and quick experiments).

```toml
[project]
name = "myproj"               # The module name used as the root
                              # prefix for cross-package imports.

[toolchain]
go = "1.22"                   # Pinned Go toolchain (matches the
                              # lowering-go.md output go.mod).

[bindings]
# Optional: extend the stdlib binding registry with extra Go
# packages exposed as bare-ident imports. Each entry is a Go
# import path; the local name in Tide is the last segment.
extra = []
```

This is the **only** v0.x configuration surface. No
build flags, no dependency resolution, no version pinning of
external packages — pre-alpha doesn't ship a package manager.

### Lowering — multi-package output

Per `lowering-go.md` §Output tree shape, the emitted Go tree
has been described as supporting `bindings/` and `tidert/`
sibling directories. Extending it to user packages is
straightforward:

```
<tmp>/
  main.go              # main package — entry-point .td's body
  go.mod               # module tide-output; go 1.22
  utils/utils.go       # one Go-pkg per Tide-pkg
  svc/store/store.go   # nested package, dotted to dirname
  bindings/...         # stdlib wrappers (forthcoming bindgen)
  tidert/...           # runtime helpers (forthcoming)
```

`go build ./...` walks the tree and links everything. No
extra work for the toolchain.

`//line` directives carry the **original repo-relative** path,
not the temp path, so panics still point at `examples/proj/svc/store/order.td`,
not at `/tmp/tide-build-X/svc/store/store.go`.

## Alternatives considered

- **Model B (TS-style file=module).** Each `.td` is its own
  namespace; cross-file uses `import { foo } from "./utils"`.
  Rejected: misaligns with Go runtime (would force one Go
  package per file with `init()` glue), brings back the
  relative-path churn that TypeScript developers were trying
  to leave behind.
- **Hybrid A+B.** Bare-ident imports → stdlib; quoted-path
  imports → user packages. Rejected: two syntactic forms for
  one concept; quoted paths still encode the relative-path
  problem in a slightly different shape.
- **Model C (Rust-style explicit `mod`).** `mod foo;` in
  source declares a sub-module. Rejected: redundant —
  filesystem already gives the answer. Adds a keyword the v1
  surface doesn't have.
- **No manifest, registry-only.** Single-file programs work
  without one (kept); but a stable project root is what tells
  the resolver "this `import myproj/utils` is local, not
  stdlib." Hardcoding "first segment of the import path matches
  the directory name" was considered as a manifest-less
  alternative; rejected as too fragile (the project name and
  the root directory often differ, e.g., a repo cloned under
  a different name).

## Paired edits

- `lang-spec/grammar.ebnf` — no change (the existing import
  production already handles slash-paths).
- `lang-spec/name-resolution.md` — new §Cross-package visibility
  documenting the capitalisation-exports rule and the
  `import P` → `lastSegment(P)` local binding rule.
- `lang-spec/lowering-go.md` — extend §Output tree shape with
  the multi-package example; cross-link to this RFC.
- `lang-spec/manifest.md` — new file specifying the
  `tide.toml` schema and the resolver algorithm.
- `lang-spec/diagnostics.md` — add E0113 (already-allocated
  parser-level code; recheck), **E0114 No / multiple `main`
  functions in package**, **E0115 Unknown import path**.
- `internal/parser` — no grammar change; resolver code lands
  with the multi-file build (see implementation plan).
- `internal/codegen` — emit one Go package per Tide package;
  retire the `mapFieldName` hack in favour of the registry.
- `cmd/tide` — accept directory inputs to `build` / `run`;
  resolver walks for `tide.toml`.

## Transition / compatibility

Strictly additive. Every PR-D corpus example continues to
compile and run unchanged (they are single-file with
stdlib-only imports). The new code paths activate only when
either the build entry is a directory or `tide.toml` is
found by the resolver walk.

## Open questions

- **Default registry contents.** The hardcoded stdlib list
  above mirrors what the corpus actually uses today. New
  additions to the registry should still go through a small
  RFC (or a one-line PR adjusting the table)? Lean toward
  "RFC-bypass for purely additive registry entries", but flag.
- **Cyclic imports between user packages.** Go forbids them at
  the toolchain level; we inherit the rejection for free. The
  spec should say so explicitly.
- **Test files.** Go uses `_test.go` to separate test code from
  package code. Mirror with `_test.td`? Defer until tests need
  to live in user packages — currently all tests are Go-side
  (`internal/*_test.go` and fixture runners).
- **Versioned dependencies.** Out of scope; pre-alpha has no
  package manager. The manifest's `[bindings] extra` slot is
  the only knob, and it expects already-vendored Go packages.

## Implementation plan (informative)

Not part of the RFC contract, but useful to size the work:

1. PR-E1: parser & codegen support multi-file packages (single
   directory). `tide build dir/` works, `func main` is
   located, codegen emits one Go file per Tide file inside one
   Go package.
2. PR-E2: `tide.toml` reader + resolver walk. `import P` with
   a project name prefix resolves locally.
3. PR-E3: registry extraction. `mapFieldName` retired in
   favour of a registry table read from a `lang-spec/`-
   sourced JSON / TOML; bindings/<pkg>.go emitted instead of
   the hardcoded `fmt.Println` shortcut.
4. PR-E4: cross-package visibility (capitalisation rule).
5. PR-E5: E0114 / E0115 diagnostics with fixtures.
