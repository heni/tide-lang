package parser

import (
	"testing"

	"github.com/heni/tide-lang/internal/ast"
	"github.com/heni/tide-lang/internal/lexer"
)

// Parser coverage for variadic parameters (`name: ...T`) and call-site
// spread (`...xs`) — grammar.ebnf §Param / §Arg.

// TestVariadicParamParses — a trailing `...T` parameter parses and is
// flagged Variadic, with DeclType holding the element type.
func TestVariadicParamParses(t *testing.T) {
	src := `func sum(label: string, nums: ...int): int { return 0 }`
	toks, _ := lexer.Lex(src)
	f, err := Parse(toks)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	fn := f.Decls[0].(*ast.FuncDecl)
	if len(fn.Params) != 2 {
		t.Fatalf("want 2 params, got %d", len(fn.Params))
	}
	if fn.Params[0].Variadic {
		t.Errorf("first param should not be variadic")
	}
	last := fn.Params[1]
	if !last.Variadic {
		t.Fatalf("last param should be variadic")
	}
	if pt, ok := last.DeclType.(*ast.PrimitiveType); !ok || pt.Name != "int" {
		t.Errorf("variadic DeclType should be element type int, got %T", last.DeclType)
	}
}

// TestVariadicParamMustBeLast — a `...T` parameter followed by another
// parameter is E0115.
func TestVariadicParamMustBeLast(t *testing.T) {
	src := `func f(xs: ...int, y: int) {}`
	toks, _ := lexer.Lex(src)
	_, err := Parse(toks)
	if err == nil {
		t.Fatalf("expected E0115, got no error")
	}
	if err.Code != "E0115" {
		t.Errorf("want E0115, got %s", err.Code)
	}
}

// TestSpreadArgParses — a trailing `...xs` call argument parses to a
// SpreadArg wrapping the inner expression.
func TestSpreadArgParses(t *testing.T) {
	src := `func main() { let r = f(1, ...xs) }`
	toks, _ := lexer.Lex(src)
	f, err := Parse(toks)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	main := f.Decls[0].(*ast.FuncDecl)
	call := main.Body.Stmts[0].(*ast.LetStmt).Value.(*ast.Call)
	if len(call.Args) != 2 {
		t.Fatalf("want 2 args, got %d", len(call.Args))
	}
	sp, ok := call.Args[1].(*ast.SpreadArg)
	if !ok {
		t.Fatalf("second arg should be a SpreadArg, got %T", call.Args[1])
	}
	if id, ok := sp.Inner.(*ast.Ident); !ok || id.Name != "xs" {
		t.Errorf("spread inner should be ident xs, got %T", sp.Inner)
	}
}

// TestSpreadArgMustBeLast — a spread followed by another argument is a
// syntax error (the `...xs` ends the argument list; the trailing `,`
// surfaces as E0112 at the closing `)`).
func TestSpreadArgMustBeLast(t *testing.T) {
	src := `func main() { f(...xs, 2) }`
	toks, _ := lexer.Lex(src)
	_, err := Parse(toks)
	if err == nil {
		t.Fatalf("expected a parse error, got none")
	}
	if err.Code != "E0112" {
		t.Errorf("want E0112, got %s", err.Code)
	}
}
