# Tide

**Familiar TypeScript-style syntax, free of JavaScript's legacy, on the Go
runtime.**

## The idea

Tide is a modern, statically typed language for TypeScript developers. It keeps
the syntax they already know — productive from day one — then drops the
JavaScript legacy (`prototype`, `this`, coercions, `any`, decorators,
`Promise`, npm) and puts the language on the Go runtime: goroutine scheduler,
garbage collector, single-binary deployment, fast startup, the standard
library.

Two things make it more than a reskin. The type system is genuinely more
capable — sum types, exhaustive matching, `Option`/`Result`, no `any` (an
ML-family type system underneath). And error handling drops `if err != nil`:
`let x = try foo()` over Go's error model. People love the Go runtime and
tolerate Go's errors — Tide keeps the first and removes the second.

Tide is **not** a TypeScript-to-Go transpiler: no npm, no JavaScript semantics,
no browser ecosystem — and that is deliberate.

> Status: **pre-alpha**. This repository is a scaffold; nothing compiles Tide
> source yet.

## A taste

```td
import http

type User = {
  id: string
  name: string
}

func getUser(id: string): Result<User, error> {
  let resp = try http.get("/users/" + id)
  return Ok(User{ id, name: resp.body })
}
```

## Building the compiler

The Tide compiler is itself written in Go.

```sh
go build ./cmd/tide
./tide version
```

Source files use the `.td` extension.

## Read more

| To understand... | Read |
|---|---|
| The architectural commitments and why Tide is the way it is | [`docs/design-decisions.md`](docs/design-decisions.md) |
| How the compiler is built — pipeline, bindings, concurrency, testing | [`docs/architecture.md`](docs/architecture.md) |
| The language surface (working draft) | [`docs/language-spec.md`](docs/language-spec.md) |
| What v1 must be able to do — the acceptance suite | [`examples/README.md`](examples/README.md) |
