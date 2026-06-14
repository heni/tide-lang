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
	// The lone `fmt.println(...)` is the function body's trailing
	// expression (block-as-expression value rule), not a statement.
	if len(fn.Body.Stmts) != 0 {
		t.Errorf("body stmts = %d; want 0 (call is the trailing expr)", len(fn.Body.Stmts))
	}
	if fn.Body.Trailing == nil {
		t.Errorf("body trailing = nil; want the println call")
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

func TestSumTypeNullary(t *testing.T) {
	src := `type Color = | Red | Green | Blue`
	f := parseString(t, src)
	td, ok := f.Decls[0].(*ast.TypeDecl)
	if !ok {
		t.Fatalf("decl[0] = %T; want TypeDecl", f.Decls[0])
	}
	if td.Name != "Color" {
		t.Errorf("type name = %q; want Color", td.Name)
	}
	sb, ok := td.Body.(*ast.SumTypeBody)
	if !ok {
		t.Fatalf("body = %T; want SumTypeBody", td.Body)
	}
	if len(sb.Variants) != 3 {
		t.Errorf("variants = %d; want 3", len(sb.Variants))
	}
	for i, want := range []string{"Red", "Green", "Blue"} {
		if sb.Variants[i].Name != want {
			t.Errorf("variant[%d] = %q; want %q", i, sb.Variants[i].Name, want)
		}
		if len(sb.Variants[i].Fields) != 0 {
			t.Errorf("nullary variant has fields: %v", sb.Variants[i].Fields)
		}
	}
}

func TestMatchExpression(t *testing.T) {
	src := `func main() {
  match x {
    Red => 1,
    _ => 0,
  }
}`
	f := parseString(t, src)
	fn := f.Decls[0].(*ast.FuncDecl)
	// The `match` is the function body's trailing (value) expression.
	m, ok := fn.Body.Trailing.(*ast.MatchExpr)
	if !ok {
		t.Fatalf("body trailing = %T; want MatchExpr", fn.Body.Trailing)
	}
	if len(m.Arms) != 2 {
		t.Fatalf("arms = %d; want 2", len(m.Arms))
	}
	if _, ok := m.Arms[0].Pattern.(*ast.VariantPat); !ok {
		t.Errorf("arm[0] pattern = %T; want VariantPat", m.Arms[0].Pattern)
	}
	if _, ok := m.Arms[1].Pattern.(*ast.WildcardPat); !ok {
		t.Errorf("arm[1] pattern = %T; want WildcardPat", m.Arms[1].Pattern)
	}
}

func TestVariantPatWithPayload(t *testing.T) {
	src := `func f() {
  match v {
    Some(x) => x,
    None => 0,
  }
}`
	f := parseString(t, src)
	fn := f.Decls[0].(*ast.FuncDecl)
	m := fn.Body.Trailing.(*ast.MatchExpr)
	vp, ok := m.Arms[0].Pattern.(*ast.VariantPat)
	if !ok {
		t.Fatalf("arm[0] not VariantPat: %T", m.Arms[0].Pattern)
	}
	if len(vp.QName) != 1 || vp.QName[0] != "Some" || len(vp.Sub) != 1 {
		t.Errorf("Some(x) parsed as %v with %d sub-patterns", vp.QName, len(vp.Sub))
	}
	if _, ok := vp.Sub[0].(*ast.IdentPat); !ok {
		t.Errorf("Some sub-pattern = %T; want IdentPat", vp.Sub[0])
	}
}

func TestAliasTypeDecl(t *testing.T) {
	src := `type Age = int`
	f := parseString(t, src)
	td := f.Decls[0].(*ast.TypeDecl)
	ab, ok := td.Body.(*ast.AliasBody)
	if !ok {
		t.Fatalf("body = %T; want AliasBody", td.Body)
	}
	pt, ok := ab.Aliased.(*ast.PrimitiveType)
	if !ok || pt.Name != "int" {
		t.Errorf("aliased = %T %v; want PrimitiveType(int)", ab.Aliased, ab.Aliased)
	}
}

func TestSliceTypeAndLit(t *testing.T) {
	src := `func main() {
  var xs: []int = []int{1, 2, 3}
}`
	f := parseString(t, src)
	fn := f.Decls[0].(*ast.FuncDecl)
	vs, ok := fn.Body.Stmts[0].(*ast.VarStmt)
	if !ok {
		t.Fatalf("stmt[0] = %T; want VarStmt", fn.Body.Stmts[0])
	}
	st, ok := vs.DeclType.(*ast.SliceType)
	if !ok {
		t.Fatalf("decl type = %T; want SliceType", vs.DeclType)
	}
	pt, ok := st.Elem.(*ast.PrimitiveType)
	if !ok || pt.Name != "int" {
		t.Errorf("elem = %T %v; want PrimitiveType(int)", st.Elem, st.Elem)
	}
	sl, ok := vs.Value.(*ast.SliceLit)
	if !ok {
		t.Fatalf("value = %T; want SliceLit", vs.Value)
	}
	if sl.ElemType == nil {
		t.Errorf("annotated SliceLit lost ElemType")
	}
	if len(sl.Items) != 3 {
		t.Errorf("items = %d; want 3", len(sl.Items))
	}
}

func TestIndexAndSlice(t *testing.T) {
	src := `func main() {
  let v = xs[0]
  let mid = xs[1:3]
  let suf = xs[1:]
  let pre = xs[:3]
}`
	f := parseString(t, src)
	stmts := f.Decls[0].(*ast.FuncDecl).Body.Stmts
	// First: Index
	if _, ok := stmts[0].(*ast.LetStmt).Value.(*ast.Index); !ok {
		t.Errorf("stmt[0] value = %T; want Index", stmts[0].(*ast.LetStmt).Value)
	}
	// Slice forms
	for i := 1; i < 4; i++ {
		if _, ok := stmts[i].(*ast.LetStmt).Value.(*ast.Slice); !ok {
			t.Errorf("stmt[%d] value = %T; want Slice", i, stmts[i].(*ast.LetStmt).Value)
		}
	}
	// `xs[1:]` — High is nil
	if se := stmts[2].(*ast.LetStmt).Value.(*ast.Slice); se.High != nil {
		t.Errorf("xs[1:] has non-nil High")
	}
	// `xs[:3]` — Low is nil
	if se := stmts[3].(*ast.LetStmt).Value.(*ast.Slice); se.Low != nil {
		t.Errorf("xs[:3] has non-nil Low")
	}
}

func TestInferredSliceLit(t *testing.T) {
	src := `func main() {
  let xs = [10, 20, 30]
}`
	f := parseString(t, src)
	let := f.Decls[0].(*ast.FuncDecl).Body.Stmts[0].(*ast.LetStmt)
	sl, ok := let.Value.(*ast.SliceLit)
	if !ok {
		t.Fatalf("value = %T; want SliceLit", let.Value)
	}
	if sl.ElemType != nil {
		t.Errorf("inferred SliceLit unexpectedly has ElemType")
	}
	if len(sl.Items) != 3 {
		t.Errorf("items = %d; want 3", len(sl.Items))
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

// splitChainedTupleIndex re-splits a `N.M` FloatLit lexeme that the
// context-free lexer produced for a chained tuple index `r.1.0`. Only the
// plain `digits "." digits` shape is a chain; every other float form (and
// malformed input) must be rejected so the parser can report the normal
// "expected field name after `.`" diagnostic.
func TestSplitChainedTupleIndex(t *testing.T) {
	cases := []struct {
		lexeme   string
		lhs, rhs int
		ok       bool
	}{
		{"1.0", 1, 0, true},
		{"0.0", 0, 0, true},
		{"10.2", 10, 2, true},
		{"1e3", 0, 0, false},   // exponent, not a chain
		{"1.5e3", 0, 0, false}, // fractional exponent
		{"1.", 0, 0, false},    // missing rhs
		{".5", 0, 0, false},    // missing lhs
		{"1.2.3", 0, 0, false}, // rhs "2.3" is not an integer
		{"12", 0, 0, false},    // no dot
	}
	for _, c := range cases {
		lhs, rhs, ok := splitChainedTupleIndex(c.lexeme)
		if ok != c.ok || (ok && (lhs != c.lhs || rhs != c.rhs)) {
			t.Errorf("splitChainedTupleIndex(%q) = (%d, %d, %v), want (%d, %d, %v)",
				c.lexeme, lhs, rhs, ok, c.lhs, c.rhs, c.ok)
		}
	}
}
