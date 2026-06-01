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

func TestDuplicateClassFieldFiresE0105(t *testing.T) {
	src := `class Point {
  var x: int
  var y: int
  var x: int
}
`
	if codes := runCheck(t, src); !contains(codes, "E0105") {
		t.Errorf("expected E0105, got %v", codes)
	}
}

func TestDuplicateVariantPayloadFieldFiresE0105(t *testing.T) {
	src := `type Pair = | Both(a: int, a: int) | Neither
`
	if codes := runCheck(t, src); !contains(codes, "E0105") {
		t.Errorf("expected E0105, got %v", codes)
	}
}

func TestWrongGenericArityFiresE0207(t *testing.T) {
	src := `func bad(m: Map<int>) {}
`
	if codes := runCheck(t, src); !contains(codes, "E0207") {
		t.Errorf("expected E0207, got %v", codes)
	}
}

func TestAliasCycleFiresE0114(t *testing.T) {
	src := `type A = B
type B = A
`
	if codes := runCheck(t, src); !contains(codes, "E0114") {
		t.Errorf("expected E0114, got %v", codes)
	}
}

func TestSelfAliasFiresE0114(t *testing.T) {
	src := `type Loop = Loop
`
	if codes := runCheck(t, src); !contains(codes, "E0114") {
		t.Errorf("expected E0114, got %v", codes)
	}
}

func TestThreeNodeAliasCycleFiresE0114(t *testing.T) {
	src := `type A = B
type B = C
type C = A
`
	if codes := runCheck(t, src); !contains(codes, "E0114") {
		t.Errorf("expected E0114, got %v", codes)
	}
}

func TestTooManyGenericArgsFiresE0207(t *testing.T) {
	src := `func bad(r: Result<int, string, bool>) {}
`
	if codes := runCheck(t, src); !contains(codes, "E0207") {
		t.Errorf("expected E0207, got %v", codes)
	}
}

func TestNonCyclicAliasPasses(t *testing.T) {
	src := `type Cents = int
type Total = Cents
`
	if codes := runCheck(t, src); len(codes) != 0 {
		t.Errorf("expected clean, got %v", codes)
	}
}

func TestCallArityWrongNumberOfArgsFiresE0202(t *testing.T) {
	src := `func greet(name: string, age: int) {}
func main() { greet("alice") }
`
	if codes := runCheck(t, src); !contains(codes, "E0202") {
		t.Errorf("expected E0202, got %v", codes)
	}
}

func TestCallArityCorrectPasses(t *testing.T) {
	src := `func greet(name: string, age: int) {}
func main() { greet("alice", 30) }
`
	if codes := runCheck(t, src); len(codes) != 0 {
		t.Errorf("expected clean, got %v", codes)
	}
}

func TestConstructorArityFiresE0202(t *testing.T) {
	src := `class Point {
  var x: int
  var y: int
}
func main() { let _ = Point(1) }
`
	if codes := runCheck(t, src); !contains(codes, "E0202") {
		t.Errorf("expected E0202, got %v", codes)
	}
}

// --- Barrier C scalar inference (PR-Sema-C1) ---------------------

func TestLetAnnotationMismatchFiresE0201(t *testing.T) {
	src := `func main() {
  let x: int = "hello"
}
`
	if codes := runCheck(t, src); !contains(codes, "E0201") {
		t.Errorf("expected E0201 (annotation mismatch), got %v", codes)
	}
}

func TestLetAnnotationMatchPasses(t *testing.T) {
	src := `func main() {
  let x: int = 3
  let s: string = "ok"
  let b: bool = true
}
`
	if codes := runCheck(t, src); len(codes) != 0 {
		t.Errorf("expected clean, got %v", codes)
	}
}

func TestAssignTypeMismatchFiresE0201(t *testing.T) {
	src := `func main() {
  var n: int = 0
  n = "nope"
}
`
	if codes := runCheck(t, src); !contains(codes, "E0201") {
		t.Errorf("expected E0201 (assign mismatch), got %v", codes)
	}
}

func TestArgTypeMismatchFiresE0201(t *testing.T) {
	src := `func greet(name: string) {}
func main() { greet(42) }
`
	if codes := runCheck(t, src); !contains(codes, "E0201") {
		t.Errorf("expected E0201 (arg mismatch), got %v", codes)
	}
}

func TestArgTypeMatchPasses(t *testing.T) {
	src := `func add(a: int, b: int): int { return a + b }
func main() { let _ = add(1, 2) }
`
	if codes := runCheck(t, src); len(codes) != 0 {
		t.Errorf("expected clean, got %v", codes)
	}
}

func TestBinaryOperandMismatchFiresE0201(t *testing.T) {
	src := `func main() {
  let x = 1 + true
}
`
	if codes := runCheck(t, src); !contains(codes, "E0201") {
		t.Errorf("expected E0201 (numeric op on bool), got %v", codes)
	}
}

func TestLogicalOperandMismatchFiresE0201(t *testing.T) {
	src := `func main() {
  let x = true && 1
}
`
	if codes := runCheck(t, src); !contains(codes, "E0201") {
		t.Errorf("expected E0201 (&& on int), got %v", codes)
	}
}

func TestReturnTypeMismatchFiresE0203(t *testing.T) {
	src := `func count(): int { return "no" }
`
	if codes := runCheck(t, src); !contains(codes, "E0203") {
		t.Errorf("expected E0203 (return mismatch), got %v", codes)
	}
}

func TestReturnTypeMatchPasses(t *testing.T) {
	src := `func count(): int { return 7 }
`
	if codes := runCheck(t, src); len(codes) != 0 {
		t.Errorf("expected clean, got %v", codes)
	}
}

func TestFieldAccessTypeMismatchFiresE0201(t *testing.T) {
	src := `class Counter { var n: int }
func main() {
  let c = Counter(0)
  let s: string = c.n
}
`
	if codes := runCheck(t, src); !contains(codes, "E0201") {
		t.Errorf("expected E0201 (field type used as string), got %v", codes)
	}
}

func TestTransparentAliasMatchesUnderlying(t *testing.T) {
	src := `type Cents = int
func price(): Cents { return 100 }
func main() {
  let total: int = price()
}
`
	if codes := runCheck(t, src); len(codes) != 0 {
		t.Errorf("expected clean (alias transparent to int), got %v", codes)
	}
}

func TestUnmodelledCalleeReturnNoFalsePositive(t *testing.T) {
	// `panic` is a builtin func whose result type PR-C1 does not
	// model (Unknown); returning it must not fire E0203.
	src := `func pick(b: bool): int {
  if b { return 1 }
  return panic("unreachable")
}
`
	if codes := runCheck(t, src); len(codes) != 0 {
		t.Errorf("expected clean (unknown callee result), got %v", codes)
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
