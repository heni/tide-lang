# RFC-0005 — Go FFI: the foreign-binding interface

| Field | Value |
|---|---|
| Number | 0005 |
| Status | draft |
| Created | 2026-06-15 |
| Supersedes | — |
| Target | new `lang-spec/ffi.md`; `lang-spec/grammar.ebnf` (extern decls); `lang-spec/ast.md` (extern nodes); `lang-spec/type-system.md` (T-Extern, boundary translation); `lang-spec/diagnostics.md` (E06xx FFI codes); `lang-spec/lowering-go.md` (§ForeignCall); `docs/binding-surface.md` (recast as curated output of this interface) |

## Summary

Tide binds Go libraries through a **semi-automatic foreign-function
interface**: a generator reads a Go package's *type* information and
emits **declaration files** — Tide source whose function/method bodies
are the marker `EXT`, meaning "implemented by this Go symbol." A human
then curates those declarations and writes thin **adapter** functions
on top for ergonomics. The generated layer is allowed to be ugly; the
adapter layer is where the nice Tide API lives. This is the
"automation does the routine, humans build the interfaces and
adapters" split, and it is the concrete realisation of decision D6
("binding *signatures* derived mechanically from Go type info; humans
write only the idiomatic wrapper layer").

The interface covers the **whole FFI surface** — any Go package,
stdlib or third-party — not a fixed binding list. Its forcing
use-case is reimplementing the corpus-status analyzer (today a Python
script) in Tide, which needs `os/exec`, file/temp I/O, `path/filepath`,
and a *non-stdlib* TOML parser — proving third-party binding end to
end.

## Motivation

Three forces make this necessary now.

1. **The hand-written binding table does not scale.** Today the stdlib
   surface is a hand-maintained table of three lowering shapes
   (`rename` / `resultWrap` / `conversion`), grown one row per call "as
   the corpus demands." That is fine for `strings.split`; it is the
   wrong tool for `os/exec` (a `Cmd` struct with ~15 methods and
   fields), `regexp` (`*Regexp` with a dozen methods), or any library
   with real surface area. Each new package is a linear hand-edit
   across the codegen *and* sema tables, with no oracle that the
   signatures are right.

2. **`go/packages` is already in-process.** D9 chose Go as the host
   language *specifically* so the binding generator can introspect Go
   packages with `go/packages` / `go/types` in-process rather than by
   subprocess. The `internal/bindgen` package has stood as an empty
   stub ("Status: not implemented") since the project began. This RFC
   is its contract.

3. **Real programs need libraries Go's stdlib does not have.** The
   corpus analyzer reads TOML manifests; Go has no stdlib TOML parser.
   "Bind a Go library" therefore *must* include third-party packages,
   which forces a capability the compiler lacks today: generated
   modules are **stdlib-only** (the emitted `go.mod` has no `require`s).
   Binding any non-stdlib package needs module-dependency plumbing.

## Design

### Overview — two layers, one verified boundary

```
   Go package  ──[ tide import ]──►  raw declaration file (.td, EXT bodies)   ◄── generated, ugly, stable
                                          │  human curates (rename, prune, Option-lift)
                                          ▼
                                     curated binding module (.td)              ◄── reviewed once
                                          │  human writes adapters on top
                                          ▼
                                     adapter module (.td, ordinary Tide)       ◄── the nice API programs call
```

The architecture is the **`*-sys` / safe-wrapper split** that Rust's
C-binding ecosystem converged on (`bindgen` → raw `unsafe` `-sys`
crate; hand-written safe crate on top), chosen there for a *technical*
reason beyond ergonomics — a shared raw layer guarantees the foreign
library resolves once. Tide adopts the same split:

- **Raw binding layer** (generated, curated): one module per Go
  package, mechanical, minimal, *not* documented beyond a pointer to
  the upstream Go docs. Stays close to the Go shapes.
- **Adapter layer** (hand-written): ordinary Tide functions/classes
  that wrap the raw layer into an idiomatic, safe Tide API. This is
  where nullability is lifted, ownership/cleanup is modelled, panics
  are trapped, and names are made Tide-ish.

### Principle: verify the declaration, do not trust it

Every language in the `external`-keyword lineage (OCaml, ReScript,
Gleam, PureScript) shares one weakness, stated plainly in Gleam's
docs: *"the compiler … cannot verify that the function … returns the
specified types, or even that it exists."* A wrong `external`
declaration does not error — it **miscompiles silently**.

Tide does not have this weakness, and the RFC mandates that it never
acquires it. Because Tide emits Go and then compiles it, **the Go type
checker re-verifies every `EXT` declaration against the real package**.
This is cxx's "static-assert the boundary, don't translate the header"
move, available to us for free:

- At **generation** time, signatures come from `go/types` — wrong
  arity / types / variadics are impossible by construction (D6).
- At **build** time, the emitted call (`pkg.GoName(args)`) is
  type-checked by Go against the imported package; a binding that has
  drifted from its package fails the build.
- Per D10, such a failure must be surfaced in **`.td` coordinates and
  Tide terminology**, never as a raw `go/types` diagnostic pointing at
  generated Go. (A binding-drift diagnostic is a new obligation; see
  Paired edits.)

The lineage's silent footgun becomes Tide's build-time error.

### Declaration surface (`extern` / `EXT`)

A binding module is ordinary Tide source marked as foreign. Sketch
(exact spelling is an open question — semantics are firm):

```td
// bindings/exec.td — generated by `tide import os/exec`, then curated.
@go("os/exec")                      // package header: Go import path

extern type Cmd                     // opaque foreign handle — no visible layout

extern fn command(name: string, args: ...string): Cmd = "Command"

extern impl Cmd {
    output(): Result<[]byte, error> = "Output"      // method (receiver implicit)
    run():    Result<unit, error>   = "Run"
    var dir:  string                = "Dir"          // exported field
}
```

Elements:

- **`@go("import/path")`** — the package header. Maps the module's
  namespace (`exec`) to the Go import path. Replaces Borgo's
  hardcoded `REWRITE_MODULES`; the mapping lives in data (see
  *Dependency plumbing*).
- **`extern type T`** — an **opaque foreign handle**. Empty body: Tide
  knows the name, never the layout. Cannot be constructed by a Tide
  literal (only returned from an `extern fn`), cannot be pattern-
  destructured. Reference identity via `refEq` (D-class semantics).
  Models `*exec.Cmd`, `*regexp.Regexp`, `os.File`, `*sql.Rows`, … —
  the 90 % case, since Go library types are used *through methods*.
- **`extern struct T { f: U, … }`** — a **transparent foreign struct**:
  a Go struct whose *exported fields* Tide reads/writes directly
  (translated as a record). Used when a binding needs field access,
  not just methods.
- **`extern fn name(...): R = "GoName"`** — a package-level function.
  The body *is* the Go symbol; `= "GoName"` is the name override
  (Go `Command` ↔ Tide `command`, the D6 case convention). The form
  `{ EXT }` (no override, Tide name = Go name) is equivalent.
- **`extern impl T { … }`** — methods and fields on a foreign type.
  A method's receiver is the foreign value; an `extern var f: U` is an
  exported-field accessor (read for `let`, read/write for `var`).
- **Binding *kind* is explicit** (ReScript's lesson): function vs
  method-on-receiver vs field vs constructor are distinct forms, not
  one overloaded shape. The generator and the type-checker both key
  off the kind.

Adapters are **not** special syntax — they are ordinary Tide functions
in a separate module that call the raw `extern` items:

```td
// adapters/proc.td — hand-written, ordinary Tide.
import exec
fn run(name: string, args: []string): Result<string, error> {
    let c = exec.command(name, ...args)
    let out = try c.output()
    return Ok(strings.fromBytes(out))
}
```

### Type translation (the mechanical part)

The generator maps each Go type to a Tide type. The total rule:

| Go | Tide | Notes |
|---|---|---|
| `bool int int8…int64 uint…uint64 byte rune float32/64 string` | same | direct |
| `[]byte` | `[]byte` | |
| `[]T` | `[]T` | element translated |
| `map[K]V` | `Map<K,V>` | |
| `chan T` / `<-chan T` / `chan<- T` | `Channel<T>` / `RecvChan<T>` / `SendChan<T>` | |
| `func(A,B) R` | `(A,B) => R` | params/result translated |
| `...T` (variadic) | `...T` | |
| `error` | `error` | predeclared |
| **`(T, error)`** | **`Result<T, error>`** | boundary lift, below |
| **`(T, bool)`** (comma-ok) | **`Option<T>`** | boundary lift, below |
| `*T` (named) | opaque handle `T` (or `Option<T>` if nilable) | below |
| exported `struct{…}` | `extern struct` (record) **or** opaque `extern type` | human chooses (field-access vs method-only) |
| `interface{ … }` | Tide `interface` | exported methods translated |
| Go type param `[T any]` | Tide generic `<T>` | unbounded only (D11) |

**Boundary lifts** happen *at the binding*, not deep in user code
(ReScript's `@return(nullable)` / PureScript's `toMaybe` lesson):

- `(T, error)` → `Result<T, error>` — reuses the existing
  `resultWrap` lowering (`tideResultOf`).
- `(T, bool)` comma-ok → `Option<T>` — but a Go function legitimately
  returning a `bool` is **ambiguous** with comma-ok. The generator's
  guess (2nd-return-is-`bool` ⇒ Option) is a *default the human can
  override* in curation; the curated file is the source of truth.
- A nil-able `*T` → the adapter lifts to `Option<T>`; the raw layer may
  keep it as a handle that is never compared to nil in Tide.

**Panics never cross the boundary.** gobind's rule: a foreign panic
that unwinds across the FFI boundary terminates the process. Adapters
that call Go code which may panic must trap and convert to `Result`
(a `recover`-backed adapter primitive; see Open questions). The raw
layer does not trap — that is an adapter responsibility.

### The bindable-type subset, and bail-out

Following gobind (which publishes exactly which Go types cross), the
RFC defines a **bindable subset**. A symbol whose signature uses only
translatable types is generated; a symbol that uses an untranslatable
type is **not silently mistranslated**.

Untranslatable today (bail list): `unsafe.Pointer`, `uintptr`,
`complex64/128`, channels/maps of unbindable element, multiple
non-error returns of arity ≥ 3, embedded/anonymous struct fields,
generic *constraints* beyond `any` (D11 parks bounded generics).

**Bail-out strategy: poison-on-use, not demote-to-opaque.** Zig's
cautionary tale: demoting an untranslatable struct to an opaque type
silently makes *every function that takes it by pointer* un-callable
("C pointers cannot point to opaque types") — one bail sterilises a
whole module. Tide instead emits the unbindable symbol as a **poison
declaration**: it compiles, but *referencing* it raises a binding
diagnostic in `.td` coordinates ("`exec.foo` is not bindable: returns
`unsafe.Pointer`"). One untranslatable symbol does not block the rest
of the package, and the failure names the real reason at the use site.

### Name and namespace mapping

- **Symbol rename** — the trailing `= "GoName"` string decouples the
  Tide name from the Go symbol (universal across the lineage: OCaml
  `="caml_…"`, ReScript `="jsName"`, Crystal `= "c_name"`). Default is
  the D6 case convention (`ServeHTTP` ↔ `serveHTTP`, `Compile` ↔
  `compile`).
- **Namespace/path** — `@go("net/http")` supplies the Go import path;
  the Tide namespace is the package's short name (`http`). Nested
  packages (`io/fs`) are addressed by the manifest, not hardcoded.

### Adapter-facing runtime ABI is frozen

PureScript's sharpest footgun: FFI code that calls *generated* host
code is brittle to codegen changes and breaks dead-code elimination;
the fix is to depend only on stable, passed-in values. Tide's analog:
**hand-written adapters and any Go-side shim depend only on a frozen,
documented runtime/ABI surface** (the `Option`/`Result`/container
prelude, the `tideResultOf` family), never on the incidental shape of
generated Go. This contract is part of the runtime (D18); changing it
is a breaking change with its own review. Without this freeze, every
codegen tweak risks breaking every binding.

### The importer tool (`tide import`)

`internal/bindgen`, finally implemented, as a **batch dev tool** — not
runtime/compile-time magic. `tide import <go/import/path>`:

1. Loads the package with `go/packages` (full type info — *not* Borgo's
   `go/ast`/`go/doc`, which loses types).
2. Walks exported funcs / types / methods / fields, translating each
   signature by the table above.
3. Emits a **deterministic** (name-sorted) `.td` declaration file —
   so re-running produces a stable diff over human curation.
4. Marks every bail-out inline (`// UNBINDABLE: returns unsafe.Pointer`)
   and every guessed lift (`// GUESS: (T,bool) → Option<T>`) so the
   human curator sees exactly what to review.

The output is a **starting point a human owns**, like Borgo's hand-
edited `std/*.brg` and TypeScript's DefinitelyTyped — *not* an
always-on translation. This is what makes the approach stable: the
generator never has to be perfect, because its output is reviewed
source, and the Go type checker catches any residual error at build.

Drift policy (bindgen's two strategies): bindings are **pre-generated
and committed** (stdlib and pinned third-party are append-only enough);
re-running `tide import` after a dependency bump shows a reviewable
diff. No regenerate-on-every-build.

### Dependency plumbing (stdlib and third-party)

- A **binding manifest** (the `modules.json` analog) maps Tide
  namespaces to Go import paths and, for third-party packages, to a
  module + version. This replaces hardcoded path rewriting.
- **Stdlib bindings** need no module dependency — the emitted `go.mod`
  stays require-free, as today.
- **Third-party bindings** require new plumbing: the emitted `go.mod`
  gains a `require` for the bound module, resolved **hermetically** —
  a `replace` directive pointing at a vendored/local copy, so a build
  never depends on network/proxy state. The bound module's version is
  pinned in the manifest. (TOML via `github.com/BurntSushi/toml` is the
  proving case: `toml.parse<T>` mirrors the existing `json.parse<T>`
  shape exactly, differing only in the underlying package and the
  `require`.)
- Generated Go imports are derived from the bindings actually used
  (transitive-closure tracking, as the concurrency prelude already
  does for indirect deps), not from a static list.

### Coverage obligation

Per the project's atomic-coverage rule, the constructs this RFC adds
(`extern type` / `extern fn` / `extern impl` / `@go` header, the
boundary-lift lowering, the new diagnostics) each owe ≥ 1 atomic
fixture in `tests/{grammar,sema,codegen}/` on `implemented`. Live
coverage is the corpus analyzer itself (soft until it lands). The Go
type-checker re-verification is itself a testable invariant (a binding
whose Go symbol is removed must fail with the `.td`-coord drift
diagnostic, not a `go/types` leak).

## Alternatives considered

- **Fully-automatic, always-on bindgen** (introspect + translate at
  compile time, no human in the loop). Rejected: the `external`-lineage
  and Zig's `translate-c` both show auto-translation is brittle at the
  edges (nullability, ownership, comma-ok ambiguity, untranslatable
  types) and, when always-on, every edge case becomes a silent
  miscompile or a sterilised module. The generate-then-curate split
  keeps the human where judgement is required and the machine on the
  routine — and is explicitly the chosen direction.
- **Keep the hand-written table forever.** Rejected: linear hand-edits
  across two tables per package, no signature oracle, and it cannot
  reach real library surface area (motivation §1).
- **Go-side shim packages as the only adapter mechanism** (write a Go
  adapter exposing a Tide-shaped API, bind that). Considered and kept
  as an *option* for impedance the Tide adapter layer cannot express,
  but not the primary path: it splits the adapter across two languages
  and still needs the third-party-module plumbing. Pure-Tide adapters
  over raw `extern` items are the default.
- **Borgo's `go/ast`/`go/doc` importer verbatim.** Rejected: it is
  untyped (loses the very type info that makes D6 sound), hardcodes
  module paths, denies large packages, and `log.Fatal`s on shapes it
  cannot handle. We adopt its *shape* (`EXT`, opaque empty types, the
  two lift rules, a module manifest) and replace its engine with a
  `go/types`-driven, build-verified one.

## Paired edits

On acceptance / implementation, these `lang-spec/` edits land:

- **`lang-spec/ffi.md`** (new) — the authoritative FFI contract: the
  declaration grammar, the type-translation table, the boundary-lift
  rules, the bindable subset + poison-on-use bail-out, the
  verify-don't-trust principle, the manifest + dependency model.
- **`lang-spec/grammar.ebnf`** — `ExternDecl`, `ExternType`,
  `ExternImpl`, the `@go` header, the `= "GoName"` override.
- **`lang-spec/ast.md`** — extern AST nodes.
- **`lang-spec/type-system.md`** — `T-Extern` (typing an `EXT` call),
  the boundary-translation judgement, opaque-handle rules.
- **`lang-spec/diagnostics.md`** — new FFI diagnostics (suggested
  E06xx range): unbindable-symbol-referenced, binding-drift
  (`.td`-coord wrapper over the Go type error), foreign-panic-uncaught,
  third-party-module-unresolved.
- **`lang-spec/lowering-go.md`** — §ForeignCall: `EXT` → `pkg.GoName`,
  the `(T,error)`/`(T,bool)` lift lowering, the `go.mod`
  `require`/`replace` emission.
- **`docs/binding-surface.md`** — recast from a hand-authored target
  list into the *curated output* of this interface; the "not in v1"
  section becomes "not yet generated."

## Transition / compatibility

Strictly additive at the language level: `extern` / `@go` / `EXT` are
new surface; no existing program changes. The existing hand-written
binding table is **superseded incrementally** — each package it covers
is replaced by a generated+curated binding module, behaviour-preserving
(the `build_ok` ratchet guards this), until the table is empty. No user
migration. The one genuinely new infra capability (third-party `go.mod`
plumbing) is opt-in: stdlib-only programs are unaffected.

## Open questions

These are flagged for resolution during stabilisation; an RFC may be
accepted with them open, resolved before `implemented`.

1. **Exact declaration spelling.** `extern fn … = "GoName"` vs
   `{ EXT }` body vs an `@go`-attribute-per-item form; `extern impl T`
   vs methods-inside-`extern type`. Semantics are firm; the surface is
   a bikeshed to settle against Tide's existing `class`/`func` style.
2. **Opaque handle ↔ Tide interface conformance.** May an opaque
   `extern type` *implement* a Tide `interface` (so a `*os.File`
   satisfies a Tide `io.Writer`)? Likely yes — needed for the I/O
   bindings — but the conformance check across the FFI boundary needs
   specifying.
3. **Panic-trapping primitive.** The shape of the adapter-level
   `recover`-backed "call Go that may panic → `Result`" primitive: a
   library function, a language form, or a generation-time wrapper?
4. **Comma-ok detection.** Whether the generator can do better than the
   2nd-return-is-`bool` heuristic (e.g. recognise map-index / type-
   assert syntactic origins), or whether human override is always the
   answer.
5. **Third-party hermeticity mechanism.** `replace`-to-vendored vs a
   committed module cache vs a `tide.toml`-declared dependency set —
   which gives reproducible builds with the least ceremony.
6. **Callbacks Go→Tide.** Passing a Tide closure as a Go `func`
   argument (needed for `sort.Slice`-style APIs) across the boundary —
   in scope for v1 of the FFI, or deferred? (The corpus analyzer does
   not need it; other corpus files might.)
7. **Foreign-handle lifecycle.** Go is GC'd, so no manual free — but
   resources needing `Close()` (files, processes) want a `defer`-shaped
   discipline. Whether the FFI models this or leaves it to adapters.
