# `lang-spec/` — Tide formal specification

Machine-precise contracts a compiler implementer (or self-host
re-implementation) reads. Prose explanation of *why* and *how it
feels* lives in [`../docs/language-spec.md`](../docs/language-spec.md);
this directory is the **authoritative** view of *what* each rule is.
On disagreement these files win, per D17.

## Contents

| File | Purpose | Status |
|---|---|---|
| `keywords.md` | Reserved words, operators, punctuation, predeclared identifiers as a list | ✓ |
| `grammar.ebnf` | Lexical + syntactic grammar in standard EBNF | ✓ |
| `test-contract.md` | Canonical fixture serialization (TOKENS / AST / TYPES / ERRORS / GO / STDOUT) | ✓ TOKENS; deeper sections forthcoming |
| `ast.md` | Canonical AST node schema (fields, invariants, source spans) | ✓ |
| `name-resolution.md` | Scoping, implicit receiver, shadow rules | ✓ |
| `type-system.md` | Inference rules in sequent notation | ✓ |
| `builtins.md` | Predeclared identifiers with full signatures | ✓ |
| `desugaring.md` | Tide AST → simpler IR (match arms, scope+spawn, try) | ✓ |
| `lowering-go.md` | IR → Go encoding, runtime helpers, `//line` placement | ✓ |
| `diagnostics.md` | Numbered error-code catalog (`E0103 Unknown name`, …) | ✓ |
| `acceptance.yml` | Per-example feature manifest (label → covered constructs) | ✓ |

## Authority and coverage

- The formal docs in this directory are authoritative; prose in
  `../docs/language-spec.md` is a mirror.
- Every formal artifact (keyword, grammar production, operator,
  built-in, AST node, type rule, diagnostic code, lowering rule)
  MUST be exercised by ≥ 1 atomic fixture in
  `../tests/{lexer,grammar,sema,codegen}/`.
- A bootstrap exemption released the formalization series
  itself from the coverage rule; that exemption is now
  **closed**. From this point onward, every new formal-doc
  edit requires paired fixtures in
  `../tests/{lexer,grammar,sema,codegen}/`.

## How to consume

For a compiler implementer, the natural reading order is:

1. **`keywords.md`** — what's reserved.
2. **`grammar.ebnf`** — how text becomes tokens, how tokens become a
   parse tree.
3. **`ast.md`** — the data the parser hands to the rest of the
   pipeline.
4. **`name-resolution.md`** — what every identifier means.
5. **`type-system.md`** — what every expression's type is.
6. **`builtins.md`** — the predeclared scope's contents.
7. **`desugaring.md`** — how complex AST shapes simplify before
   codegen.
8. **`lowering-go.md`** — how simplified IR becomes Go.
9. **`diagnostics.md`** — every failure mode the implementation must
   catch.
10. **`test-contract.md`** — how to write fixtures that any
    implementation must pass.
11. **`acceptance.yml`** — which examples cover which features.
