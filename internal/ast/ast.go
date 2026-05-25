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

// FuncDecl is a top-level function. PR-B only handles the no-param
// no-return shape produced by hello.td / fizzbuzz.td. Param,
// TypeParams, and ReturnType slots are reserved for later PRs.
type FuncDecl struct {
	Span Span
	Name string
	Body *Block
}

func (n *FuncDecl) NodeSpan() Span   { return n.Span }
func (n *FuncDecl) NodeKind() string { return "FuncDecl" }
func (n *FuncDecl) declMarker()      {}

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

// ---------------------------------------------------------------
// Patterns
// ---------------------------------------------------------------

// Pattern is the sum of pattern kinds. PR-B only emits IdentPat
// (for loop variables); WildcardPat / TuplePat / VariantPat /
// RecordPat / AltPat land in later PRs together with their
// fixtures.
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

// Ident references a name in scope.
type Ident struct {
	Span Span
	Name string
}

func (n *Ident) NodeSpan() Span   { return n.Span }
func (n *Ident) NodeKind() string { return "Ident" }
func (n *Ident) exprMarker()      {}

// Call is an application: callee(args). Explicit type arguments
// (callee<T1, T2>(args)) are not parsed in PR-B; the slot will
// land with the parser pass that adds generics.
type Call struct {
	Span   Span
	Callee Expr
	Args   []Expr
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
