# std/ — generated standard-library bindings

Tide bindings for Go standard-library packages, produced by `internal/bindgen`
(see `docs/architecture.md` section 3).

These files are **generated**. Raw binding signatures are derived mechanically
from `go/packages` type information (D6 — see `docs/design-decisions.md`);
only the idiomatic wrapper layer involves human or agent judgment. Do not
hand-edit signatures here.

Empty until the binding-generator phase lands. First targets: `fmt`, `os`,
`io`, `errors`, `strconv`, `strings`, `bytes`, `time`, `context`,
`encoding/json`.
