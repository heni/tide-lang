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
be pattern-destructured. It is a **reference type** admitted into
`refEq` (the relaxed `T-RefEq`). It models `*exec.Cmd`,
`*regexp.Regexp`, `os.File`, `*sql.Rows` — Go library types used
*through methods* — and lowers to the Go pointer type `*pkg.Sym`
(`lowering-go.md` §ForeignCall).

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

## Variadic parameters and spread

A trailing parameter spelled `name: ...T` is **variadic**: the call site
supplies zero or more arguments of element type `T`, and inside the body
`name` has type `[]T`. Only the **final** parameter of a function,
method, or `extern func`/`extern impl` method may be variadic; a `...T`
followed by another parameter is **E0115**. This is an ordinary language
feature — it benefits plain Tide functions — but it is the binding-layer
unblocker that lets Go variadics (`exec.Command(name string, arg
...string)`) bind faithfully rather than bail.

A call passes the variadic tail two ways:

- **Inline** — `f(a, x, y, z)` collects `x, y, z` as the `[]T` tail.
  Each tail argument is checked against `T` (a mismatch is **E0201**).
- **Spread** — `f(a, ...xs)` forwards an existing slice `xs: []T`. A
  spread is written `...e` and is only legal as the **final** argument of
  a call whose callee's last parameter is variadic; otherwise **E0213**.
  The spread expression must fit the `[]T` parameter (else **E0201**).

Typing (T-Variadic / T-Spread): for `f: (P₁, …, Pₙ, ...T) → R`, a call
`f(a₁, …, aₙ, t₁, …, tₘ)` requires each `aᵢ : Pᵢ` and each `tⱼ : T`,
yielding `R`; with `m = 0` the tail is empty. A spread `f(a₁, …, aₙ,
...e)` requires `e : []T`. The fixed-arity shortfall (`< n` arguments) is
**E0202** ("expects at least n").

Lowering (`lowering-go.md` is unaffected — the shapes are Go-native): a
variadic parameter lowers to Go's `name ...T`; an inline tail lowers
argument-for-argument; a spread `...e` lowers to Go's `e...`. An
`extern func`/method emits no Go signature, so a variadic binding is
purely a call-site concern: `command("echo", ...args)` lowers to
`exec.Command("echo", args...)`.

Grammar: `grammar.ebnf` §Param / §Arg. AST: `ast.md` §Param
(`variadic`) / §SpreadArg.

## Verify the declaration, do not trust it

Because Tide emits Go and then compiles it, the **Go type checker
re-verifies every binding** against the real package:

- At **generation** time, signatures come from `go/types` — wrong
  arity / types are impossible by construction.
- At **build** time, the emitted call (`pkg.GoName(args)`) is
  type-checked by Go; a binding that has drifted from its package fails
  the build. The *contract* is that such a failure is surfaced in
  **`.td` coordinates and Tide terminology**, never as a raw `go/types`
  diagnostic. The current lowering achieves the build-time *rejection*
  (a drifted binding cannot miscompile); the `.td`-coordinate
  binding-drift **diagnostic** that translates the Go error back to
  Tide source is a later slice (until then the Go error leaks).

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

## The generator — `tide import`

`tide import <go/import/path>` introspects a Go package's type
information and prints a deterministic (name-sorted) `.td` binding file
of `extern` items — a *starting point a human owns*, not an always-on
translation. The output is reviewed source; the Go type checker catches
any residual error at build (verify-don't-trust).

Introspection uses the **stdlib `go/importer` in "source" mode**, not
`golang.org/x/tools/go/packages` — source-mode importing gives the same
full `go/types` information for the stdlib targets this epoch needs while
keeping the compiler dependency-free (the project's stdlib-only ethos).
The third-party / module-aware loading `go/packages` provides lands with
the third-party plumbing, if needed.

Each symbol is rendered with its translated signature and the boundary
lifts; the generator marks what the curator must review inline:
`// UNBINDABLE <name>: <reason>` for a symbol it cannot translate, and a
`// GUESS` note on a `(T, bool) → Option<T>` lift (which it cannot prove
is comma-ok rather than a meaningful bool). Names colliding with a Tide
keyword are escaped (`Match → match_`), the `@go` attribute pinning the
real Go symbol.

Every emitted `extern` binding round-trips through the compiler — it
parses and type-checks with no diagnostics. (Precondition: a package
with ≥ 1 bindable symbol. A package whose *every* export bails yields a
comment-only report carrying a "no bindable symbols" note, not a
compilable module — there is nothing to bind.)

This epoch's generator binds a **named type whose underlying is an
interface** (e.g. `os.Signal`) as an opaque handle, and **bails** on a
`func`-typed parameter or result (Tide closures-as-FFI-callbacks are a
later slice). Both diverge from the RFC's eventual table (interfaces →
Tide `interface`, `func(A) R → (A) => R`); the curator bridges the gap
by hand until those lifts land.

## Bindable subset and bail-out

A symbol whose signature uses only translatable types is bindable; one
that uses an untranslatable type (`unsafe.Pointer`, `uintptr`,
`complex*`, arity-≥3 non-error returns, anonymous interfaces, `func`
types, cross-package named types) is **not** emitted as a
binding: the generator currently renders it as a `// UNBINDABLE` comment
naming the real reason, so one untranslatable symbol does not sterilise
the rest of the package. The RFC's stronger **poison declaration** (a
binding that compiles but raises a `.td`-coordinate diagnostic on *use*)
is a follow-up; the comment form already prevents silent mistranslation.

## Dependency model

Stdlib bindings need no module dependency (the emitted `go.mod` stays
require-free). Third-party bindings are admitted **only** through an
explicit, pinned, hermetic binding declared in a **binding manifest**
(`std/bindings.json`) and resolved via a vendored `replace` (the amended
third-party-dependency decision, D19).

The manifest maps a third-party Go import path to a module, a pinned
version, and a vendored copy under `std/vendor/`. When a generated
program imports a manifest-listed path, the build emits a `go.mod`
carrying a `require` for the module plus a `replace` to the **absolute
path of the vendored copy**, so the build never touches the network —
the hermeticity guardrail. A program that uses no third-party binding
gets the plain require-free module. The toolchain locates the manifest
via `$TIDE_ROOT`, else the nearest ancestor of the cwd holding
`std/bindings.json`.

The proving case is `examples/ffi/config_reader` binding the vendored
`example.com/tidekv` module; a real third-party library
(`github.com/BurntSushi/toml`) plugs into the same mechanism once
vendored — its `toml.parse<T>` would mirror `json.parse<T>`, differing
only in the underlying package and the manifest `require`. Generating
bindings for a *non-stdlib* package (module-aware loading, which
`go/importer` source mode does not do) is a separate follow-up — the
plumbing here is independent of how the binding `.td` is authored
(hand-curated, as the proving case is).
