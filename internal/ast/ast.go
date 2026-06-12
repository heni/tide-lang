package ast

// Span is a half-open character-counted source range, line:col-line:col.
// Char-counted (not byte-counted), 1-indexed, matching the lexer's
// position format.
type Span struct {
	StartLine, StartCol int
	EndLine, EndCol     int
}

// Node is the root interface implemented by every AST node.
// Implementations expose their span and a canonical name (used by
// the S-expression serializer in serialise.go).
type Node interface {
	NodeSpan() Span
	NodeKind() string
}

// ---------------------------------------------------------------
// Top level
// ---------------------------------------------------------------

// File is the root of a single .td source file.
type File struct {
	Span    Span
	Imports []*Import
	Decls   []Decl
}

func (n *File) NodeSpan() Span   { return n.Span }
func (n *File) NodeKind() string { return "File" }

// Import is a single `import <path>` line.
type Import struct {
	Span Span
	Path string // as written; dots and slashes are preserved
}

func (n *Import) NodeSpan() Span   { return n.Span }
func (n *Import) NodeKind() string { return "Import" }

// Decl is the sum of top-level declaration kinds. PR-B only
// implements FuncDecl; later PRs add TypeDecl / ClassDecl /
// InterfaceDecl / TopLevelLet.
type Decl interface {
	Node
	declMarker()
}

// TypeDecl — `type Name = TypeBody`. TypeBody is the sum of
// AliasBody, TupleAliasBody, RecordTypeBody, SumTypeBody.
type TypeDecl struct {
	Span       Span
	Name       string
	TypeParams []string // `type Pair<T, U> = …`; nil when non-generic
	Body       TypeBody
}

func (n *TypeDecl) NodeSpan() Span   { return n.Span }
func (n *TypeDecl) NodeKind() string { return "TypeDecl" }
func (n *TypeDecl) declMarker()      {}

// TypeBody is the sum of type-declaration bodies.
type TypeBody interface {
	Node
	typeBodyMarker()
}

// AliasBody — `type T = OtherType`.
type AliasBody struct {
	Span    Span
	Aliased TypeExpr
}

func (n *AliasBody) NodeSpan() Span   { return n.Span }
func (n *AliasBody) NodeKind() string { return "AliasBody" }
func (n *AliasBody) typeBodyMarker()  {}

// SumTypeBody — `type T = | V1 | V2(...) | ...`. PR-F2 admits
// only nullary variants (no payload); payload variants land
// with PR-F3 (Result/Option) or earlier as the corpus demands.
type SumTypeBody struct {
	Span     Span
	Variants []*Variant
}

func (n *SumTypeBody) NodeSpan() Span   { return n.Span }
func (n *SumTypeBody) NodeKind() string { return "SumTypeBody" }
func (n *SumTypeBody) typeBodyMarker()  {}

// RecordTypeBody — `type T = { f1: T1, f2: T2, ... }`. Nominal
// record (D14): the declared name is its identity. All fields are
// required in v1.
type RecordTypeBody struct {
	Span   Span
	Fields []*FieldDecl
}

func (n *RecordTypeBody) NodeSpan() Span   { return n.Span }
func (n *RecordTypeBody) NodeKind() string { return "RecordTypeBody" }
func (n *RecordTypeBody) typeBodyMarker()  {}

// Variant is one constructor of a sum type. Fields empty
// ⇒ nullary; non-empty ⇒ tagged-payload variant.
type Variant struct {
	Span   Span
	Name   string
	Fields []*FieldDecl
}

func (n *Variant) NodeSpan() Span   { return n.Span }
func (n *Variant) NodeKind() string { return "Variant" }

// FieldDecl is a named field with a declared type. Used by
// Variant payload, RecordTypeBody, and ClassDecl fields.
type FieldDecl struct {
	Span     Span
	Name     string
	DeclType TypeExpr
}

func (n *FieldDecl) NodeSpan() Span   { return n.Span }
func (n *FieldDecl) NodeKind() string { return "FieldDecl" }

// ClassDecl — `class Name<TypeParams> { fields, methods }`.
// PR-F4 admitted only the non-generic, non-implements shape;
// PR-G1 lifted the generic restriction (TypeParams now populated
// by the parser for `class Name<T, U>` decls). `implements` is
// still rejected at parse time and lands with the interface PR;
// the Implements slot stays empty per ast.md §ClassDecl so the
// struct shape and canonical serialisation are stable.
type ClassDecl struct {
	Span       Span
	Name       string
	TypeParams []string   // empty for PR-F4; populated by generics PR
	Implements []TypeExpr // empty for PR-F4; populated by interfaces PR
	Fields     []*ClassField
	Methods    []*Method
}

func (n *ClassDecl) NodeSpan() Span   { return n.Span }
func (n *ClassDecl) NodeKind() string { return "ClassDecl" }
func (n *ClassDecl) declMarker()      {}

// ClassField is one field of a ClassDecl. Mutability is
// "Let" or "Var" matching ast.md §Mutability.
type ClassField struct {
	Span       Span
	Name       string
	DeclType   TypeExpr
	Mutability string // "Let" or "Var"
}

func (n *ClassField) NodeSpan() Span   { return n.Span }
func (n *ClassField) NodeKind() string { return "ClassField" }

// InterfaceDecl — `interface Name<T> extends ... { method sigs }`.
// Per ast.md §InterfaceDecl. Nominal conformance (D14): a class
// states `implements Name` explicitly.
type InterfaceDecl struct {
	Span       Span
	Name       string
	TypeParams []string
	Extends    []TypeExpr // composed interfaces
	Methods    []*InterfaceMethodSig
}

func (n *InterfaceDecl) NodeSpan() Span   { return n.Span }
func (n *InterfaceDecl) NodeKind() string { return "InterfaceDecl" }
func (n *InterfaceDecl) declMarker()      {}

// TopLevelLet — module-level constant `let Name [: T] = Value`. Per
// ast.md §TopLevelLet the initialiser is mandatory and `var` is not
// legal at the top level (grammar.ebnf §TopLevelLet — module-scope
// mutable state requires a singleton class). The binding is visible
// everywhere in the file (name-resolution.md §File scope).
type TopLevelLet struct {
	Span     Span
	Name     string
	DeclType TypeExpr // nil ⇒ inferred from Value
	Value    Expr
}

func (n *TopLevelLet) NodeSpan() Span   { return n.Span }
func (n *TopLevelLet) NodeKind() string { return "TopLevelLet" }
func (n *TopLevelLet) declMarker()      {}

// InterfaceMethodSig — `name(params): R` inside an interface body.
type InterfaceMethodSig struct {
	Span       Span
	Name       string
	Params     []*Param
	ReturnType TypeExpr // nil ⇒ unit
}

func (n *InterfaceMethodSig) NodeSpan() Span   { return n.Span }
func (n *InterfaceMethodSig) NodeKind() string { return "InterfaceMethodSig" }

// Method is one method of a ClassDecl. IsStatic distinguishes
// `static foo(): T { ... }` from `foo(): T { ... }`.
type Method struct {
	Span       Span
	Name       string
	IsStatic   bool
	Params     []*Param
	ReturnType TypeExpr // nil ⇒ unit
	Body       *Block
}

func (n *Method) NodeSpan() Span   { return n.Span }
func (n *Method) NodeKind() string { return "Method" }

// FuncDecl is a top-level function. PR-F1 covered the
// non-generic shape; PR-G1 adds TypeParams (empty when the
// function is non-generic). Each entry is a type-parameter
// name; constraints are all `any` in PR-G1 (per the agreed
// minimal-generics scope), with comparable-propagation for
// container key positions added later by codegen if needed.
type FuncDecl struct {
	Span       Span
	Name       string
	TypeParams []string // empty for non-generic
	Params     []*Param
	ReturnType TypeExpr // nil ⇒ unit
	Body       *Block
}

func (n *FuncDecl) NodeSpan() Span   { return n.Span }
func (n *FuncDecl) NodeKind() string { return "FuncDecl" }
func (n *FuncDecl) declMarker()      {}

// Param is one parameter of a FuncDecl. DeclType is required at
// FuncDecl position (closures may omit it; not parsed in PR-F1).
type Param struct {
	Span     Span
	Name     string // "_" allowed
	DeclType TypeExpr
}

func (n *Param) NodeSpan() Span   { return n.Span }
func (n *Param) NodeKind() string { return "Param" }

// ---------------------------------------------------------------
// Type expressions
// ---------------------------------------------------------------

// TypeExpr is the sum of type-expression kinds. PR-F1 emits
// PrimitiveType (for the closed primitive-name set per
// ast.md PrimitiveName) and NamedType (everything else).
// SliceType / TupleType / FuncType / InlineInterface land
// with later PRs.
type TypeExpr interface {
	Node
	typeMarker()
}

// PrimitiveType is one of the closed PrimitiveName tokens per
// ast.md §Type expressions:
//
//	bool int int8..int64 uint..uint64 float32 float64
//	byte rune string unit
//
// The parser commits to PrimitiveType only for exact matches; any
// other identifier becomes a NamedType with Args.len() == 0.
type PrimitiveType struct {
	Span Span
	Name string
}

func (n *PrimitiveType) NodeSpan() Span   { return n.Span }
func (n *PrimitiveType) NodeKind() string { return "PrimitiveType" }
func (n *PrimitiveType) typeMarker()      {}

// PrimitiveNames is the closed set of primitive type names per
// ast.md §PrimitiveName.
var PrimitiveNames = map[string]bool{
	"bool": true,
	"int":  true,
	"int8": true, "int16": true, "int32": true, "int64": true,
	"uint":  true,
	"uint8": true, "uint16": true, "uint32": true, "uint64": true,
	"float32": true, "float64": true,
	"byte":   true,
	"rune":   true,
	"string": true,
	"unit":   true,
}

// SliceType — `[]T`. Per ast.md §TypeExpr.
type SliceType struct {
	Span Span
	Elem TypeExpr
}

func (n *SliceType) NodeSpan() Span   { return n.Span }
func (n *SliceType) NodeKind() string { return "SliceType" }
func (n *SliceType) typeMarker()      {}

// TupleType — `(A, B, ...)`. Per ast.md §TypeExpr; arity ≥ 2.
type TupleType struct {
	Span       Span
	Components []TypeExpr
}

func (n *TupleType) NodeSpan() Span   { return n.Span }
func (n *TupleType) NodeKind() string { return "TupleType" }
func (n *TupleType) typeMarker()      {}

// FuncType — `func(A, B) : R`. Per ast.md §TypeExpr; the type of a
// closure / function value.
type FuncType struct {
	Span       Span
	Params     []TypeExpr
	ReturnType TypeExpr // nil ⇒ unit
}

func (n *FuncType) NodeSpan() Span   { return n.Span }
func (n *FuncType) NodeKind() string { return "FuncType" }
func (n *FuncType) typeMarker()      {}

// NamedType is a possibly-qualified identifier (`Result`,
// `Map<K, V>`, `fmt.Foo`). QName has length ≥ 1. Args is empty
// when the type carries no generic arguments.
type NamedType struct {
	Span  Span
	QName []string
	Args  []TypeExpr
}

func (n *NamedType) NodeSpan() Span   { return n.Span }
func (n *NamedType) NodeKind() string { return "NamedType" }
func (n *NamedType) typeMarker()      {}

// ---------------------------------------------------------------
// Statements
// ---------------------------------------------------------------

// Stmt is the sum of statement kinds.
type Stmt interface {
	Node
	stmtMarker()
}

// ExprStmt wraps an expression at statement position.
type ExprStmt struct {
	Span Span
	Expr Expr
}

func (n *ExprStmt) NodeSpan() Span   { return n.Span }
func (n *ExprStmt) NodeKind() string { return "ExprStmt" }
func (n *ExprStmt) stmtMarker()      {}

// LetStmt — `let <pattern> [: T] = value`. Per ast.md the
// left-hand side is a Pattern (IdentPat for the common case,
// TuplePat / RecordPat / VariantPat for destructure forms in
// later PRs). Immutable binding.
type LetStmt struct {
	Span     Span
	Pattern  Pattern
	DeclType TypeExpr
	Value    Expr
}

func (n *LetStmt) NodeSpan() Span   { return n.Span }
func (n *LetStmt) NodeKind() string { return "LetStmt" }
func (n *LetStmt) stmtMarker()      {}

// VarStmt — `var name [: T] [= value]`. Mutable binding. Per
// ast.md `value: Option<Expr>` — initialiser optional at the
// AST level. The sema decision about whether bare
// `var x: T` is admitted (G1 says no) is handled by sema, not
// the parser.
type VarStmt struct {
	Span     Span
	Name     string
	DeclType TypeExpr
	Value    Expr // nil ⇒ uninitialised (sema E0202 in v1)
}

func (n *VarStmt) NodeSpan() Span   { return n.Span }
func (n *VarStmt) NodeKind() string { return "VarStmt" }
func (n *VarStmt) stmtMarker()      {}

// AssignStmt — `lvalue = value`. Value-position to value-position
// assignment. Sema restricts which expressions are valid lvalues;
// the parser accepts any expression on the left and defers.
type AssignStmt struct {
	Span   Span
	LValue Expr
	Value  Expr
}

func (n *AssignStmt) NodeSpan() Span   { return n.Span }
func (n *AssignStmt) NodeKind() string { return "AssignStmt" }
func (n *AssignStmt) stmtMarker()      {}

// (Return is an Expr — see ReturnExpr below. The parser wraps
// it in an ExprStmt when it appears in statement position.)

// IfStmt — statement form. else_branch is nil, a nested *IfStmt
// (for "else if"), or a *Block (for plain "else { }").
type IfStmt struct {
	Span      Span
	Cond      Expr
	ThenBlock *Block
	Else      Node // nil | *IfStmt | *Block
}

func (n *IfStmt) NodeSpan() Span   { return n.Span }
func (n *IfStmt) NodeKind() string { return "IfStmt" }
func (n *IfStmt) stmtMarker()      {}

// ForStmt — for <pattern> in <iterable> { body }.
type ForStmt struct {
	Span     Span
	Pattern  Pattern
	Iterable Iterable
	Body     *Block
}

func (n *ForStmt) NodeSpan() Span   { return n.Span }
func (n *ForStmt) NodeKind() string { return "ForStmt" }
func (n *ForStmt) stmtMarker()      {}

// WhileStmt — `while <cond> { body }`. Per ast.md §Stmt.
type WhileStmt struct {
	Span Span
	Cond Expr
	Body *Block
}

func (n *WhileStmt) NodeSpan() Span   { return n.Span }
func (n *WhileStmt) NodeKind() string { return "WhileStmt" }
func (n *WhileStmt) stmtMarker()      {}

// SelectStmt — `select { case … => block, … }`. Waits on multiple
// channel operations, running the first ready case's body (T-Select
// : unit). Per ast.md §Stmt (`SelectStmt { cases }`).
type SelectStmt struct {
	Span  Span
	Cases []SelectCase
}

func (n *SelectStmt) NodeSpan() Span   { return n.Span }
func (n *SelectStmt) NodeKind() string { return "SelectStmt" }
func (n *SelectStmt) stmtMarker()      {}

// SelectCase is one arm of a `select` (ast.md §SelectCase):
// SelectRecv | SelectSend | SelectDefault.
type SelectCase interface {
	Node
	selectCaseMarker()
}

// SelectRecv — `case (x =)? <-ch => block`. Bind is "" when the
// received value is dropped.
type SelectRecv struct {
	Span    Span
	Bind    string
	Channel Expr
	Body    *Block
}

func (n *SelectRecv) NodeSpan() Span    { return n.Span }
func (n *SelectRecv) NodeKind() string  { return "SelectRecv" }
func (n *SelectRecv) selectCaseMarker() {}

// SelectSend — `case ch.send(v) => block`.
type SelectSend struct {
	Span    Span
	Channel Expr
	Value   Expr
	Body    *Block
}

func (n *SelectSend) NodeSpan() Span    { return n.Span }
func (n *SelectSend) NodeKind() string  { return "SelectSend" }
func (n *SelectSend) selectCaseMarker() {}

// SelectDefault — `default => block` (non-blocking fall-through).
type SelectDefault struct {
	Span Span
	Body *Block
}

func (n *SelectDefault) NodeSpan() Span    { return n.Span }
func (n *SelectDefault) NodeKind() string  { return "SelectDefault" }
func (n *SelectDefault) selectCaseMarker() {}

// DeferStmt — `defer <call>`. Per ast.md §Stmt (`DeferStmt { call:
// Expr }`) and grammar production `DeferStmt = "defer" Expr`.
// `defer` is adopted from Go directly (G27): function-scoped, LIFO.
// Sema requires Call shape (T-Defer / E0406); codegen lowers it to
// a Go `defer` statement (lowering-go.md §Defer).
type DeferStmt struct {
	Span Span
	Call Expr
}

func (n *DeferStmt) NodeSpan() Span   { return n.Span }
func (n *DeferStmt) NodeKind() string { return "DeferStmt" }
func (n *DeferStmt) stmtMarker()      {}

// Iterable marks the type of values legal at the right-hand side
// of `for x in ...`. Per ast.md §Iterable, the cases are
// `RangeExpr | Expr`; both *RangeExpr and any concrete Expr type
// satisfy this interface because they all implement Node.
type Iterable interface {
	Node
}

// ---------------------------------------------------------------
// Block
// ---------------------------------------------------------------

// Block carries a sequence of statements and an optional trailing
// expression giving the block its value.
type Block struct {
	Span     Span
	Stmts    []Stmt
	Trailing Expr // nil ⇒ block evaluates to unit
}

func (n *Block) NodeSpan() Span   { return n.Span }
func (n *Block) NodeKind() string { return "Block" }

// Block is also an expression (PrimaryExpr → Block, grammar.ebnf):
// its value is `trailing`, or unit when absent.
func (n *Block) exprMarker() {}

// ---------------------------------------------------------------
// Patterns
// ---------------------------------------------------------------

// Pattern is the sum of pattern kinds (ast.md §Pattern). RecordPat
// is the only one still unparsed; the rest — wildcard, the literal
// pats, tuple, variant, alt — are produced by the parser.
type Pattern interface {
	Node
	patternMarker()
}

// IdentPat introduces a new binding named Name.
type IdentPat struct {
	Span Span
	Name string
}

func (n *IdentPat) NodeSpan() Span   { return n.Span }
func (n *IdentPat) NodeKind() string { return "IdentPat" }
func (n *IdentPat) patternMarker()   {}

// WildcardPat — `_`. Matches anything, binds nothing.
type WildcardPat struct {
	Span Span
}

func (n *WildcardPat) NodeSpan() Span   { return n.Span }
func (n *WildcardPat) NodeKind() string { return "WildcardPat" }
func (n *WildcardPat) patternMarker()   {}

// TuplePat — `(p1, p2, ...)`. Per ast.md §Pattern; arity ≥ 2.
// Destructures a tuple, binding each sub-pattern to a component.
type TuplePat struct {
	Span Span
	Sub  []Pattern
}

func (n *TuplePat) NodeSpan() Span   { return n.Span }
func (n *TuplePat) NodeKind() string { return "TuplePat" }
func (n *TuplePat) patternMarker()   {}

// IntLitPat — match against a literal integer.
type IntLitPat struct {
	Span    Span
	RawText string
	Value   int64
}

func (n *IntLitPat) NodeSpan() Span   { return n.Span }
func (n *IntLitPat) NodeKind() string { return "IntLitPat" }
func (n *IntLitPat) patternMarker()   {}

// FloatLitPat — `3.14` in pattern position. Grammar-legal but sema
// rejects it (E0305): float equality on patterns is unsafe.
type FloatLitPat struct {
	Span    Span
	RawText string
	Value   float64
}

func (n *FloatLitPat) NodeSpan() Span   { return n.Span }
func (n *FloatLitPat) NodeKind() string { return "FloatLitPat" }
func (n *FloatLitPat) patternMarker()   {}

// StringLitPat — match against a literal string.
type StringLitPat struct {
	Span  Span
	Value string
}

func (n *StringLitPat) NodeSpan() Span   { return n.Span }
func (n *StringLitPat) NodeKind() string { return "StringLitPat" }
func (n *StringLitPat) patternMarker()   {}

// BoolLitPat — match against `true` or `false`.
type BoolLitPat struct {
	Span  Span
	Value bool
}

func (n *BoolLitPat) NodeSpan() Span   { return n.Span }
func (n *BoolLitPat) NodeKind() string { return "BoolLitPat" }
func (n *BoolLitPat) patternMarker()   {}

// RuneLitPat — match against a literal code point (`'a'`). RawText
// preserves the source spelling so codegen re-emits the same Go
// rune literal (a `rune` is `int32`); Value is the decoded point.
type RuneLitPat struct {
	Span    Span
	RawText string
	Value   int32
}

func (n *RuneLitPat) NodeSpan() Span   { return n.Span }
func (n *RuneLitPat) NodeKind() string { return "RuneLitPat" }
func (n *RuneLitPat) patternMarker()   {}

// VariantPat — `V` (nullary), `V(sub1, sub2)` (with payload), or
// `Type.V` (qualified). QName has length ≥ 1; the last segment
// is the variant name, earlier segments are the qualifying
// type / module path.
type VariantPat struct {
	Span  Span
	QName []string
	Sub   []Pattern
}

func (n *VariantPat) NodeSpan() Span   { return n.Span }
func (n *VariantPat) NodeKind() string { return "VariantPat" }
func (n *VariantPat) patternMarker()   {}

// AltPat — `a | b | c`. Matches when any atom matches. Per
// grammar.ebnf §AltPat each atom is a literal pattern or a nullary
// VariantPat (`'(' | '[' | '{'`, `Up | Left`); none bind variables,
// so all atoms agree on the empty binding set (type-system.md
// §P-Alt). Atoms has length ≥ 2.
type AltPat struct {
	Span  Span
	Atoms []Pattern
}

func (n *AltPat) NodeSpan() Span   { return n.Span }
func (n *AltPat) NodeKind() string { return "AltPat" }
func (n *AltPat) patternMarker()   {}

// ---------------------------------------------------------------
// Expressions
// ---------------------------------------------------------------

// Expr is the sum of expression kinds.
type Expr interface {
	Node
	exprMarker()
}

// IntLitExpr is an integer literal. RawText preserves the source
// text (with `_` separators) for round-tripping; Value is the
// resolved int64.
type IntLitExpr struct {
	Span    Span
	RawText string
	Value   int64
}

func (n *IntLitExpr) NodeSpan() Span   { return n.Span }
func (n *IntLitExpr) NodeKind() string { return "IntLitExpr" }
func (n *IntLitExpr) exprMarker()      {}

// FloatLitExpr — `3.14`, `1e3`. Per ast.md §Expr; types to float64
// (T-FloatLit).
type FloatLitExpr struct {
	Span    Span
	RawText string
	Value   float64
}

func (n *FloatLitExpr) NodeSpan() Span   { return n.Span }
func (n *FloatLitExpr) NodeKind() string { return "FloatLitExpr" }
func (n *FloatLitExpr) exprMarker()      {}

// StringLitExpr is a string literal. Value is the decoded string
// (escapes resolved); RawText is the source text with quotes.
type StringLitExpr struct {
	Span    Span
	RawText string
	Value   string
}

func (n *StringLitExpr) NodeSpan() Span   { return n.Span }
func (n *StringLitExpr) NodeKind() string { return "StringLitExpr" }
func (n *StringLitExpr) exprMarker()      {}

// BoolLitExpr is the literal `true` or `false`.
type BoolLitExpr struct {
	Span  Span
	Value bool
}

func (n *BoolLitExpr) NodeSpan() Span   { return n.Span }
func (n *BoolLitExpr) NodeKind() string { return "BoolLitExpr" }
func (n *BoolLitExpr) exprMarker()      {}

// UnitLit is the unit value `()` — the single inhabitant of the
// `unit` type. Per ast.md §Expr (a leaf node carrying only its
// span) and grammar production `UnitLit = "(" ")"`. Lowers to Go's
// zero-byte value (lowering-go.md §Primitive type lowering).
type UnitLit struct {
	Span Span
}

func (n *UnitLit) NodeSpan() Span   { return n.Span }
func (n *UnitLit) NodeKind() string { return "UnitLit" }
func (n *UnitLit) exprMarker()      {}

// RuneLitExpr is a single-quoted code-point literal (`'a'`,
// `'\n'`). Lowers to Go's identical syntax — Tide's `rune` is
// Go's `rune` (alias for `int32`).
type RuneLitExpr struct {
	Span    Span
	RawText string // source text including the surrounding quotes
	Value   int32  // decoded code point
}

func (n *RuneLitExpr) NodeSpan() Span   { return n.Span }
func (n *RuneLitExpr) NodeKind() string { return "RuneLitExpr" }
func (n *RuneLitExpr) exprMarker()      {}

// ThisExpr — `this` inside an instance method. Sema attaches
// the enclosing class type; codegen lowers to the Go receiver
// name (`t`, per lowering-go.md §Implicit receiver).
type ThisExpr struct {
	Span Span
}

func (n *ThisExpr) NodeSpan() Span   { return n.Span }
func (n *ThisExpr) NodeKind() string { return "ThisExpr" }
func (n *ThisExpr) exprMarker()      {}

// Ident references a name in scope.
type Ident struct {
	Span Span
	Name string
}

func (n *Ident) NodeSpan() Span   { return n.Span }
func (n *Ident) NodeKind() string { return "Ident" }
func (n *Ident) exprMarker()      {}

// Call is an application: `callee(args)` or
// `callee<T1, T2>(args)`. TypeArgs is empty when the call has
// no explicit type arguments (the common case — Go's own
// inference picks them from arg types in most positions).
// PR-G2 populates TypeArgs from `<...>` parsed in expression
// position; PR-G1 left this slot empty.
type Call struct {
	Span     Span
	Callee   Expr
	TypeArgs []TypeExpr
	Args     []Expr
}

func (n *Call) NodeSpan() Span   { return n.Span }
func (n *Call) NodeKind() string { return "Call" }
func (n *Call) exprMarker()      {}

// Field is member access: receiver.name.
type Field struct {
	Span     Span
	Receiver Expr
	Name     string
}

func (n *Field) NodeSpan() Span   { return n.Span }
func (n *Field) NodeKind() string { return "Field" }
func (n *Field) exprMarker()      {}

// Binary is a binary operation.
type Binary struct {
	Span        Span
	Op          string // "+" "-" "*" "/" "%" "==" "!=" "<" "<=" ">" ">=" "&&" "||"
	Left, Right Expr
}

func (n *Binary) NodeSpan() Span   { return n.Span }
func (n *Binary) NodeKind() string { return "Binary" }
func (n *Binary) exprMarker()      {}

// Unary is a unary operation. PR-B does not parse unary forms
// other than negative literals (handled in the lexer / parser).
type Unary struct {
	Span    Span
	Op      string // "!" "-"
	Operand Expr
}

func (n *Unary) NodeSpan() Span   { return n.Span }
func (n *Unary) NodeKind() string { return "Unary" }
func (n *Unary) exprMarker()      {}

// SliceLit — `[e_1, ..., e_n]` (inferred element type) or
// `[]T{e_1, ..., e_n}` (annotated). ElemType is nil for the
// inferred form; Items may be empty when ElemType is set.
type SliceLit struct {
	Span     Span
	ElemType TypeExpr // nil ⇒ inferred from items
	Items    []Expr
}

func (n *SliceLit) NodeSpan() Span   { return n.Span }
func (n *SliceLit) NodeKind() string { return "SliceLit" }
func (n *SliceLit) exprMarker()      {}

// Index — `receiver[i]`.
type Index struct {
	Span     Span
	Receiver Expr
	Idx      Expr
}

func (n *Index) NodeSpan() Span   { return n.Span }
func (n *Index) NodeKind() string { return "Index" }
func (n *Index) exprMarker()      {}

// Slice — `receiver[low:high]`. Low / High may be nil for
// `[:hi]` or `[lo:]` forms. Spelling matches ast.md:269.
type Slice struct {
	Span     Span
	Receiver Expr
	Low      Expr
	High     Expr
}

func (n *Slice) NodeSpan() Span   { return n.Span }
func (n *Slice) NodeKind() string { return "Slice" }
func (n *Slice) exprMarker()      {}

// IfExpr — `if cond { ... } else { ... }` in value position. Per
// ast.md §Expr the value form requires both arms (else non-nil);
// that rule is enforced at lowering today (codegen rejects an
// else-less value `if`), with the Barrier-D diagnostic a follow-up.
type IfExpr struct {
	Span      Span
	Cond      Expr
	ThenBlock *Block
	Else      Node // nil | *IfExpr (else-if) | *Block
}

func (n *IfExpr) NodeSpan() Span   { return n.Span }
func (n *IfExpr) NodeKind() string { return "IfExpr" }
func (n *IfExpr) exprMarker()      {}

// MatchExpr — `match subject { pat1 => body1, pat2 => body2 }`.
// Arms count ≥ 1 (parser enforces).
type MatchExpr struct {
	Span    Span
	Subject Expr
	Arms    []*MatchArm
}

func (n *MatchExpr) NodeSpan() Span   { return n.Span }
func (n *MatchExpr) NodeKind() string { return "MatchExpr" }
func (n *MatchExpr) exprMarker()      {}

// MatchArm — `pattern => body`.
type MatchArm struct {
	Span    Span
	Pattern Pattern
	Body    Expr
}

func (n *MatchArm) NodeSpan() Span   { return n.Span }
func (n *MatchArm) NodeKind() string { return "MatchArm" }

// ReturnExpr — `return` (Value nil) or `return expr`. Per ast.md
// §Expr, Return is a DivergingExpr — it has type Never and may
// appear anywhere an Expr is expected, including statement
// position via ExprStmt-wrapping.
type ReturnExpr struct {
	Span  Span
	Value Expr // nil ⇒ bare return
}

func (n *ReturnExpr) NodeSpan() Span   { return n.Span }
func (n *ReturnExpr) NodeKind() string { return "ReturnExpr" }
func (n *ReturnExpr) exprMarker()      {}

// BreakExpr — `break`. A DivergingExpr (Never-typed) per ast.md
// §Expr; legal only inside a loop body (sema E0404). Appears at
// statement position wrapped in an ExprStmt.
type BreakExpr struct {
	Span Span
}

func (n *BreakExpr) NodeSpan() Span   { return n.Span }
func (n *BreakExpr) NodeKind() string { return "BreakExpr" }
func (n *BreakExpr) exprMarker()      {}

// ContinueExpr — `continue`. A DivergingExpr (Never-typed) per
// ast.md §Expr; legal only inside a loop body (sema E0404).
type ContinueExpr struct {
	Span Span
}

func (n *ContinueExpr) NodeSpan() Span   { return n.Span }
func (n *ContinueExpr) NodeKind() string { return "ContinueExpr" }
func (n *ContinueExpr) exprMarker()      {}

// BraceKind classifies a BraceLit, committed by the parser on the
// first entry (or left Unknown for an empty `Type {}`, resolved by
// sema from the type name). Per ast.md §BraceLit.
type BraceKind string

const (
	BraceUnknown BraceKind = "Unknown"
	BraceRecord  BraceKind = "Record"
	BraceMap     BraceKind = "Map"
	BraceSet     BraceKind = "Set"
	BraceStack   BraceKind = "Stack"
)

// BraceEntry is the sum of brace-literal entry shapes.
type BraceEntry interface {
	Node
	braceEntryMarker()
}

// RecordEntry — `name: value`. Per ast.md §BraceEntry.
type RecordEntry struct {
	Span  Span
	Name  string
	Value Expr
}

func (n *RecordEntry) NodeSpan() Span    { return n.Span }
func (n *RecordEntry) NodeKind() string  { return "RecordEntry" }
func (n *RecordEntry) braceEntryMarker() {}

// MapEntry — `key: value` (key is a non-Ident expression).
type MapEntry struct {
	Span  Span
	Key   Expr
	Value Expr
}

func (n *MapEntry) NodeSpan() Span    { return n.Span }
func (n *MapEntry) NodeKind() string  { return "MapEntry" }
func (n *MapEntry) braceEntryMarker() {}

// SetEntry — `value` (no `:`). Per ast.md §BraceEntry.
type SetEntry struct {
	Span  Span
	Value Expr
}

func (n *SetEntry) NodeSpan() Span    { return n.Span }
func (n *SetEntry) NodeKind() string  { return "SetEntry" }
func (n *SetEntry) braceEntryMarker() {}

// BraceLit — the unified `NamedType "{" ... "}"` literal covering
// record / map / set / stack. Kind is committed by the parser on the
// first entry; an empty body stays Unknown until sema resolves it
// from TypeName. Per ast.md §BraceLit.
type BraceLit struct {
	Span     Span
	TypeName *NamedType
	Kind     BraceKind
	Entries  []BraceEntry
}

func (n *BraceLit) NodeSpan() Span   { return n.Span }
func (n *BraceLit) NodeKind() string { return "BraceLit" }
func (n *BraceLit) exprMarker()      {}

// TupleLit — `(e1, e2, ...)`. Per ast.md §Expr; arity ≥ 2.
type TupleLit struct {
	Span       Span
	Components []Expr
}

func (n *TupleLit) NodeSpan() Span   { return n.Span }
func (n *TupleLit) NodeKind() string { return "TupleLit" }
func (n *TupleLit) exprMarker()      {}

// TupleField — `recv.N` tuple element access (position ≥ 0). Per
// ast.md §Expr; distinct from Field (which has a name).
type TupleField struct {
	Span     Span
	Receiver Expr
	Position int
}

func (n *TupleField) NodeSpan() Span   { return n.Span }
func (n *TupleField) NodeKind() string { return "TupleField" }
func (n *TupleField) exprMarker()      {}

// ClosureLit — a function value. Full form `func(p: T): R { body }`
// or short form `(p) => expr` (desugared to a Block whose trailing is
// expr). Per ast.md §Expr; Params may have nil DeclType (short form).
type ClosureLit struct {
	Span       Span
	Params     []*Param
	ReturnType TypeExpr // nil ⇒ inferred / unit
	Body       *Block
	Short      bool // true for the `(p) => expr` form
}

func (n *ClosureLit) NodeSpan() Span   { return n.Span }
func (n *ClosureLit) NodeKind() string { return "ClosureLit" }
func (n *ClosureLit) exprMarker()      {}

// ParenExpr — `( inner )`. Per ast.md §Expr. Preserved as a node
// (not flattened to `inner`) so codegen reproduces the author's
// grouping verbatim — dropping it loses operator-precedence intent
// (`a * (b + c)` would re-associate to `a * b + c`).
type ParenExpr struct {
	Span  Span
	Inner Expr
}

func (n *ParenExpr) NodeSpan() Span   { return n.Span }
func (n *ParenExpr) NodeKind() string { return "ParenExpr" }
func (n *ParenExpr) exprMarker()      {}

// TryExpr — `try e`. Per `lang-spec/ast.md` §Expr and
// `lang-spec/desugaring.md` §T-Try, evaluates the inner
// expression (which must be of `Result<T, E>` or `Option<T>`
// shape per sema); if the inner value is Err / None, early-
// returns the wrapped error from the enclosing function;
// otherwise the value of the whole `try e` is the unwrapped
// payload.
type TryExpr struct {
	Span  Span
	Inner Expr
}

func (n *TryExpr) NodeSpan() Span   { return n.Span }
func (n *TryExpr) NodeKind() string { return "TryExpr" }
func (n *TryExpr) exprMarker()      {}

// ScopeExpr — `scope<T, E>(parent?) { body }`. A structured-
// concurrency scope (D7/D11): an expression evaluating to
// `Result<T, E>`. The body's trailing expression is the success
// value T (absent ⇒ T = unit); `spawn` blocks inside register on
// the scope's group and the scope joins them before returning. Per
// ast.md §Expr (`ScopeExpr { type_args, parent, body }`).
type ScopeExpr struct {
	Span     Span
	TypeArgs []TypeExpr
	Parent   Expr // optional parent context; nil ⇒ background
	Body     *Block
}

func (n *ScopeExpr) NodeSpan() Span   { return n.Span }
func (n *ScopeExpr) NodeKind() string { return "ScopeExpr" }
func (n *ScopeExpr) exprMarker()      {}

// SpawnExpr — `spawn { body }`. Registers a goroutine on the
// enclosing scope's group (E0405 if there is none). The body is
// typed `Result<unit, E>`; the expression itself is unit. Per
// ast.md §Expr (`SpawnExpr { body }`).
type SpawnExpr struct {
	Span Span
	Body *Block
}

func (n *SpawnExpr) NodeSpan() Span   { return n.Span }
func (n *SpawnExpr) NodeKind() string { return "SpawnExpr" }
func (n *SpawnExpr) exprMarker()      {}

// RangeExpr is `low..high` (exclusive) or `low..=high` (inclusive).
// Per ast.md, RangeExpr is iterable-position-only; it is NOT an
// Expr (it does not appear in the Expr sum). It satisfies the
// Iterable interface by implementing Node.
type RangeExpr struct {
	Span      Span
	Low       Expr
	High      Expr
	Inclusive bool
}

func (n *RangeExpr) NodeSpan() Span   { return n.Span }
func (n *RangeExpr) NodeKind() string { return "RangeExpr" }
