# Foreign-binding interface (Go FFI)

The contract for binding Go libraries into Tide. This file is the
formal mirror of the accepted design in `../docs/rfcs/0005-go-ffi.md`;
on disagreement the formal files (`grammar.ebnf`, `ast.md`,
`type-system.md`, `lowering-go.md`) win, and this file is updated to
match.

Tide binds Go through a **generate-then-curate** split: a generator
(`tide import`) reads a Go package's type information and emits
**declaration files** — Tide source whose items are marked foreign;
a human curates them and writes idiomatic **adapters** on top. The raw
binding layer stays close to the Go shapes; the adapter layer is
ordinary Tide. This file specifies the *raw* layer's surface and
semantics.

## Declaration surface

A binding module is ordinary Tide source whose foreign items are
introduced by the `extern` keyword. Three declaration forms, plus a
per-item `@go("...")` attribute that names the Go referent.

```td
extern type Cmd @go("os/exec")

extern func command(name: string, args: []string): Cmd @go("os/exec.Command")

extern impl Cmd {
    output(): Result<[]byte, error> @go("Output")
    run():    Result<unit, error>   @go("Run")
    var dir:  string                @go("Dir")
}
```

### `extern type` — opaque foreign handle

`extern type T @go("pkg")` declares an **opaque foreign handle**: Tide
knows the name `T`, never the layout. A handle cannot be constructed by
a Tide literal (only returned from an extern function/method) and cannot
be pattern-destructured. It is a **reference type**; a handle is
admitted into `refEq` (the relaxation of the class-only `T-RefEq`
premise lands with the sema PR). It models `*exec.Cmd`,
`*regexp.Regexp`, `os.File`, `*sql.Rows` — Go library types used
*through methods*.

A raw handle **may carry Go's `nil` unchecked**: Go has no static
nilability, so the generator cannot prove non-nil. Guarding nil and
lifting `*T → Option<T>` is an **adapter** responsibility — the raw
layer never auto-lifts a handle to `Option`.

A handle is **opaque** (T-Extern): it cannot be built from a Tide
literal or constructor call (**E1001**), cannot be tuple/record-
destructured (**E1002**), and is excluded from structural `==`/`!=`
(routed to `refEq`). It **is** admitted into `refEq` as a
reference-identity type — `refEq(a, b)` on two handles of the same
`extern type` is well-typed (the relaxed T-RefEq / E0206; now in
effect).

### `extern func` — package-level foreign function

`extern func f(params): R @go("pkg.Sym")` binds a package-level Go
function. An extern function has **no body**: the `@go` attribute is the
binding. The boundary lifts (below) apply to its return type.

### `extern impl` — methods and fields on a handle

`extern impl T { … }` declares methods and exported-field accessors on
a foreign handle. A method's receiver is the foreign value (implicit).
An `extern var f: U` is a read/write exported-field accessor; `let` is
read-only. The Go import path is **not** repeated here — it comes from
`T`'s own `extern type` declaration.

### The `@go("...")` attribute

`@go("...")` names the Go referent. `@` is a token no ordinary v1
production accepts; the FFI cashes that reservation for this attribute
alone. The string is interpreted by **position**:

- On an `extern type` / `extern func`: an **import path**, optionally
  suffixed `.Symbol`. The package/symbol split is on the **last `.`
  after the last `/`** — `"os/exec.Command"` → package `os/exec`, symbol
  `Command`; `"os/exec"` → package `os/exec`, symbol defaulted.
- On an `extern impl` member: the **bare Go method/field name** (the
  package comes from the receiver handle).
- **Omitted** (`@go` absent, or path with no `.Symbol`): the Go symbol
  defaults to the case-converted Tide name — Tide `command` ↔ Go
  `Command`, Tide `Cmd` ↔ Go `Cmd` (the standard convention).

The per-item `@go` **supplants** the RFC sketch's `EXT` body marker and
trailing `= "GoName"` rename: a single attribute carries the binding
target, so an extern item never has a body. `EXT` is therefore not a
Tide token. (This resolves the declaration-spelling open question.)

Grammar: `grammar.ebnf` §"Foreign-binding". AST: `ast.md` §"Foreign
bindings". Lexical surface: `keywords.md` (`extern` hard keyword;
`impl`/`go` contextual; `@` attribute head).

## Verify the declaration, do not trust it

Because Tide emits Go and then compiles it, the **Go type checker
re-verifies every binding** against the real package:

- At **generation** time, signatures come from `go/types` — wrong
  arity / types are impossible by construction.
- At **build** time, the emitted call (`pkg.GoName(args)`) is
  type-checked by Go; a binding that has drifted from its package fails
  the build. Such a failure is surfaced in **`.td` coordinates and Tide
  terminology**, never as a raw `go/types` diagnostic (a binding-drift
  diagnostic; lands with the codegen/diagnostics PR).

This is the property the `external`-keyword lineage (OCaml, ReScript,
Gleam, PureScript) lacks — there a wrong declaration miscompiles
silently. In Tide it is a build-time error.

## Typing the surface

An extern function/method/field is typed by its declared (curated)
signature, exactly like an ordinary call/field access — the rules are
**T-Extern** (`type-system.md` §"Foreign handles"). Because the curated
`.td` writes any boundary-lifted return type (`Result<·, error>`,
`Option<·>`) **directly**, there is no separate lift judgement at the
type level: `extern func atoi(s: string): Result<int, error>` simply
*is* a function returning `Result<int, error>` to the type checker. The
lift from Go's `(int, error)` into that `Result` is a **lowering** rule
(`lowering-go.md` §ForeignCall), applied at codegen.

The mechanical Go→Tide type map the **generator** uses, and the two
automatic boundary lifts it applies when emitting the curated file —
`(T, error) → Result<T, error>` and comma-ok `(T, bool) → Option<T>` —
are specified in `../docs/rfcs/0005-go-ffi.md` §"Type translation". The
invariant: the only automatic `Option`/`Result`-producing lifts are
those two; a nil-able `*T` is an **adapter** lift, never automatic.

## Bindable subset and bail-out

A symbol whose signature uses only translatable types is bindable; one
that uses an untranslatable type (`unsafe.Pointer`, `uintptr`,
`complex*`, arity-≥3 non-error returns, embedded/anonymous fields,
bounded generics) is emitted as a **poison declaration**: it compiles,
but *referencing* it raises a binding diagnostic in `.td` coordinates,
naming the real reason at the use site. One untranslatable symbol does
not sterilise the rest of the package. Detail and the diagnostic codes
land with the generator and sema PRs.

## Dependency model

Stdlib bindings need no module dependency (the emitted `go.mod` stays
require-free). Third-party bindings are admitted **only** through an
explicit, pinned, hermetic binding declared in a binding manifest and
resolved via a vendored `replace` (the amended third-party-dependency
decision). The manifest and `require`/`replace` emission land with the
third-party-plumbing PR.
