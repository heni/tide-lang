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

## AST  *(forthcoming — defined by `ast.md`)*

S-expression with spec-canonical node names. Each node carries a
`@line:col-line:col` span attribute. The exact form lands with
`ast.md`.

## TYPES  *(forthcoming — defined by `type-system.md`)*

Sorted table by span. One row per typed sub-expression. Form:

```
line:col-line:col  kind  name?  type
```

Type strings are in spec-canonical notation (`Map<int, []int>`, not
`map[int][]int`).

## ERRORS  *(forthcoming — defined by `diagnostics.md`)*

One diagnostic per line:

```
[ERROR|WARNING] file:line:col  ECODE  message
```

`ECODE` is a stable identifier from the diagnostics catalog (e.g.
`E0201`). The `message` text is part of the contract — both Go and
Tide implementations must emit byte-identical strings.

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
  (e.g. `examples/hello.td`, not `/home/.../hello.td`).
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
