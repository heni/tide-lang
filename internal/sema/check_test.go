package sema

import (
	"testing"

	"github.com/heni/tide-lang/internal/lexer"
	"github.com/heni/tide-lang/internal/parser"
)

func runCheck(t *testing.T, src string) []string {
	t.Helper()
	toks, lerr := lexer.LexFile(src, "test.td")
	if lerr != nil {
		t.Fatalf("lex: %v", lerr)
	}
	f, perr := parser.ParseFile(toks, "test.td")
	if perr != nil {
		t.Fatalf("parse: %v", perr)
	}
	_, diags := Check(f, "test.td")
	codes := make([]string, 0, len(diags))
	for _, d := range diags {
		codes = append(codes, d.Code)
	}
	return codes
}

func TestKnownNamesPass(t *testing.T) {
	src := `import fmt
class Counter { var n: int }
func main() {
  let c = Counter(7)
  fmt.println(c.n)
}
`
	codes := runCheck(t, src)
	if len(codes) != 0 {
		t.Errorf("clean program produced diags: %v", codes)
	}
}

func TestUnknownNameFiresE0103(t *testing.T) {
	src := `import fmt
func main() {
  fmt.println(missing)
}
`
	codes := runCheck(t, src)
	if !contains(codes, "E0103") {
		t.Errorf("expected E0103, got %v", codes)
	}
}

// Note: E0107 (reserved `_tide_` prefix) is caught by the
// lexer before sema sees the AST. The defensive check in
// builtins.go is unreachable via the public path today; it
// stays as defence-in-depth for future synthesised-name
// pipelines that bypass the lexer.

func TestDuplicateTopLevelDeclFiresE0113(t *testing.T) {
	src := `func foo() {}
func foo() {}
`
	codes := runCheck(t, src)
	if !contains(codes, "E0113") {
		t.Errorf("expected E0113, got %v", codes)
	}
}

func TestDuplicateVariantFiresE0106(t *testing.T) {
	src := `type Color = | Red | Green | Red
`
	codes := runCheck(t, src)
	if !contains(codes, "E0106") {
		t.Errorf("expected E0106 (duplicate variant), got %v", codes)
	}
}

func TestAmbiguousVariantFiresE0104(t *testing.T) {
	src := `type A = | Up | Down
type B = | Up | Left
`
	codes := runCheck(t, src)
	if !contains(codes, "E0104") {
		t.Errorf("expected E0104, got %v", codes)
	}
}

func TestClassFieldVisibleInsideMethod(t *testing.T) {
	src := `import fmt
class Counter {
  var n: int
  dump() {
    fmt.println(n)
  }
}
func main() { Counter(0).dump() }
`
	if codes := runCheck(t, src); len(codes) != 0 {
		t.Errorf("expected clean (field via implicit receiver), got %v", codes)
	}
}

func TestForRangeBoundsResolve(t *testing.T) {
	src := `import fmt
func main() {
  for i in 0..missing_upper {
    fmt.println(i)
  }
}
`
	codes := runCheck(t, src)
	if !contains(codes, "E0103") {
		t.Errorf("expected E0103 for unresolved range bound, got %v", codes)
	}
}

func TestLetBindingResolves(t *testing.T) {
	src := `import fmt
func greet(name: string) {
  let prefix = "hi"
  fmt.println(prefix, name)
}
func main() { greet("Tide") }
`
	if codes := runCheck(t, src); len(codes) != 0 {
		t.Errorf("expected clean, got %v", codes)
	}
}

func TestForBindingResolves(t *testing.T) {
	src := `import fmt
func main() {
  for i in 0..3 {
    fmt.println(i)
  }
}
`
	if codes := runCheck(t, src); len(codes) != 0 {
		t.Errorf("expected clean, got %v", codes)
	}
}

func TestMatchPatternBindingResolves(t *testing.T) {
	src := `import fmt
func main() {
  let opt = Some(42)
  match opt {
    Some(v) => fmt.println(v),
    None => fmt.println("none"),
  }
}
`
	if codes := runCheck(t, src); len(codes) != 0 {
		t.Errorf("expected clean, got %v", codes)
	}
}

func TestThisInsideMethod(t *testing.T) {
	src := `import fmt
class Counter {
  var n: int
  dump() {
    fmt.println(this.n)
  }
}
func main() {
  let c = Counter(7)
  c.dump()
}
`
	if codes := runCheck(t, src); len(codes) != 0 {
		t.Errorf("expected clean, got %v", codes)
	}
}

func TestDiagsOrderedBySourcePos(t *testing.T) {
	src := `import fmt
func main() {
  fmt.println(b)
  fmt.println(a)
}
`
	toks, _ := lexer.LexFile(src, "test.td")
	f, _ := parser.ParseFile(toks, "test.td")
	_, diags := Check(f, "test.td")
	if len(diags) != 2 {
		t.Fatalf("expected 2 diags, got %d: %v", len(diags), diags)
	}
	if diags[0].Line != 3 || diags[1].Line != 4 {
		t.Errorf("diags not sorted: %v", diags)
	}
}

func contains(s []string, want string) bool {
	for _, v := range s {
		if v == want {
			return true
		}
	}
	return false
}
