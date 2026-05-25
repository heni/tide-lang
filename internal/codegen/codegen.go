package codegen

import (
	"fmt"
	"go/format"
	"strconv"
	"strings"

	"github.com/heni/tide-lang/internal/ast"
)

// Emit lowers the given Tide AST to a Go source string. The
// returned text is gofmt-stable for the hello/fizzbuzz subset
// (no extra trailing whitespace, single trailing newline, tab
// indentation, alphabetised imports). file is the source path
// embedded into //line directives; pass "" to suppress them.
func Emit(f *ast.File, file string) (string, error) {
	g := &gen{file: file}
	g.writeHeader(f)
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok {
			return "", fmt.Errorf("codegen: PR-C only handles FuncDecl, got %T", d)
		}
		if err := g.emitFuncDecl(fn); err != nil {
			return "", err
		}
	}
	// gofmt -s pass — guarantees the output round-trips through
	// gofmt to itself (test-contract.md §GO, lowering-go.md
	// §Output formatting).
	out, err := format.Source([]byte(g.b.String()))
	if err != nil {
		// E0801 internal: codegen emitted malformed Go. This
		// should never reach a user under correct sema; if it
		// does, it's a compiler bug and the raw buffer is
		// included for compiler-developer triage only.
		return "", fmt.Errorf("internal[E0801]: codegen produced unparseable Go (please file a bug): %w\n--- raw output ---\n%s", err, g.b.String())
	}
	return string(out), nil
}

type gen struct {
	b      strings.Builder
	file   string
	indent int
	// emittedLine tracks the source line whose //line directive
	// has most recently been written, so we avoid emitting the
	// same directive twice in a row.
	emittedLine int
}

func (g *gen) writeHeader(f *ast.File) {
	g.b.WriteString("package main\n\n")
	// PR-C bindings shortcut: every Tide import resolves to the
	// matching Go stdlib package by the same name. fmt → "fmt".
	// strconv → "strconv". etc. Sorted for determinism.
	if len(f.Imports) > 0 {
		paths := make([]string, len(f.Imports))
		for i, im := range f.Imports {
			paths[i] = im.Path
		}
		// Simple insertion sort (n is tiny).
		for i := 1; i < len(paths); i++ {
			for j := i; j > 0 && paths[j-1] > paths[j]; j-- {
				paths[j-1], paths[j] = paths[j], paths[j-1]
			}
		}
		if len(paths) == 1 {
			g.b.WriteString("import \"")
			g.b.WriteString(paths[0])
			g.b.WriteString("\"\n\n")
		} else {
			g.b.WriteString("import (\n")
			for _, p := range paths {
				g.b.WriteString("\t\"")
				g.b.WriteString(p)
				g.b.WriteString("\"\n")
			}
			g.b.WriteString(")\n\n")
		}
	}
}

func (g *gen) emitFuncDecl(fn *ast.FuncDecl) error {
	g.line(fn.Span.StartLine)
	g.b.WriteString("func ")
	g.b.WriteString(goIdent(fn.Name))
	g.b.WriteString("() {\n")
	g.indent++
	if err := g.emitBlockBody(fn.Body); err != nil {
		return err
	}
	g.indent--
	g.b.WriteString("}\n")
	return nil
}

func (g *gen) emitBlockBody(b *ast.Block) error {
	for _, s := range b.Stmts {
		if err := g.emitStmt(s); err != nil {
			return err
		}
	}
	if b.Trailing != nil {
		// PR-C: trailing-expression block (used by IfExpr / ScopeExpr)
		// isn't reached for hello/fizzbuzz. Reserve.
		return fmt.Errorf("codegen: trailing-expression block not supported in PR-C")
	}
	return nil
}

func (g *gen) emitStmt(s ast.Stmt) error {
	switch v := s.(type) {
	case *ast.ExprStmt:
		g.line(v.Span.StartLine)
		g.writeIndent()
		if err := g.emitExpr(v.Expr); err != nil {
			return err
		}
		g.b.WriteByte('\n')
		return nil
	case *ast.IfStmt:
		return g.emitIfStmt(v)
	case *ast.ForStmt:
		return g.emitForStmt(v)
	}
	return fmt.Errorf("codegen: unhandled stmt %T", s)
}

func (g *gen) emitIfStmt(s *ast.IfStmt) error {
	g.line(s.Span.StartLine)
	g.writeIndent()
	g.b.WriteString("if ")
	if err := g.emitExpr(s.Cond); err != nil {
		return err
	}
	g.b.WriteString(" {\n")
	g.indent++
	if err := g.emitBlockBody(s.ThenBlock); err != nil {
		return err
	}
	g.indent--
	switch e := s.Else.(type) {
	case nil:
		g.writeIndent()
		g.b.WriteString("}\n")
	case *ast.IfStmt:
		g.writeIndent()
		g.b.WriteString("} else ")
		// emit the nested IfStmt without re-indenting the `if`.
		if err := g.emitElseIf(e); err != nil {
			return err
		}
	case *ast.Block:
		g.writeIndent()
		g.b.WriteString("} else {\n")
		g.indent++
		if err := g.emitBlockBody(e); err != nil {
			return err
		}
		g.indent--
		g.writeIndent()
		g.b.WriteString("}\n")
	default:
		return fmt.Errorf("codegen: unexpected else branch %T", s.Else)
	}
	return nil
}

// emitElseIf emits an IfStmt as the continuation of `} else `.
// It does NOT write a leading newline or indent — the caller has
// already emitted those.
func (g *gen) emitElseIf(s *ast.IfStmt) error {
	// //line directive maps the nested if's condition back to the
	// source position the developer typed `else if` on, not the
	// outer if's line. lowering-go.md §Source maps requires the
	// directive at every statement boundary.
	g.line(s.Span.StartLine)
	g.b.WriteString("if ")
	if err := g.emitExpr(s.Cond); err != nil {
		return err
	}
	g.b.WriteString(" {\n")
	g.indent++
	if err := g.emitBlockBody(s.ThenBlock); err != nil {
		return err
	}
	g.indent--
	switch e := s.Else.(type) {
	case nil:
		g.writeIndent()
		g.b.WriteString("}\n")
	case *ast.IfStmt:
		g.writeIndent()
		g.b.WriteString("} else ")
		return g.emitElseIf(e)
	case *ast.Block:
		g.writeIndent()
		g.b.WriteString("} else {\n")
		g.indent++
		if err := g.emitBlockBody(e); err != nil {
			return err
		}
		g.indent--
		g.writeIndent()
		g.b.WriteString("}\n")
	}
	return nil
}

func (g *gen) emitForStmt(s *ast.ForStmt) error {
	g.line(s.Span.StartLine)
	g.writeIndent()
	idPat, ok := s.Pattern.(*ast.IdentPat)
	if !ok {
		return fmt.Errorf("codegen: only IdentPat loop var in PR-C, got %T", s.Pattern)
	}
	switch iter := s.Iterable.(type) {
	case *ast.RangeExpr:
		g.b.WriteString("for ")
		g.b.WriteString(goIdent(idPat.Name))
		g.b.WriteString(" := ")
		if err := g.emitExpr(iter.Low); err != nil {
			return err
		}
		g.b.WriteString("; ")
		g.b.WriteString(goIdent(idPat.Name))
		if iter.Inclusive {
			g.b.WriteString(" <= ")
		} else {
			g.b.WriteString(" < ")
		}
		if err := g.emitExpr(iter.High); err != nil {
			return err
		}
		g.b.WriteString("; ")
		g.b.WriteString(goIdent(idPat.Name))
		g.b.WriteString("++ {\n")
	default:
		return fmt.Errorf("codegen: only RangeExpr iterables in PR-C, got %T", s.Iterable)
	}
	g.indent++
	if err := g.emitBlockBody(s.Body); err != nil {
		return err
	}
	g.indent--
	g.writeIndent()
	g.b.WriteString("}\n")
	return nil
}

// ---- expressions ----

func (g *gen) emitExpr(e ast.Expr) error {
	switch v := e.(type) {
	case *ast.IntLitExpr:
		g.b.WriteString(strconv.FormatInt(v.Value, 10))
		return nil
	case *ast.StringLitExpr:
		g.b.WriteString(strconv.Quote(v.Value))
		return nil
	case *ast.BoolLitExpr:
		if v.Value {
			g.b.WriteString("true")
		} else {
			g.b.WriteString("false")
		}
		return nil
	case *ast.Ident:
		g.b.WriteString(goIdent(v.Name))
		return nil
	case *ast.Field:
		return g.emitField(v)
	case *ast.Call:
		return g.emitCall(v)
	case *ast.Binary:
		if err := g.emitExpr(v.Left); err != nil {
			return err
		}
		g.b.WriteByte(' ')
		g.b.WriteString(v.Op)
		g.b.WriteByte(' ')
		return g.emitExpr(v.Right)
	case *ast.Unary:
		g.b.WriteString(v.Op)
		return g.emitExpr(v.Operand)
	}
	return fmt.Errorf("codegen: unhandled expression %T", e)
}

func (g *gen) emitField(f *ast.Field) error {
	if err := g.emitExpr(f.Receiver); err != nil {
		return err
	}
	g.b.WriteByte('.')
	g.b.WriteString(mapFieldName(f.Receiver, f.Name))
	return nil
}

func (g *gen) emitCall(c *ast.Call) error {
	if err := g.emitExpr(c.Callee); err != nil {
		return err
	}
	g.b.WriteByte('(')
	for i, a := range c.Args {
		if i > 0 {
			g.b.WriteString(", ")
		}
		if err := g.emitExpr(a); err != nil {
			return err
		}
	}
	g.b.WriteByte(')')
	return nil
}

// mapFieldName is the PR-C shortcut for binding calls. Tide
// `fmt.println` maps to Go `fmt.Println` etc. This bypasses the
// full bindgen pipeline; only the names hello/fizzbuzz use are
// hardcoded.
func mapFieldName(receiver ast.Expr, name string) string {
	id, ok := receiver.(*ast.Ident)
	if !ok {
		return goIdent(name)
	}
	switch id.Name {
	case "fmt":
		switch name {
		case "println":
			return "Println"
		case "print":
			return "Print"
		case "printf":
			return "Printf"
		case "sprintf":
			return "Sprintf"
		}
	}
	return goIdent(name)
}

// goIdent maps a Tide identifier to its Go form. PR-C handles
// the common cases (no transform); future PRs add Go-reserved-
// word escaping ("type" → "tide_type") and the `$tide_NN` →
// `_tide_NN` rewrite for codegen-synthesised names.
func goIdent(name string) string {
	if isGoReserved(name) {
		return "tide_" + name
	}
	return name
}

var goReserved = map[string]bool{
	"break": true, "case": true, "chan": true, "const": true,
	"continue": true, "default": true, "defer": true, "else": true,
	"fallthrough": true, "for": true, "func": true, "go": true,
	"goto": true, "if": true, "import": true, "interface": true,
	"map": true, "package": true, "range": true, "return": true,
	"select": true, "struct": true, "switch": true, "type": true,
	"var": true,
}

func isGoReserved(name string) bool { return goReserved[name] }

// ---- helpers ----

func (g *gen) writeIndent() {
	for i := 0; i < g.indent; i++ {
		g.b.WriteByte('\t')
	}
}

// line emits a //line directive at the start of a statement
// boundary, mapping subsequent Go lines back to the Tide source
// line. Suppressed when no file path was supplied.
func (g *gen) line(srcLine int) {
	if g.file == "" || srcLine == g.emittedLine {
		return
	}
	g.writeIndent()
	g.b.WriteString("//line ")
	g.b.WriteString(g.file)
	g.b.WriteByte(':')
	g.b.WriteString(strconv.Itoa(srcLine))
	g.b.WriteString(":1\n")
	g.emittedLine = srcLine
}
