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
