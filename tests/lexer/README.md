# tests/lexer/

Atomic lexer fixtures. Each file is a manifest per
[`lang-spec/test-contract.md`](../../lang-spec/test-contract.md)
with two sections at minimum. The Go runner strips one trailing
newline from each section body, so INPUT bodies end at their last
visible line — no spurious final-newline Newline tokens unless
the input genuinely spans multiple lines.

Two sections required:

```
--- INPUT ---
<.td source>

--- TOKENS ---
<canonical TOKENS form>
```

The Go-side runner (`internal/lexer/fixtures_test.go`) walks every
`*.txt` here, lexes the `INPUT`, and byte-compares the result
against `TOKENS` (whitespace-normalised).

For negative cases (lexer errors), `TOKENS` is replaced by
`ERRORS`:

```
--- ERRORS ---
<one diagnostic per line, see test-contract.md §ERRORS>
```
