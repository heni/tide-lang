# AST schema

Canonical shape of every node in the Tide abstract syntax tree.
One entry per node kind, with fields (typed), required vs optional,
and invariants. Source span is mandatory on every node.

This file is the **data-model contract** between the parser and
everything downstream (sema, codegen). Re-implementations (Tide-in-
Tide self-host) must produce nodes with the same fields and field
names, so that AST-serialisation fixtures (`tests/parser/*.txt`
TOKENS+AST sections per `test-contract.md`) remain stable.

## Conventions

- Every node has a **`span: Span`** field. `Span` is two character-
  counted positions: `start: Pos`, `end: Pos`; `Pos` is `line: int`,
  `col: int`, 1-indexed. The span covers the node from its first
  to its last source character, inclusive on the start, exclusive
  on the end (half-open).
- Field types use spec terminology — `[]Stmt`, `Option<TypeExpr>`,
  `Map<string, Field>`. Not Go types.
- Fields are **required** unless explicitly marked `Option<...>`
  (one slot may be absent) or `[]T` (zero or more — empty list is
  the absence).
- Identifier-token nodes carry only the lexeme string, not the
  raw `Token` — the lexer's bookkeeping does not propagate into
  the AST.

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
| `decl_type` | `Option<TypeExpr>` | optional in short-closure shape; required elsewhere | |

## Type expressions — `TypeExpr` is a sum

```
TypeExpr =
  | PrimitiveType    { name: string }                   // "bool", "int", "string", "unit", ...
  | NamedType        { qname: []string, args: []TypeExpr }
  | TupleType        { components: []TypeExpr }         // components.len() >= 2
  | SliceType        { elem: TypeExpr }
  | FuncType         { params: []TypeExpr, return_type: Option<TypeExpr> }
  | InlineInterface  { methods: []InterfaceMethodSig }
```

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
  | Literal         { kind: LiteralKind, value: ... }
  | ThisExpr
  | ScopeRef
  | Ident           { name: string }
  | ParenExpr       { inner: Expr }
  | TupleLit        { components: []Expr }            // arity >= 2
  | SliceLit        { elem_type: Option<TypeExpr>, items: []Expr }
  | RecordLit       { type_name: NamedType, fields: []FieldInit }
  | MapLit          { type_name: NamedType, entries: []MapEntry }
  | SetLit          { type_name: NamedType, items: []Expr }
  | StackLit        { type_name: NamedType }                       // always empty
  | Block           ... see below
  | IfExpr          { cond: Expr, then_block: Block, else_branch: IfExpr | Block }
  | MatchExpr       { subject: Expr, arms: []MatchArm }
  | ScopeExpr       { type_args: Option<[]TypeExpr>, parent: Option<Expr>, body: Block }
  | TryExpr         { inner: Expr }
  | SpawnExpr       { body: Block }
  | ClosureLit      { params: []Param, return_type: Option<TypeExpr>, body: Block | Expr }
  | UnitLit
  | Call            { callee: Expr, type_args: []TypeExpr, args: []Expr }
  | Index           { receiver: Expr, index: Expr }
  | Slice           { receiver: Expr, low: Option<Expr>, high: Option<Expr> }
  | Field           { receiver: Expr, name: string }
  | TupleField      { receiver: Expr, position: int }
  | Unary           { op: string, operand: Expr }              // "!" or "-"
  | Binary          { op: string, left: Expr, right: Expr }    // "+", "-", "*", ..., "&&", "||"
  | Return          { value: Option<Expr> }
  | Break
  | Continue
```

`LiteralKind` enumerates `Int | Float | String | Rune | Bool`.

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

### `FieldInit`

| Field | Type | Required | Meaning |
|---|---|---|---|
| `name` | `string` | yes | record field name |
| `value` | `Expr` | yes | |

### `MapEntry`

| Field | Type | Required | Meaning |
|---|---|---|---|
| `key` | `Expr` | yes | non-Ident expression — parser-side discriminator |
| `value` | `Expr` | yes | |

### `SelectCase`

| Field | Type | Required | Meaning |
|---|---|---|---|
| `span` | `Span` | yes | |
| `kind` | `SelectKind` | yes | `Recv` / `Send` / `Default` |
| `bind` | `Option<string>` | optional | for `case x = <-ch =>` |
| `channel` | `Option<Expr>` | optional | absent only on `Default` |
| `send_value` | `Option<Expr>` | optional | present only on `Send` |
| `body` | `Block` | yes | |

## Patterns — `Pattern` is a sum

```
Pattern =
  | WildcardPat                                      // _
  | LiteralPat   { kind: LiteralKind, value: ... }
  | UnitPat
  | IdentPat     { name: string }                    // binding
  | TuplePat     { components: []Pattern }
  | VariantPat   { qname: []string, payload: []Pattern }
  | RecordPat    { type_name: NamedType, fields: []RecordPatField }
  | AltPat       { atoms: []Pattern }                // each atom is LiteralPat or VariantPat
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

### `SelectKind`

```
SelectKind = Recv | Send | Default
```

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
