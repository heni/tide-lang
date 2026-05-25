# tests/codegen/

Atomic codegen fixtures. Each file is a manifest per
[`lang-spec/test-contract.md`](../../lang-spec/test-contract.md)
with these sections:

```
--- INPUT ---
<.td source>

--- GO ---
<emitted Go after gofmt -s>

--- STDOUT ---
<runtime stdout, byte-identical>      # optional, PR-D and later

--- EXIT ---
<integer exit code>                   # optional, PR-D and later
```

The Go-side runner (`internal/codegen/fixtures_test.go`) walks
every `*.txt` here, lexes + parses + emits Go from `INPUT`, and
byte-compares against `GO`. The `STDOUT` / `EXIT` sections are
declared on PR-C but actually executed by PR-D's CLI / runner.

Output is guaranteed gofmt-stable (`Emit` calls `go/format.Source`
on the buffer before returning), so writing the `GO` section is a
matter of running the source through `gofmt -s` and pasting.

Same trailing-newline rule as the other test dirs: the runner
strips all trailing newlines from each section body before
comparison.
