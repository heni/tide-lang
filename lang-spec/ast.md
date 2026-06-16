# AST schema

Canonical shape of every node in the Tide abstract syntax tree.
One entry per node kind, with fields (typed), required vs optional,
and invariants. Source span is mandatory on every node.

**Authority.** This file is the contract. Prose mirror in
`../docs/language-spec.md` may lag; on disagreement this file
wins. The grammar in `grammar.ebnf` produces these nodes 1:1 — any
divergence between grammar productions and AST kinds here is a
bug in one of the two files.

This file is the **data-model contract** between the parser and
everything downstream (sema, codegen). Re-implementations (Tide-in-
Tide self-host) must produce nodes with the same fields and field
names, so that AST-serialisation fixtures (`tests/grammar/*.txt`
TOKENS+AST sections per `test-contract.md`) remain stable.

## Conventions

- Every node has a **`span: Span`** field. `Span` is two character-
  counted positions: `start: Pos`, `end: Pos`; `Pos` is `line: int`,
  `col: int`, 1-indexed. The span covers the node from its first
  to its last source character, inclusive on the start, exclusive
  on the end (half-open). **Nullary variants of a sum (e.g.
  `WildcardPat`, `Break`, `Continue`, `UnitLit`) carry only their
  `span`** — no other fields.
- Field types use spec terminology — `[]Stmt`, `Option<TypeExpr>`.
  Not Go types.
- Fields are **required** unless explicitly marked `Option<...>`
  (one slot may be absent) or `[]T` (zero or more — empty list is
  the absence; no `Option<[]T>` anywhere — empty list *is* "no
  items").
- `_` (discard) is a regular `string` value in `name` fields — no
  sum-typed marker. The discard convention is enforced by sema
  (only some positions accept `"_"`).
- Identifier-token nodes carry only the lexeme string, not the
  raw `Token` — the lexer's bookkeeping does not propagate into
  the AST.

## Span on auxiliary record-shapes

`MatchArm`, `Param`, `Variant`, `FieldDecl`, `ClassField`,
`Method`, `InterfaceMethodSig`, `RecordPatField`, and every
`BraceEntry` / `SelectCase` variant all carry `span`. Anything
that the source file produces as a contiguous chunk has a span.

## Top level

### `File`

| Field | Type | Required | Meaning |
|---|---|---|---|
| `span` | `Span` | yes | from first byte of input to EOF |
| `imports` | `[]Import` | yes (may be empty) | in source order |
| `decls` | `[]Decl` | yes (≥ 1) | every file declares at least one item |

Invariant: a `File` with `decls.len() == 0` is a parse error.

### `Import`

| Field | Type | Required | Meaning |
|---|---|---|---|
| `span` | `Span` | yes | |
| `path` | `string` | yes | dotted/slashed package path as written (e.g. `"encoding/json"`) |

## Declarations — `Decl` is a sum

```
Decl =
  | FuncDecl
  | TypeDecl
  | ClassDecl
  | InterfaceDecl
  | TopLevelLet
  | ExternTypeDecl
  | ExternFuncDecl
  | ExternImplDecl
```

### `FuncDecl`

| Field | Type | Required | Meaning |
|---|---|---|---|
| `span` | `Span` | yes | |
| `name` | `string` | yes | non-empty |
| `type_params` | `[]string` | yes (may be empty) | generic parameter names |
| `params` | `[]Param` | yes (may be empty) | |
| `return_type` | `Option<TypeExpr>` | optional | absent ⇒ returns `unit` |
| `body` | `Block` | yes | |

### `TopLevelLet`

| Field | Type | Required | Meaning |
|---|---|---|---|
| `span` | `Span` | yes | |
| `name` | `string` | yes | |
| `decl_type` | `Option<TypeExpr>` | optional | inferred when absent |
| `value` | `Expr` | yes | initialiser; mandatory at top level |

### `TypeDecl`

| Field | Type | Required | Meaning |
|---|---|---|---|
| `span` | `Span` | yes | |
| `name` | `string` | yes | PascalCase by convention |
| `type_params` | `[]string` | yes (may be empty) | |
| `body` | `TypeBody` | yes | sum of `RecordTypeBody`, `TupleAliasBody`, `SumTypeBody`, `AliasBody` |

`TypeBody` sum:

- `RecordTypeBody { fields: []FieldDecl }`
- `TupleAliasBody { components: []TypeExpr }` (`components.len() >= 2`)
- `SumTypeBody    { variants: []Variant }` (`variants.len() >= 2`)
- `AliasBody      { aliased: TypeExpr }`

### `Variant`

| Field | Type | Required | Meaning |
|---|---|---|---|
| `span` | `Span` | yes | |
| `name` | `string` | yes | |
| `fields` | `[]FieldDecl` | yes (may be empty) | empty = nullary variant |

### `FieldDecl`

| Field | Type | Required | Meaning |
|---|---|---|---|
| `span` | `Span` | yes | |
| `name` | `string` | yes | |
| `decl_type` | `TypeExpr` | yes | |

### `ClassDecl`

| Field | Type | Required | Meaning |
|---|---|---|---|
| `span` | `Span` | yes | |
| `name` | `string` | yes | PascalCase invariant |
| `type_params` | `[]string` | yes (may be empty) | |
| `implements` | `[]TypeExpr` | yes (may be empty) | interface conformance list |
| `fields` | `[]ClassField` | yes (may be empty) | |
| `methods` | `[]Method` | yes (may be empty) | both instance and static — distinguish via `Method.is_static` |

Invariant: `fields` and `methods` appear in declaration order; an
implementation should preserve that order.

### `ClassField`

| Field | Type | Required | Meaning |
|---|---|---|---|
| `span` | `Span` | yes | |
| `name` | `string` | yes | |
| `decl_type` | `TypeExpr` | yes | |
| `mutability` | `Mutability` | yes | `Let` or `Var` |

### `Method`

| Field | Type | Required | Meaning |
|---|---|---|---|
| `span` | `Span` | yes | |
| `name` | `string` | yes | |
| `is_static` | `bool` | yes | |
| `params` | `[]Param` | yes (may be empty) | |
| `return_type` | `Option<TypeExpr>` | optional | absent ⇒ `unit` |
| `body` | `Block` | yes | |

### `InterfaceDecl`

| Field | Type | Required | Meaning |
|---|---|---|---|
| `span` | `Span` | yes | |
| `name` | `string` | yes | |
| `type_params` | `[]string` | yes (may be empty) | |
| `extends` | `[]TypeExpr` | yes (may be empty) | interface composition |
| `methods` | `[]InterfaceMethodSig` | yes (may be empty) | |

### `InterfaceMethodSig`

| Field | Type | Required | Meaning |
|---|---|---|---|
| `span` | `Span` | yes | |
| `name` | `string` | yes | |
| `params` | `[]Param` | yes | |
| `return_type` | `Option<TypeExpr>` | optional | |

### `Param`

| Field | Type | Required | Meaning |
|---|---|---|---|
| `span` | `Span` | yes | |
| `name` | `string` | yes | `"_"` allowed (discard parameter) |
| `decl_type` | `Option<TypeExpr>` | optional in short-closure shape; required elsewhere | for a variadic parameter this is the **element** type `T` (the parameter is in scope as `[]T`) |
| `variadic` | `bool` | yes | `name: ...T` — only the final parameter may be variadic (E0115). See `ffi.md` §Variadic. |

## Foreign bindings — Go FFI (`ffi.md`)

The three `extern` declaration nodes and their member/attribute
shapes. Semantics are in `ffi.md`; this section is the AST contract.

### `GoRef`

The `@go("...")` attribute. `raw` is the string verbatim (quotes
decoded); the package/symbol split is interpreted by sema/codegen
(`ffi.md` §GoRef), not stored here.

| Field | Type | Required | Meaning |
|---|---|---|---|
| `span` | `Span` | yes | spans `@go( … )` |
| `raw` | `string` | yes | referent string with quotes stripped |

### `ExternTypeDecl`

An opaque foreign handle (`extern type T @go("pkg")`).

| Field | Type | Required | Meaning |
|---|---|---|---|
| `span` | `Span` | yes | |
| `name` | `string` | yes | the Tide handle name |
| `go` | `Option<GoRef>` | optional | absent ⇒ symbol defaults to `name` |

### `ExternFuncDecl`

A package-level foreign function. No body — `go` is the binding.

| Field | Type | Required | Meaning |
|---|---|---|---|
| `span` | `Span` | yes | |
| `name` | `string` | yes | |
| `type_params` | `[]string` | yes (may be empty) | |
| `params` | `[]Param` | yes (may be empty) | |
| `return_type` | `Option<TypeExpr>` | optional | absent ⇒ `unit` |
| `go` | `Option<GoRef>` | optional | absent ⇒ symbol defaults to case-converted `name` |

### `ExternImplDecl`

Methods and exported-field accessors on a foreign handle. The Go
import path comes from the named handle's own `ExternTypeDecl`.

| Field | Type | Required | Meaning |
|---|---|---|---|
| `span` | `Span` | yes | |
| `type` | `string` | yes | the extern handle name `T` |
| `methods` | `[]ExternMethod` | yes (may be empty) | |
| `fields` | `[]ExternField` | yes (may be empty) | |

Invariant: `methods` and `fields` appear in declaration order.

### `ExternMethod`

| Field | Type | Required | Meaning |
|---|---|---|---|
| `span` | `Span` | yes | |
| `name` | `string` | yes | |
| `params` | `[]Param` | yes (may be empty) | receiver is implicit |
| `return_type` | `Option<TypeExpr>` | optional | absent ⇒ `unit` |
| `go` | `Option<GoRef>` | optional | bare Go method name; absent ⇒ case-converted `name` |

### `ExternField`

| Field | Type | Required | Meaning |
|---|---|---|---|
| `span` | `Span` | yes | |
| `name` | `string` | yes | |
| `decl_type` | `TypeExpr` | yes | |
| `mutability` | `Mutability` | yes | `Let` (read) or `Var` (read/write) |
| `go` | `Option<GoRef>` | optional | bare Go field name; absent ⇒ case-converted `name` |

## Type expressions — `TypeExpr` is a sum

```
TypeExpr =
  | PrimitiveType    { name: PrimitiveName }
  | NamedType        { qname: []string, args: []TypeExpr }
  | TupleType        { components: []TypeExpr }         // components.len() >= 2
  | SliceType        { elem: TypeExpr }
  | FuncType         { params: []TypeExpr, return_type: Option<TypeExpr> }
  | InlineInterface  { methods: []InterfaceMethodSig }

PrimitiveName =
  | "bool" | "int" | "int8" | "int16" | "int32" | "int64"
  | "uint" | "uint8" | "uint16" | "uint32" | "uint64"
  | "float32" | "float64" | "byte" | "rune" | "string"
  | "unit"
```

`PrimitiveName` is a closed enum — the parser only constructs
`PrimitiveType` when the source token is one of these exact
identifiers. Any other identifier becomes a `NamedType` with
`args.len() == 0`.

`NamedType.qname.len() == 1` for unqualified, `>= 2` for
`Pkg.Type` chains.

## Statements — `Stmt` is a sum

```
Stmt =
  | LetStmt       { pattern: Pattern, decl_type: Option<TypeExpr>, value: Expr }
  | VarStmt       { name: string | "_", decl_type: Option<TypeExpr>, value: Option<Expr> }
  | AssignStmt    { lvalue: Expr, value: Expr }          // sema restricts lvalue
  | IfStmt        { cond: Expr, then_block: Block, else_branch: Option<IfStmt | Block> }
  | ForStmt       { pattern: Pattern, iterable: Iterable, body: Block }
  | WhileStmt     { cond: Expr, body: Block }
  | SelectStmt    { cases: []SelectCase }
  | DeferStmt     { call: Expr }
  | ExprStmt      { expr: Expr }
```

`Iterable` is `RangeExpr | Expr`.

### `RangeExpr`

| Field | Type | Required | Meaning |
|---|---|---|---|
| `span` | `Span` | yes | |
| `low` | `Expr` | yes | |
| `high` | `Expr` | yes | |
| `inclusive` | `bool` | yes | `true` for `..=`, `false` for `..` |

## Expressions — `Expr` is a sum

```
Expr =
  | IntLitExpr      { value: int64 }
  | FloatLitExpr    { value: float64 }
  | StringLitExpr   { value: string }                   // decoded (escapes resolved)
  | RuneLitExpr     { value: int32 }                    // Unicode code point
  | BoolLitExpr     { value: bool }
  | ThisExpr
  | ScopeRef
  | Ident           { name: string }
  | ParenExpr       { inner: Expr }
  | TupleLit        { components: []Expr }              // components.len() >= 2
  | SliceLit        { elem_type: Option<TypeExpr>, items: []Expr }
  | BraceLit        { type_name: NamedType, kind: BraceKind, entries: []BraceEntry }
  | Block           // see Block subsection below
  | IfExpr          { cond: Expr, then_block: Block, else_branch: IfExpr | Block }
  | MatchExpr       { subject: Expr, arms: []MatchArm }     // arms.len() >= 1
  | ScopeExpr       { type_args: []TypeExpr, parent: Option<Expr>, body: Block }
  | TryExpr         { inner: Expr }
  | SpawnExpr       { body: Block }
  | ClosureLit      { params: []Param, return_type: Option<TypeExpr>, body: Block }
  | UnitLit
  | Call            { callee: Expr, type_args: []TypeExpr, args: []Expr }
  | SpreadArg       { inner: Expr }                          // `...e`; only the final call arg (ffi.md §Variadic)
  | Index           { receiver: Expr, index: Expr }
  | Slice           { receiver: Expr, low: Option<Expr>, high: Option<Expr> }
  | Field           { receiver: Expr, name: string }
  | TupleField      { receiver: Expr, position: int }       // position >= 0
  | Unary           { op: UnaryOp, operand: Expr }
  | Binary          { op: BinaryOp, left: Expr, right: Expr }
  | Return          { value: Option<Expr> }
  | Break
  | Continue

UnaryOp  = "!" | "-"
BinaryOp = "+" | "-" | "*" | "/" | "%"
         | "==" | "!=" | "<" | "<=" | ">" | ">="
         | "&&" | "||"
```

Notes:

- The five literal kinds are **separate AST variants**, not one
  `Literal { kind, value: ... }` — value types differ per kind and
  the data-model is cleaner with one variant per shape.
- `ClosureLit.body` is always `Block`. The short closure form
  `(a, b) => expr` is parsed as a `Block` whose `stmts` is empty
  and `trailing` is the expression — no `Block | Expr` sum in one
  field.
- `Return` `Break` `Continue` are **diverging expressions** (Never-
  typed; unify with any expected position) — they replace the
  earlier `ReturnStmt`/`BreakStmt`/`ContinueStmt` and appear at
  statement position as `ExprStmt` wrapping them.

### `BraceLit` — unified record / map / set / stack literal

All four `NamedType "{" ... "}"` literal shapes share one AST
node. The parser commits to a `BraceKind` based on the first
entry's shape; an empty literal stays `Unknown` and is resolved
by sema from `type_name`.

```
BraceKind  = Record | Map | Set | Stack | Unknown
BraceEntry =
  | RecordEntry { span: Span, name: string, value: Expr }
  | MapEntry    { span: Span, key: Expr,    value: Expr }
  | SetEntry    { span: Span, value: Expr }
```

Invariants:

- `kind == Record`  ⇒ every `entries[i]` is a `RecordEntry`.
- `kind == Map`     ⇒ every `entries[i]` is a `MapEntry`.
- `kind == Set`     ⇒ every `entries[i]` is a `SetEntry`.
- `kind == Stack`   ⇒ `entries.len() == 0` (`Stack<T>{}` is
  always empty; non-empty stacks come from `push`).
- `kind == Unknown` ⇒ `entries.len() == 0`; sema lowers to one
  of the four based on the resolved type of `type_name`.

Mixing entry shapes in one literal is a parse error: the parser
commits after the first entry and rejects deviation.

### `Block`

| Field | Type | Required | Meaning |
|---|---|---|---|
| `span` | `Span` | yes | |
| `stmts` | `[]Stmt` | yes (may be empty) | |
| `trailing` | `Option<Expr>` | optional | value of the block; absence ⇒ `unit` |

### `MatchArm`

| Field | Type | Required | Meaning |
|---|---|---|---|
| `span` | `Span` | yes | |
| `pattern` | `Pattern` | yes | |
| `body` | `Expr` | yes | |

### `SelectCase` — sum, not Option-soup

```
SelectCase =
  | SelectRecv    { span: Span, bind: Option<string>, channel: Expr, body: Block }
  | SelectSend    { span: Span, channel: Expr, value: Expr, body: Block }
  | SelectDefault { span: Span, body: Block }
```

The cross-field invariants from the earlier Option-typed shape
("`channel` absent iff `Default`", "`send_value` present iff
`Send`") become impossible-by-construction.

## Patterns — `Pattern` is a sum

```
Pattern =
  | WildcardPat                                      // _
  | IntLitPat    { value: int64 }
  | StringLitPat { value: string }
  | RuneLitPat   { value: int32 }
  | BoolLitPat   { value: bool }
  | FloatLitPat  { value: float64 }                  // grammar-legal; sema rejects (float == on patterns is unsafe)
  | UnitPat
  | IdentPat     { name: string }                    // binding
  | TuplePat     { components: []Pattern }
  | VariantPat   { qname: []string, payload: []Pattern }
  | RecordPat    { qname: []string, fields: []RecordPatField }   // qname: the record type name (mirrors VariantPat); v1 omits type-args and punning
  | AltPat       { atoms: []Pattern }                // atoms.len() >= 2; each atom is a literal-pattern variant or a VariantPat
```

### `RecordPatField`

| Field | Type | Required | Meaning |
|---|---|---|---|
| `name` | `string` | yes | record field name |
| `pattern` | `Pattern` | yes | |

## Miscellaneous types

### `Mutability`

```
Mutability = Let | Var
```

Used on `ClassField` and `VarStmt` (`Var`) / `LetStmt` (`Let`).

### `SelectKind` (removed — see `SelectCase` sum above)

`SelectCase` is now itself a sum (`SelectRecv` / `SelectSend` /
`SelectDefault`); no separate `SelectKind` enum is needed.

## Invariants checked at sema (not at the AST level)

- A `VariantPat` whose `qname` resolves to a class / record / non-
  variant is a sema error, not a parse error.
- An `IdentPat` whose name collides with a known variant in scope
  is a warning (potential shadow of a variant constructor).
- An `AssignStmt.lvalue` that is not a valid write target (the
  PostfixExpr restriction) is a sema error.
- A `RecordLit` missing a required field is a sema error.
- A `MatchExpr` missing an arm (non-exhaustive) is a sema error.

These are written to `lang-spec/type-system.md` and
`lang-spec/diagnostics.md` (both forthcoming).
