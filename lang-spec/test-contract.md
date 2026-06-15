# Test fixture contract

Every compiler stage has a **canonical text serialization** for its
output. Tests are data files in `../tests/<stage>/`, not Go code:
the runner reads the input, runs the stage, serializes the output,
and diffs against an expected text. When the compiler is re-
implemented in Tide (self-host), the runner is rewritten on Tide
but the fixtures and expected text stay byte-identical.

This file is the contract for those serializations. Implementations
that produce different output for the same input are wrong, not
the fixtures.

## File format — manifest sections

Each fixture is one file with `--- SECTION ---` delimiters. A
section is optional unless the stage being tested needs it.
Whitespace inside a section is significant. A trailing blank line
inside a section is dropped by the reader. Lines starting with
`#` at column 1 inside the file but outside any section are
comments and may be used as titles.

```
--- TITLE ---
short one-liner describing the case

--- INPUT ---
<.td source, verbatim>

--- TOKENS ---
<canonical TOKENS form, see below>

--- AST ---
<canonical AST S-expression>

--- TYPES ---
<canonical TYPES table>

--- ERRORS ---
<canonical ERRORS list>

--- GO ---
<emitted Go after gofmt -s>

--- STDOUT ---
<runtime stdout, byte-identical>

--- EXIT ---
<integer exit code>
```

A test runner runs the deepest section present and verifies every
present section in order. The `INPUT` section is always required.

## TOKENS

One token per line. Format:

```
Kind<lexeme>  line:col
```

Where:

- `Kind` is the token kind name from `grammar.ebnf` (`Keyword`,
  `Ident`, `IntLit`, `FloatLit`, `StringLit`, `RuneLit`, `Op`,
  `Punct`, `Newline`, `EOF`).
- `<lexeme>` is the source text the token covers, wrapped in angle
  brackets. Empty for `Newline` and `EOF` (write `<>`).
- `line:col` is the 1-indexed character (not byte) position of the
  token's first character in the input file. UTF-8-aware: a multi-
  byte rune counts as one column. Matches the span format used by
  AST / TYPES / ERRORS sections — positions are uniform across the
  whole contract.

At least two spaces separate `<lexeme>` from `line:col`. The
canonical writer pads to a stable column for readability;
implementations may emit any width ≥ 2 — the runner normalises
whitespace before diffing. Tokens are listed in source order, one
per line. The list ends with an `EOF` token.

Example for `import fmt\n`:

```
Keyword<import>      1:1
Ident<fmt>           1:8
Newline<>            1:11
EOF<>                2:1
```

## AST

S-expression with the canonical node names from `ast.md`. Each
node carries a `@line:col-line:col` span attribute (char-counted
positions, matching the TOKENS format).

### Form

For each node:

```
(NodeKind [attrs ...] @startLine:startCol-endLine:endCol
  child1
  child2
  ...)
```

- `NodeKind` is the node's name verbatim from `ast.md` (e.g.
  `FuncDecl`, `IntLitExpr`, `Binary`).
- `attrs` are quoted string literals or int/bool constants in
  the order documented per node — typically a name, a literal
  value, or an operator lexeme. Strings are wrapped in `"…"`;
  escape sequences inside the value are written as `\n \t \r
  \\ \"`.
- Each child appears on its own line, indented by **two spaces**
  per nesting level relative to its parent's opening paren.
- The closing paren of a node trails the last child on the same
  line, with no extra space.

### Examples

A bare integer literal at line 1 col 5:

```
(IntLitExpr 42 @1:5-1:7)
```

A binary expression:

```
(Binary "+" @1:1-1:6
  (IntLitExpr 1 @1:1-1:2)
  (IntLitExpr 2 @1:5-1:6))
```

A function declaration with an empty parameter list:

```
(FuncDecl "main" @3:1-5:2
  (params)
  (Block @3:13-5:2
    ...))
```

The form is deterministic: two parses of the same input must
produce byte-identical canonical strings. The runner normalises
trailing whitespace on each line and strips trailing newlines
before comparing, but interior whitespace (indentation) is
part of the contract.

## TYPES  *(forthcoming — defined by `type-system.md`)*

Sorted table by span. One row per typed sub-expression. Form:

```
line:col-line:col  kind  name?  type
```

Type strings are in spec-canonical notation (`Map<int, []int>`, not
`map[int][]int`).

## ERRORS

One diagnostic per line, in the canonical emission format from
`diagnostics.md` §Diagnostic formatting:

```
<file>:<line>:<col>: <severity-label>[<code>]: <message>
```

`<severity-label>` is `error` (E), `warning` (W), or `internal`
(I). `<code>` is a stable identifier from the diagnostics
catalog (e.g. `E0201`). Examples:

```
src.td:1:1: error[E0103]: Unknown name
src.td:4:9: warning[E0503]: Soft shadow
src.td:7:5: error[E0303]: Non-exhaustive match
```

The `message` text is part of the contract — both Go and Tide
implementations must emit byte-identical strings matching the
catalog's `message` column. Optional secondary lines (snippet,
caret, fix hint) follow the primary line indented by two
spaces; they're informational and not part of the byte-compare
contract.

**Sema coverage convention.** There are deliberately no
`tests/sema/` fixtures: the sema layer's coverage gate is
satisfied by the Go table-tests in `internal/sema/*_test.go`, not
by `ERRORS`-section fixtures. A sema diagnostic's contract is behavioural — *does code
E fire (in `.td` coordinates) on the failure case, and stay silent
on the corpus* — which the table-tests assert directly (input →
the set of emitted codes). The `ERRORS` fixture format above stays
the **cross-implementation** contract (byte-identical output once a
Tide re-implementation of sema exists); until then the Go
table-tests are the authoritative sema coverage. New sema
diagnostics satisfy the atomic-coverage rule with a table-test.

## GO

The emitted Go source, after `gofmt -s`. This is the simplest
section: Go's own canonicaliser does the work. Implementations
must produce code that round-trips through `gofmt -s` to the same
text.

## STDOUT / STDERR / EXIT

Byte-identical to runtime output. For concurrent programs whose
output is order-unspecified, use the variants:

- `--- STDOUT-UNORDERED ---` — runner sorts lines before diffing.
- `--- STDOUT-MULTI-RUN ---` — runner accepts one of several
  acceptable outputs (each separated by `---` inside the section).
- `--- STDOUT-CONTAINS ---` — runner checks substring presence
  rather than full equality.

## Normalization rules — shared

- File paths in any position are **relative** to the repo root
  (e.g. `examples/core-language/hello/hello.td`, not `/home/.../hello.td`).
- Position / span format is always `line:col` or `line:col-line:col`,
  1-indexed, character-counted not byte-counted (UTF-8 aware) —
  applies uniformly to TOKENS, AST spans, TYPES rows, ERRORS lines.
- Type printer is stable: `Map<K, V>` always exactly so; one space
  after `,`. Sum-type variants printed in declaration order.
- Error messages quote source verbatim with double quotes and
  escape only `\`, `"`, newline.
- Trailing whitespace on any line in a `--- ... ---` section is a
  test bug; the runner treats it as a diff.

## Runner behaviour

- Reads file as UTF-8.
- Splits into sections by `^--- ([A-Z-]+) ---$` regex.
- Runs the compiler stages in pipeline order; stops at the deepest
  section present.
- For each present section, runs the stage's `Canonical()`-style
  serializer and `diff`s against the expected section text.
- On any diff: prints structured failure with file path, section
  name, and unified-diff body.
- Exit non-zero if any test fails.

The Go implementation lives in `../tests/harness/`; a Tide
re-implementation must produce the same output format for the same
input.
