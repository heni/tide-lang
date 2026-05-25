# tests/grammar/

Atomic parser fixtures. Each file is a manifest per
[`lang-spec/test-contract.md`](../../lang-spec/test-contract.md)
with three sections in the typical positive case:

```
--- INPUT ---
<.td source>

--- AST ---
<canonical S-expression>
```

The Go-side runner (`internal/parser/fixtures_test.go`) walks
every `*.txt` here, lexes + parses the `INPUT`, and byte-compares
the canonical AST serialization against `AST`.

For negative cases (parse errors), use `ERRORS` instead of `AST`:

```
--- ERRORS ---
src.td:line:col: error[EXXXX]: message
```

The same trailing-newline rule as `tests/lexer/` applies: the
runner strips all trailing newlines from each section body, so
fixture writers can use natural blank-line spacing.
