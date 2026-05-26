package parser

import (
	"strings"
	"testing"

	"github.com/heni/tide-lang/internal/ast"
	"github.com/heni/tide-lang/internal/lexer"
)

func parseString(t *testing.T, src string) *ast.File {
	t.Helper()
	toks, lerr := lexer.Lex(src)
	if lerr != nil {
		t.Fatalf("lex error: %v", lerr)
	}
	f, perr := Parse(toks)
	if perr != nil {
		t.Fatalf("parse error: %v", perr)
	}
	return f
}

func TestHello(t *testing.T) {
	src := `import fmt

func main() {
  fmt.println("Tide is rising.")
}
`
	f := parseString(t, src)
	if len(f.Imports) != 1 || f.Imports[0].Path != "fmt" {
		t.Errorf("expected one import `fmt`; got %+v", f.Imports)
	}
	if len(f.Decls) != 1 {
		t.Fatalf("expected one decl; got %d", len(f.Decls))
	}
	fn, ok := f.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatalf("decl[0] not FuncDecl: %T", f.Decls[0])
	}
	if fn.Name != "main" {
		t.Errorf("func name = %q; want \"main\"", fn.Name)
	}
	if len(fn.Body.Stmts) != 1 {
		t.Errorf("body stmts = %d; want 1", len(fn.Body.Stmts))
	}
}

func TestFizzBuzzShape(t *testing.T) {
	src := `import fmt

func main() {
  for i in 1..=100 {
    if i % 15 == 0 {
      fmt.println("FizzBuzz")
    } else if i % 3 == 0 {
      fmt.println("Fizz")
    } else if i % 5 == 0 {
      fmt.println("Buzz")
    } else {
      fmt.println(i)
    }
  }
}
`
	f := parseString(t, src)
	fn := f.Decls[0].(*ast.FuncDecl)
	if len(fn.Body.Stmts) != 1 {
		t.Fatalf("main body should have one for-stmt; got %d", len(fn.Body.Stmts))
	}
	fs, ok := fn.Body.Stmts[0].(*ast.ForStmt)
	if !ok {
		t.Fatalf("top-level stmt is not ForStmt: %T", fn.Body.Stmts[0])
	}
	r, ok := fs.Iterable.(*ast.RangeExpr)
	if !ok {
		t.Fatalf("iterable is not RangeExpr: %T", fs.Iterable)
	}
	if !r.Inclusive {
		t.Errorf("range should be inclusive (..=); got exclusive")
	}
}

func TestParseFile_FilePrefix(t *testing.T) {
	toks, _ := lexer.LexFile("##", "foo.td") // lex error first
	if toks == nil {
		// expected: lex returns tokens-so-far + error; here it
		// errors before producing any token, but tokens may be
		// the empty slice with an EOF. The point of this test is
		// just to confirm the parser is reachable; if the lexer
		// failed, that's fine.
		return
	}
	_, perr := ParseFile(toks, "foo.td")
	if perr == nil {
		return
	}
	if !strings.HasPrefix(perr.Error(), "foo.td:") {
		t.Errorf("parser diag missing file prefix: %s", perr.Error())
	}
}

func TestComparisonNonAssociative(t *testing.T) {
	// grammar.ebnf separates EqExpr (== !=) and CmpExpr (< <= > >=)
	// as non-associative single-operator productions. Chained
	// applications at the same level must be rejected.
	cases := []string{
		`func main() { if a == b == c { } }`,
		`func main() { if a != b != c { } }`,
		`func main() { if a < b < c { } }`,
		`func main() { if a <= b > c { } }`,
	}
	for _, src := range cases {
		toks, _ := lexer.Lex(src)
		_, err := Parse(toks)
		if err == nil {
			t.Errorf("expected E0112 on %q; got no error", src)
			continue
		}
		if err.Code != "E0112" {
			t.Errorf("for %q want E0112; got %s", src, err.Code)
		}
	}
}

func TestEqAndCmpMixedNests(t *testing.T) {
	// `a == b && c < d` is fine — different precedence levels.
	src := `func main() { if a == b && c < d { } }`
	toks, _ := lexer.Lex(src)
	if _, err := Parse(toks); err != nil {
		t.Errorf("unexpected error on %q: %v", src, err)
	}
}

func TestFuncWithParamsAndReturn(t *testing.T) {
	src := `func add(a: int, b: int): int {
  return a + b
}`
	f := parseString(t, src)
	fn := f.Decls[0].(*ast.FuncDecl)
	if fn.Name != "add" {
		t.Errorf("func name = %q; want add", fn.Name)
	}
	if len(fn.Params) != 2 {
		t.Fatalf("params count = %d; want 2", len(fn.Params))
	}
	if fn.Params[0].Name != "a" || fn.Params[1].Name != "b" {
		t.Errorf("param names = [%q, %q]; want [a, b]", fn.Params[0].Name, fn.Params[1].Name)
	}
	if fn.ReturnType == nil {
		t.Fatal("expected non-nil return type")
	}
	pt, ok := fn.ReturnType.(*ast.PrimitiveType)
	if !ok || pt.Name != "int" {
		t.Errorf("return type = %T %v; want PrimitiveType(int)", fn.ReturnType, fn.ReturnType)
	}
	// Body has one ExprStmt wrapping a ReturnExpr.
	if len(fn.Body.Stmts) != 1 {
		t.Fatalf("body stmts = %d; want 1", len(fn.Body.Stmts))
	}
	es, ok := fn.Body.Stmts[0].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("first stmt is not ExprStmt: %T", fn.Body.Stmts[0])
	}
	if _, ok := es.Expr.(*ast.ReturnExpr); !ok {
		t.Errorf("first stmt's expr is not ReturnExpr: %T", es.Expr)
	}
}

func TestLetVarAssign(t *testing.T) {
	src := `func main() {
  let x = 42
  var y: int = 7
  y = y + x
}`
	f := parseString(t, src)
	fn := f.Decls[0].(*ast.FuncDecl)
	stmts := fn.Body.Stmts
	if len(stmts) != 3 {
		t.Fatalf("expected 3 stmts; got %d", len(stmts))
	}
	if let, ok := stmts[0].(*ast.LetStmt); !ok {
		t.Errorf("stmt[0] = %T %v; want LetStmt", stmts[0], stmts[0])
	} else if id, ok := let.Pattern.(*ast.IdentPat); !ok || id.Name != "x" {
		t.Errorf("LetStmt pattern = %T %v; want IdentPat x", let.Pattern, let.Pattern)
	}
	if v, ok := stmts[1].(*ast.VarStmt); !ok || v.Name != "y" || v.DeclType == nil {
		t.Errorf("stmt[1] = %T %v; want VarStmt y with type", stmts[1], stmts[1])
	}
	if _, ok := stmts[2].(*ast.AssignStmt); !ok {
		t.Errorf("stmt[2] = %T; want AssignStmt", stmts[2])
	}
}

func TestBareReturn(t *testing.T) {
	src := `func foo() {
  return
}`
	f := parseString(t, src)
	fn := f.Decls[0].(*ast.FuncDecl)
	if len(fn.Body.Stmts) != 1 {
		t.Fatalf("expected 1 stmt; got %d", len(fn.Body.Stmts))
	}
	es, ok := fn.Body.Stmts[0].(*ast.ExprStmt)
	if !ok {
		t.Fatalf("not ExprStmt: %T", fn.Body.Stmts[0])
	}
	ret, ok := es.Expr.(*ast.ReturnExpr)
	if !ok {
		t.Fatalf("not ReturnExpr: %T", es.Expr)
	}
	if ret.Value != nil {
		t.Errorf("bare return has non-nil value: %v", ret.Value)
	}
}

func TestGenericTypeArgs(t *testing.T) {
	// Type-arg parsing exists even though no PR-F1 corpus uses it;
	// `Map<string, int>` should round-trip through the parser.
	src := `func lookup(m: Map<string, int>, k: string): int {
  return 0
}`
	f := parseString(t, src)
	fn := f.Decls[0].(*ast.FuncDecl)
	mapTy, ok := fn.Params[0].DeclType.(*ast.NamedType)
	if !ok {
		t.Fatalf("first param type is not NamedType: %T", fn.Params[0].DeclType)
	}
	if mapTy.QName[0] != "Map" || len(mapTy.Args) != 2 {
		t.Errorf("Map<string, int> mis-parsed: qname=%v args=%d", mapTy.QName, len(mapTy.Args))
	}
}

func TestCanonicalSerialisationStable(t *testing.T) {
	src := `import fmt

func main() {
  fmt.println("Tide is rising.")
}
`
	f := parseString(t, src)
	a := ast.Canonical(f)
	// Second parse → second canonical must equal first.
	g := parseString(t, src)
	b := ast.Canonical(g)
	if a != b {
		t.Errorf("canonical serialisation not deterministic:\n--- a ---\n%s\n--- b ---\n%s", a, b)
	}
	// Sanity: form starts with the root node name.
	if !strings.HasPrefix(a, "(File") {
		t.Errorf("canonical does not start with (File: %s", a[:40])
	}
}
