# Project manifest — `tide.toml`

The optional project file that defines a multi-package Tide project's
root and name (RFC-0002). A single-file program or a single directory of
`.td` files needs **no manifest** — it is one package, resolved against
the stdlib binding registry only. A manifest is required only to span
**more than one user package**.

This is the **authoritative** schema and resolution contract; the prose
mirror is `../docs/language-spec.md`. On disagreement this file wins (D17).

## Schema

`tide.toml` is the **only** v0.x configuration surface — no build flags,
no external-dependency version pinning (pre-alpha ships no package
manager). It has exactly three tables:

```toml
[project]
name = "myproj"          # required — the import-path root prefix for
                         # this project's own packages.

[toolchain]
go = "1.22"              # optional — the pinned Go toolchain (matches the
                         # emitted go.mod; lowering-go.md §Output).

[bindings]
extra = []               # optional — extra Go import paths exposed as
                         # bare-ident bindings (the local name is the
                         # last path segment). Two entries sharing a last
                         # segment collide and are rejected at start.
```

**Parser.** The reader is a deliberately tiny, closed-schema parser: the
compiler core stays dependency-free (no third-party TOML library — D19),
so only the line shapes above are accepted — `[section]` headers, `key =
"string"`, `key = ["a", "b"]` single-line arrays, `#` comments, and blank
lines. Anything else (an unknown section/key, a missing `[project]
name`, a malformed value) is a manifest error reported at compiler start.

## Resolution

A package is a directory of `.td` files (`name-resolution.md` §Scopes —
package scope). When the build target's source tree contains a
`tide.toml` (found by walking up from the entry file/directory to the
filesystem root), each `import P` resolves as:

1. **Local user package.** If `P` equals the manifest `name` or begins
   with `name/`, strip the `name` segment and look up the remainder as a
   directory **relative to the manifest's directory**. Bare `import
   myproj` resolves to the manifest root directory itself. A missing
   directory is **E0117 Unknown import path** (it does *not* fall through
   to stdlib). The package's `.td` files join the build; its top-level
   names bind under the import's last segment (`import myproj/utils` →
   `utils.…`; the qualified-reference surface and cross-package
   visibility are specified in `name-resolution.md` §Cross-package
   imports).
2. **Stdlib / extra binding.** Otherwise, if the path's head is a known
   stdlib namespace or matches a `[bindings] extra` entry's last segment,
   it resolves through the binding registry (no package directory to
   gather).
3. **Failure.** Neither local nor a known binding namespace → **E0117
   Unknown import path**.

Without a `tide.toml`, step 1 is skipped entirely: the program is a
single package resolved against the stdlib registry — the zero-config
path for scripts and quick experiments.

**Acyclic graph (D20).** The user-package import graph must be acyclic;
a cycle (`a` imports `b` imports `a`) is **E0116 Cyclic package import**,
rejected before sema runs. Shared code is extracted into a third package.

**Edge case — `name` collides with a stdlib package** (e.g. `name =
"fmt"`): the local lookup wins for paths under that name; the manifest is
authoritative for the local project. Choosing a non-colliding name is
recommended.
