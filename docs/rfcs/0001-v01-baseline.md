# RFC-0001 — v0.1 baseline

| Field | Value |
|---|---|
| Number | 0001 |
| Status | accepted |
| Created | 2026-05-25 |
| Supersedes | — |
| Target | `lang-spec/` (entire directory, snapshotted by tag) |

## Summary

The Tide language and compiler enter pre-alpha at version
**v0.1**. The v0.1 surface is exactly the contents of
`lang-spec/` at the git tag
[`v0.1-baseline`](https://github.com/heni/tide-lang/releases/tag/v0.1-baseline)
(commit `fbc530e`). Every later RFC extends or amends this
baseline.

## Motivation

The Formalization-A through Formalization-L series merged eleven
formal artefacts into `lang-spec/` (twelve counting the index
README) plus the corpus in `examples/`. Without a stable
reference point, future RFCs that say "extends the type system"
or "adds a method to Map" cannot pin **which** type system or
**which** Map. The baseline tag is that reference point.

The version label is **v0.1**, not v1: pre-alpha, no compiler
yet, no production users. The eventual stable target is v1; the
intermediate steps (v0.2, v0.3, …) will accumulate the RFCs.

## Design — what v0.1 is

The v0.1 baseline is defined by the contents of `lang-spec/` at
tag `v0.1-baseline`:

| File | Role |
|---|---|
| `keywords.md` | Reserved words, operators, punctuation, predeclared identifiers |
| `grammar.ebnf` | Lexical + syntactic grammar |
| `ast.md` | Canonical AST schema |
| `name-resolution.md` | Scopes, implicit receiver, shadow rules |
| `type-system.md` | Sequent-style inference rules + exhaustiveness |
| `builtins.md` | Predeclared identifier catalog with full signatures |
| `desugaring.md` | Tide AST → Tide IR rewrite stages |
| `lowering-go.md` | Tide IR → Go encoding |
| `diagnostics.md` | Numbered error / warning catalog |
| `test-contract.md` | Canonical fixture serialization |
| `acceptance.yml` | Per-example feature manifest |

The corpus at `examples/` (51 `.td` files at the baseline tag)
demonstrates v0.1 in practice; every feature listed in
`acceptance.yml` is exercised by at least one example.

## Design — what v0.1 is NOT

The following are explicitly **not** part of v0.1; each will be
its own RFC when work begins:

- A working compiler binary. `cmd/tide` is currently a stub;
  the lexer / parser / sema / desugar / codegen pipeline is the
  next milestone.
- Atomic-fixture coverage in `tests/{lexer,grammar,sema,codegen}/`.
  The bootstrap exemption that released the formalization series
  from this requirement is now closed — every spec-touching PR
  from this point forward must satisfy the rule.
- Constructs that the formalization explicitly parks (D11
  bounded generics; typed errors on `scope<T, E>` with E ≠ error;
  user-extensible `Iterable<T>`).

## Alternatives considered

- **No baseline RFC at all** — every later RFC says "extends
  the current state". Rejected: "current" drifts as soon as
  the first amending RFC merges; cross-referencing becomes
  ambiguous.
- **Re-document v0.1 in this RFC** — i.e., put the whole spec
  inline. Rejected: guaranteed to drift from `lang-spec/`;
  defeats the single-source-of-truth discipline.
- **Pin to a commit hash rather than a tag** — works
  mechanically but is less readable. A tag is an annotated,
  human-meaningful name; the hash is in the tag's metadata.

## Paired edits

None — this RFC is purely declarative. No `lang-spec/` files
change as part of its acceptance.

The git side has already happened: the
[`v0.1-baseline`](https://github.com/heni/tide-lang/releases/tag/v0.1-baseline)
tag points at commit `fbc530e` on `main`.

## Transition / compatibility

Not applicable — this is the starting state. There is nothing
to migrate from.

## Open questions

None. Subsequent versions (v0.2, v0.3, …) accumulate as RFCs
land and the implementation catches up.
