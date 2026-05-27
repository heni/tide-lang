# Design decisions

The architectural commitments behind Tide, in one place. Each decision has a
**claim** (what the project does), **why** it does that, and the
**trade-offs** that follow. The numbering (D1, D2, ...) is a stable label
used across the docs and source comments — when something says "(D14)" it
points here.

> This is the public, polished view. The full working history of how each
> decision was made — together with the open questions and the testing
> philosophy — lives in the project's internal notes and is not part of
> this document.

---

## Positioning

**One line.** Familiar TypeScript-style syntax, free of JavaScript's legacy,
on the Go runtime.

**Audience: TypeScript developers.** That is who Tide is for, and it shapes
everything. The familiar surface is genuine value for this audience, not a
cosmetic detail — it removes the largest cost of adopting a new language
(relearning everything from scratch).

What Tide gives a TypeScript developer:

- **A syntax you already know** — productive from day one.
- **No JavaScript legacy** — dropped, deliberately: `prototype`, `this`,
  implicit coercions, dynamic open objects, decorators, `any`,
  `Promise`/async coloring, npm.
- **A more capable type system** — sum types, exhaustive matching,
  `Option`/`Result`, nominal newtypes, no `any`. This is an ML-family type
  system under the hood. For the user the point is simple: the types catch
  more, and `null` and `any` stop leaking through.
- **The Go runtime** — goroutine scheduler, GC, single-binary deployment,
  fast startup, the standard library.
- **Errors without `if err != nil`** — `let x = try foo()` over Go's error
  model. People love the Go runtime and tolerate Go's errors; Tide keeps
  the first and removes the second.

**Honest framing.** Tide is not a TypeScript-to-Go transpiler. That phrasing
would make people expect npm, JavaScript semantics, and a browser ecosystem
— none of which Tide has, deliberately.

**Not present:** npm packages, JavaScript interop, `Promise`, decorators,
dynamic objects, browser targets, systems-level memory control.

### The honest cost — npm

Dropping JavaScript *semantics* is free. Dropping *npm* is not — it removes
the largest package ecosystem in the world, and that cost is real. Tide's
answer is not "recreate npm"; it is to bind the finite Go standard library
(see D3) and to host its own packages with a decentralized registry
(see D5). Whether that is enough for a given use case is a fair question to
ask of the project.

---

## Decisions

### D1 — Go is an intermediate representation, not a target

**Claim.** Generated Go is a build artifact, like assembly or LLVM IR. It is
not meant to be read or maintained, and "readable generated Go" is not a
project goal.

**Why.** A rich type system — sum types, exhaustive matching, non-nullable
types — has no idiomatic Go encoding. Forcing codegen to produce idiomatic
Go would either crush the type system into something Go-shaped, or pay
heavy runtime cost. Treating Go as IR frees the type system to be what it
needs to be.

**Trade-off.** Tide commits to first-class source-level debugging (see D8)
so the user never has to read generated Go. This is non-negotiable and lands
in Phase 1, not later as polish.

### D2 — TypeScript-flavored syntax, ML-family type system, no JavaScript semantics

**Claim.** The *surface syntax* is TypeScript-flavored — productive for TS
developers from day one. The *type system* is ML-family and richer than
TypeScript's (see D14). The JavaScript runtime legacy is discarded
entirely.

**Why.** The surface should feel familiar. The semantics should not be — a
modern type system catches a class of errors TypeScript can't, and dropping
JavaScript semantics is a feature, not a regression.

**Trade-off.** Tide must not be described as a TypeScript transpiler. That
phrasing imports the wrong expectations (npm, JS semantics, browser).

### D3 — Bind, do not port, the Go standard library

**Claim.** For Go stdlib functionality, Tide generates a thin typed
*binding* over the real Go package. It never *ports* (reimplements).

**Why.** Because Go is the IR (D1), a call into a Go package costs nothing
at runtime — no FFI, no marshalling. Binding inherits Go's tested
implementation, performance, and security patches for free. Porting means
owning a worse, diverging, unmaintained fork forever; no scenario favors it.

**Note.** `runtime`, `sync`, `syscall`, `reflect`, and parts of `crypto`
*are* the Go runtime. They are unportable in principle and can only be
bound.

### D4 — Go packages are reachable only through an explicit binding layer

**Claim.** No first-class Go imports, no seamless interop. Go is accessed
only through generated bindings.

**Why.** This quarantines the impedance — `nil`, bare pointers, `(T, error)`,
`interface{}`, `context` — at one explicit, generated boundary, instead of
letting it bleed through every file.

**Trade-off.** Tide consciously declines to use the Go ecosystem as a free
bootstrap. That sharpens the cold-start problem.

### D5 — Tide has its own decentralized package ecosystem

**Claim.** Tide packages distribute with a go-mod-style model: import path
as URL, no central registry, MVS-style version resolution, vendoring.

**Why.** Go proved this model. It's cheap to copy and needs no central
service.

**Note.** The hard part of losing npm was never the plumbing — it is the
cold start, which this does not solve.

### D6 — Bindings are generated from `go/packages`, not hand-written

**Claim.** Binding *signatures* are derived mechanically from the Go type
checker. Humans and agents write only the *idiomatic wrapper* layer.

**Why.** Deriving signatures from real type information makes wrong arity,
wrong types, wrong returns, wrong variadics, and wrong generics impossible
by construction. Only the semantic layer — nullability, `(T, error)` vs
comma-ok, panics — needs human review.

### D7 — No async/await function coloring; concurrency is uncolored

**Claim.** No `async` signatures, no `await`. Any function may block; the
Go scheduler handles it. Concurrency uses `spawn`, channels, and
structured-concurrency scopes.

**Why.** Function coloring is a JavaScript wart. Goroutines are uncolored
— inheriting that is a gift; reintroducing coloring would import the worst
part of the JS concurrency model.

**Trade-off.** The genuinely hard problem cancellation handles is solved by
structured scopes mapped onto Go's `context`. The colored-function model
"solves" cancellation by making it the caller's problem; the structured
model is more demanding to design but a better answer.

### D8 — Source-level debugging via `//line` directives

**Claim.** Codegen emits `//line file:line` for every construct, so Go
panics, stack traces, `runtime.Caller`, and (via DWARF) `delve` and `pprof`
all report `.td` locations.

**Why.** D1 makes Go invisible. Without source maps, every panic and
profile would point at unreadable generated code — destroying the user
experience.

**Status.** A Phase-1 requirement, not later polish.

### D9 — The compiler is implemented in Go

**Claim.** The Tide compiler is written in Go.

**Why.** Go is already a dependency (it is the IR and the runtime), so this
avoids a second toolchain. Critically, the binding generator (D6) must
introspect Go packages — in Go, `go/packages` / `go/types` / `go/ast` /
`go/printer` are available in-process rather than via subprocess.

**Trade-off.** Go is verbose for compiler code and lacks sum types — ironic
for a project whose central claim is sum types. Accepted; a self-hosted
compiler is a long-term goal.

### D10 — Errors are reported in Tide source coordinates

**Claim.** Type and binding errors surface in `.td` coordinates and Tide
terminology. Raw `go/types` diagnostics that point at generated Go are a
**bug**, caught by a negative test.

**Why.** Errors pointing at code the user never wrote destroy the
"TypeScript ergonomics" promise.

### D11 — Surface-syntax discipline

**Claim.** The Tide surface is **TypeScript-flavoured, ML-influenced**, with
a single, settled spelling for each construct. The acceptance-suite paper
validation forced concrete answers across roughly thirty surface-syntax
questions; the resolutions live in `docs/language-spec.md`.

**Why.** A language with two equally valid spellings for the same thing
("many ways to do it") inflates surface area for users and for the
compiler. The paper-validation pass kills "let's pick later" by
demanding that every example be writeable **one obvious, unambiguous
way**.

**Status.** *Mostly firm.* Three items remain open: the concrete syntax
for nominal newtypes (`newtype X = T` with optional methods, working
shape), the typed `match v as { T => ..., U => ... }` narrowing form for
the `Any` escape type, and visibility / public-private on class members.
All three are scoped out of the v1 acceptance suite; the suite is
expressible without them.

### D12 — v1 scope is defined by an example acceptance suite

**Claim.** The set of programs in `examples/README.md` defines what v1 must
be able to express. Each example is chosen to *force* specific language
features; an example compiling and running is the definition of done for
those features.

**Why.** Examples-as-acceptance-criteria keeps the language honest —
features are designed against concrete, representative programs, not in the
abstract.

### D13 — Validate the spec on paper before building the compiler

**Claim.** Before any compiler code is written, every example in the
acceptance suite (D12) is hand-implemented as a complete `.td` program
against `docs/language-spec.md`. The spec passes only when each example
can be written *one obvious, unambiguous way*.

**Why.** Writing real programs in a spec is the cheapest way to expose
under-specification, ambiguity, and awkwardness — and to force the
remaining D11 syntax questions to resolution. Doing this *before* a
lexer or parser exists keeps mistakes cheap: a missing form in the spec
costs an edit, not a compiler rebuild.

**Status.** Complete for v1. Found roughly thirty surface-syntax gaps;
each has a resolution folded into `docs/language-spec.md` (G15 binding
naming convention also noted on D14).

### D14 — Structural records, nominal behavioral types

**Claim.** Plain data shapes (`type X = {...}` records) use **structural**
typing — two records with the same fields are interchangeable. Types that
carry behavior — `class`es and Tide-defined `interface`s — are **nominal**
with **explicit, declared** conformance: `class MyReader implements
io.Reader { ... }`. The checker verifies the declared method set; there is
no implicit or accidental satisfaction.

**Why.** Full structural subtyping over behavioral types invites three
failures: pathological type-checker complexity, confusing inference, and
accidental compatibility (two unrelated types interchangeable because their
method sets happen to coincide). Structural is right for data, wrong for
behavior. Explicit conformance also turns Go interop into a finite,
declared set the checker can verify, instead of an open-ended structural
compatibility check.

**Trade-off.** An explicit `implements` line per conformance is mild
ceremony, accepted as the price of no accidental compatibility. This makes
Tide less TypeScript-like and more ML-like (cf. Rust traits, Swift
protocols).

**Note.** This discipline governs Tide-authored types. Bound Go types
flowing into Go interfaces are satisfied structurally on the Go side at
codegen — Tide adds no checking there.

### D15 — Tide targets a defined, stable subset of Go as its IR contract

**Claim.** Tide does not assume "the current Go, forever." It commits to a
defined subset of the Go language and a supported Go version range, treated
as a stable IR contract.

- **Codegen** emits only the conservative stable subset; never depends on
  experimental Go features (`GOEXPERIMENT`, arenas, etc.).
- **Bindgen** consumes whatever the Go stdlib uses, so the subset it must
  *understand* grows across Go releases. It depends only on the stable
  `go/packages` / `go/types` public API and the Go specification — never on
  compiler internals.

**Why.** Without an explicit contract, codegen and bindgen drift into
dependence on incidental or unstable Go semantics, and Tide rots as Go
evolves.

**Status.** Pinning the exact subset is due before codegen settles.

### D16 — Tide is not a transpiler at the UX level

**Claim.** The user works entirely in Tide: `.td` source, Tide diagnostics,
Tide tooling, a Tide debugger view. Go is purely backend compiler
infrastructure the user never sees.

**Why.** A transpiler UX would constantly pull Tide back toward being a
thin skin over Go, undermining its identity as a language in its own right.
This is the product-identity counterpart to D1 (Go is an IR), D8 (`//line`
source maps), and D10 (errors in Tide coordinates) — naming it explicitly
guards against the project sliding into "just nicer Go syntax."

### D18 — Tide has a runtime, and it is part of the language contract

**Claim.** Tide ships a small runtime package (`tidert/`) whose surface is
part of the language contract, not a private codegen implementation
detail. Three things live there:

1. Method bodies for the predeclared generic containers (`Map`, `Set`,
   `Stack`). These have always lived in `tidert/`; this decision just
   names what was implicit.
2. Per-class / per-sum **type descriptors** — small records carrying
   Tide-side names (class name, field names, variant names, generic
   arguments), emitted by codegen and held in a runtime registry.
3. The reflection API (`Type`, `Kind`, `FieldInfo`, the `Dynamic`
   wrapper, the functions in the `reflect` module) that reads
   descriptors and box-values at runtime.

**Contract invariants (CT1–CT3).** These govern how the runtime
surface evolves over time.

1. **CT1. `tidert/` has a private and a public layer.** The container
   helpers (`Map.new`, `Set.new`, `Stack.pop`, ...) stay private —
   codegen is free to refactor them without it being a contract
   change. The reflection-facing surface — type descriptors, the
   registry, the `reflect` module — is public; every observation
   user code can make through it is a commitment.
2. **CT2. Generated code and `tidert` are version-locked.** A binary
   produced by compiler version *N* must be linked against `tidert`
   built at version *N*. Mismatch is detected eagerly (build-time
   error or forced rebuild) and never produces a runnable binary.
3. **CT3. Reflection ABI is append-only within a compatibility
   window.** New `Kind` variants, new descriptor fields, new
   `reflect` functions may be added without a language-version bump.
   Changing the meaning of an already-exposed field, or dropping a
   field, requires a major / lang-version bump.

**Performance invariants (P1–P3).** These govern how the runtime
interacts with the rest of the language at execution time. The
guiding principle:

> Tide runtime is **opt-in** for dynamic / reflection features and
> **zero-cost or near-zero-cost passive** for statically typed code.

1. **P1. Reflection metadata is passive.** Type descriptors may be
   emitted into the binary, but ordinary field access, method call,
   `match` dispatch, allocation, slice / map / set operations, and
   `Result` / `Option` control flow do not consult descriptors at
   runtime. Descriptors are read only by `reflect.*` function
   bodies.
2. **P2. `Dynamic` is explicit and viral only by spelling.** Implicit
   boxing happens exclusively at parameter sites of `reflect.*`
   functions whose formal type is `Dynamic`. No implicit unboxing
   anywhere — `reflect.unbox<T>` is the only path back to `T`.
   Generic lowering does not route values through `Dynamic` as a
   universal carrier.
3. **P3. `tidert` helpers are monomorphic Go helpers or use Go
   generics directly.** They must not force Tide values through a
   universal `Value`-style representation at runtime. The path from
   Tide source to running code is "Tide → near-direct Go → Go
   optimiser / Go runtime", not "Tide → tidert dispatch / boxing
   → Go".

**Layer split.** `tidert/` is organised into two layers:

- **`tidert/core`** — containers, channel construction, minimal
  helpers. Inlinable. No reflection dependency. Linked into every
  Tide binary that uses these features.
- **`tidert/reflect`** — descriptors, registry, `Dynamic` box,
  pretty-print helpers. Linked **only** when the program
  transitively imports `reflect`. Programs that don't use reflection
  must not ship this layer in their binary.

**Anti-pattern — universal `Value` representation (forbidden).** The
shape `type Value struct { Type *TypeDesc; Data any }` is **not** a
valid lowering target for ordinary Tide values. If every Tide value
lowered through such a type, Go's optimiser would lose visibility
through the entire program. `Value`-shaped representations are
admissible only inside the `Dynamic` boundary — i.e., as the
implementation of `tidert.Dynamic`, never as the lowering of `int`,
`string`, class instances, sum-type values, or any other
statically-typed construct.

**Concrete reviewer signals.** A PR breaks P1–P3 if any of the
following appear outside `tidert/reflect`:

- A new Go struct of the shape `{ *TypeDesc, any }` (or any
  morally equivalent "type descriptor + payload" pair) reachable
  from non-`reflect.*` code paths.
- A descriptor lookup (`registry.Get`, `tidert.DescOf`, etc.) in
  emitted code for ordinary field access, method dispatch, `match`
  arms, or container operations.
- A helper signature with `any` / `interface{}` parameter or
  return type where a Go type parameter would carry the static
  type through. Exception: the `Dynamic` payload field itself.
- A `Map` / `Set` / `Stack` operation that resolves the element
  type at runtime rather than via Go's compile-time generic
  monomorphisation.

A PR exhibiting any of these patterns is presumed to break P1–P3
and must either remove the pattern or justify the breakage
explicitly. The review subagent is briefed to look for these
shapes alongside the existing review checklist.

**Why.** Without naming the line, every future runtime-shaped feature
(serialisation, debug printers, hot reload, deep clone) would relitigate
"does Tide have a runtime?" instead of "what does the runtime do for
this feature?". The threshold was crossed implicitly when `Map`/`Set`/
`Stack` containers landed; reflection makes it observable, so naming it
is overdue.

**What it does not change.** D1 still holds — generated Go is not for
human reading. The runtime exists in Go because the IR is Go; it
remains invisible to the Tide user.

### D19 — Third-party Go dependencies are reserved for UX-only surfaces

**Claim.** The compiler core (lexer, parser, sema, codegen, bindgen)
ships against the Go standard library and project-local `internal/`
packages — nothing else. Third-party Go modules are admissible only
in user-facing UX surfaces (the REPL prompt, future devtool plumbing)
where the alternative is rebuilding a heavy non-language wheel.

The first dep crossed in is `c-bata/go-prompt` (plus its transitive
`mattn/go-tty`, `mattn/go-isatty`, `mattn/go-runewidth`,
`mattn/go-colorable`, `pkg/term`, `golang.org/x/sys`) for the
`tide repl` interactive line editor. Reimplementing arrow-key
history, raw-mode terminal handling, and cursor positioning inside
this repo is out of proportion with the value delivered, hence the
exception.

**Why.** The compiler stays portable, easy to audit, and resistant
to supply-chain drift — anyone reading the spec can read the
implementation without auditing thousands of lines of third-party
Go. UX shells live on a separate budget because terminal handling
is not a Tide-design question; it is a "make the prompt usable"
question.

**Rules of thumb when adding a new dep.**

- Compiler-core PRs that introduce a non-stdlib import default to
  rejection. Justify the exception in the PR; pin a version.
- UX-shell PRs may introduce deps if the alternative is non-trivial
  re-implementation. Prefer libraries with stable APIs and a small
  transitive footprint.
- Generated user code (Tide → Go output) must never depend on a
  third-party Go module. The runtime in `tidert/` and the stdlib
  bindings are the only Go-side surface a Tide program is allowed
  to reach.

**What it does not change.** D6 (bind, don't port the Go stdlib) and
D15 (Go IR contract) still hold for the compiler and the generated
code. D19 only carves out the *toolchain UX shell* as a place where
external libraries are not architecturally forbidden.

---

## What's not here

A handful of decisions and open questions are intentionally out of scope
for this document:

- **Surface-syntax fine print** (e.g. the mutable counterpart to `let`,
  the spawn keyword, the exact newtype syntax) — tracked separately as
  open questions in `docs/language-spec.md` and resolved as the example
  suite forces them.
- **Internal process** (how the spec is paper-validated before any
  compiler exists, how decisions are amended, how tests are layered) —
  development practice, not language design.
- **Open product risks** (cold-start versus npm, the hardest bindings,
  panic policy at the boundary) — known and tracked, but not yet
  decisions.
