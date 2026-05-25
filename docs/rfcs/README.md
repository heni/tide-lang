# Tide RFCs

Forward-looking governance for changes to the Tide language and
compiler. RFCs (Request For Comments) are how non-trivial
changes to the v1 surface, the compiler pipeline, or the
language tooling enter the project.

This directory holds:

- **`0000-process.md`** — the RFC process itself. How RFCs are
  proposed, reviewed, accepted, and superseded.
- **`0001-v01-baseline.md`** — the v0.1 language-surface
  baseline (pre-alpha). Every later RFC extends or amends this
  baseline.
- **`NNNN-<kebab-name>.md`** — individual proposals.

## Index

| Number | Status | Title |
|---|---|---|
| 0000 | accepted | RFC process |
| 0001 | accepted | v0.1 baseline |

## How to write an RFC

See [`0000-process.md`](0000-process.md). The short version: copy
the template from §"Document structure", fill it in, open a PR
with the file at `docs/rfcs/<next-number>-<kebab-name>.md`. The
PR follows the same review-subagent discipline as any other
change.
