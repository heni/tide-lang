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

func TestMatchArmBlockBodyResolves(t *testing.T) {
	// A block-as-expression arm body: its locals are in scope for
	// its trailing value, and the whole thing types cleanly.
	src := `import fmt
type Color = | Red | Green
func name(c: Color): string {
  return match c {
    Red => {
      let s = "red"
      s
    },
    Green => "green",
  }
}
func main() { fmt.println(name(Red)) }
`
	if codes := runCheck(t, src); len(codes) != 0 {
		t.Errorf("expected clean (block arm body), got %v", codes)
	}
}

func TestIfExprArmBodyResolves(t *testing.T) {
	// An if-expression arm body types cleanly across both branches.
	src := `import fmt
type Color = | Red | Green
func name(c: Color): string {
  return match c {
    Red => if true { "red" } else { "crimson" },
    Green => "green",
  }
}
func main() { fmt.println(name(Green)) }
`
	if codes := runCheck(t, src); len(codes) != 0 {
		t.Errorf("expected clean (if-expr arm body), got %v", codes)
	}
}

func TestBlockExprScopeIsLocal(t *testing.T) {
	// A binding introduced inside a block-as-expression must not
	// leak past the block — referencing it outside is E0103.
	src := `import fmt
func main() {
  let v = {
    let inner = 1
    inner
  }
  fmt.println(inner)
}
`
	if codes := runCheck(t, src); !contains(codes, "E0103") {
		t.Errorf("expected E0103 for block-local leak, got %v", codes)
	}
}

func TestBreakInLoopPasses(t *testing.T) {
	src := `import fmt
func main() {
  var i = 0
  while i < 10 {
    if i == 3 { break }
    i += 1
  }
  for j in 0..5 {
    if j == 2 { continue }
    fmt.println(j)
  }
}
`
	if codes := runCheck(t, src); len(codes) != 0 {
		t.Errorf("expected clean (break/continue in loops), got %v", codes)
	}
}

func TestBreakOutsideLoopFiresE0404(t *testing.T) {
	src := `func main() {
  break
}
`
	if codes := runCheck(t, src); !contains(codes, "E0404") {
		t.Errorf("expected E0404 for break outside loop, got %v", codes)
	}
}

func TestContinueOutsideLoopFiresE0404(t *testing.T) {
	// A `continue` in a function body that is not inside any loop —
	// even nested in an if — is illegal.
	src := `func main() {
  if true {
    continue
  }
}
`
	if codes := runCheck(t, src); !contains(codes, "E0404") {
		t.Errorf("expected E0404 for continue outside loop, got %v", codes)
	}
}

func TestParenLiteralNarrowingNoFalsePositive(t *testing.T) {
	// Int-literal narrowing must see through a ParenExpr — `(5)` at
	// a byte target narrows like a bare `5`, no false E0201/E0204.
	src := `import fmt
func main() {
  let b: byte = (5)
  fmt.println(b)
}
`
	if codes := runCheck(t, src); len(codes) != 0 {
		t.Errorf("expected clean (paren literal narrowing), got %v", codes)
	}
}

func TestTupleTypingNoFalsePositive(t *testing.T) {
	// Tuple type in a signature, tuple literal, `.N` field access,
	// and a `for (i, v)` destructuring loop all type cleanly.
	src := `import fmt
func swap(p: (int, string)): (string, int) {
  return (p.1, p.0)
}
func main() {
  let s = swap((1, "a"))
  fmt.println(s.0, s.1)
  for (i, v) in ["x", "y"] {
    fmt.println(i, v)
  }
}
`
	if codes := runCheck(t, src); len(codes) != 0 {
		t.Errorf("expected clean (tuples), got %v", codes)
	}
}

func TestTuplePatternForMapResolves(t *testing.T) {
	src := `import fmt
func main() {
  let m = Map<string, int>.new()
  m["a"] = 1
  for (k, v) in m {
    fmt.println(k, v)
  }
}
`
	if codes := runCheck(t, src); len(codes) != 0 {
		t.Errorf("expected clean (for (k,v) over map), got %v", codes)
	}
}

func TestRecordTypingNoFalsePositive(t *testing.T) {
	src := `import fmt
type Point = { x: int, y: int }
func main() {
  let p = Point { x: 3, y: 4 }
  if p.x > 0 {
    fmt.println(p.x, p.y)
  }
}
`
	if codes := runCheck(t, src); len(codes) != 0 {
		t.Errorf("expected clean (records), got %v", codes)
	}
}

func TestRecordFieldMismatchFiresE0201(t *testing.T) {
	src := `type Point = { x: int, y: int }
func main() {
  let p = Point { x: 3, y: "no" }
}
`
	if codes := runCheck(t, src); !contains(codes, "E0201") {
		t.Errorf("expected E0201 for record field type mismatch, got %v", codes)
	}
}

func TestDuplicateRecordFieldFiresE0105(t *testing.T) {
	src := `type Bad = { a: int, a: int }
`
	if codes := runCheck(t, src); !contains(codes, "E0105") {
		t.Errorf("expected E0105 for duplicate record field, got %v", codes)
	}
}

func TestFloatTypingNoFalsePositive(t *testing.T) {
	src := `import fmt
func main() {
  let pi = 3.14
  let area = pi * 2.0
  if area > 1.0 {
    fmt.println(area)
  }
}
`
	if codes := runCheck(t, src); len(codes) != 0 {
		t.Errorf("expected clean (floats), got %v", codes)
	}
}

func TestFloatPatternFiresE0305(t *testing.T) {
	src := `func main() {
  match 3.14 {
    3.14 => 1,
    _ => 0,
  }
}
`
	if codes := runCheck(t, src); !contains(codes, "E0305") {
		t.Errorf("expected E0305 for float-literal pattern, got %v", codes)
	}
}

func TestFloatPatternInAltFiresE0305(t *testing.T) {
	// checkNoFloatPat must descend into AltPat atoms.
	src := `func main() {
  match 3.14 {
    1.0 | 3.14 => 1,
    _ => 0,
  }
}
`
	if codes := runCheck(t, src); !contains(codes, "E0305") {
		t.Errorf("expected E0305 for float-literal alt atom, got %v", codes)
	}
}

func TestRuneAndAltPatternNoFalsePositive(t *testing.T) {
	// Rune-literal patterns (P-Lit-Rune) and a literal AltPat
	// (P-Alt) type clean against a rune subject; the `_` arm makes
	// the (infinite-domain) match exhaustive.
	src := `func kind(c: rune): int {
  match c {
    '(' | '[' | '{' => 1,
    ')' => 2,
    _ => 0,
  }
}
`
	if codes := runCheck(t, src); len(codes) != 0 {
		t.Errorf("expected clean (rune + alt patterns), got %v", codes)
	}
}

func TestVariantAltExhaustiveNoFalsePositive(t *testing.T) {
	// A sum match whose arms are AltPats covering every variant is
	// exhaustive — collectCoveredVariants must descend into AltPat.
	src := `type Dir = | Up | Down | Left | Right
func vertical(d: Dir): bool {
  match d {
    Up | Down => true,
    Left | Right => false,
  }
}
`
	if codes := runCheck(t, src); len(codes) != 0 {
		t.Errorf("expected exhaustive (variant alt covers all), got %v", codes)
	}
}

func TestVariantAltNonExhaustiveFiresE0303(t *testing.T) {
	// Dropping Right from the alts leaves the match non-exhaustive.
	src := `type Dir = | Up | Down | Left | Right
func vertical(d: Dir): bool {
  match d {
    Up | Down => true,
    Left => false,
  }
}
`
	if codes := runCheck(t, src); !contains(codes, "E0303") {
		t.Errorf("expected E0303 (missing Right), got %v", codes)
	}
}

func TestClosureTypingNoFalsePositive(t *testing.T) {
	// Closure params are in scope in the body; captured outer
	// bindings resolve; a func-typed parameter type-checks.
	src := `import fmt
func apply(f: func(int): int, x: int): int {
  return f(x)
}
func main() {
  let factor = 3
  let scale = (x: int) => x * factor
  fmt.println(apply(scale, 5))
}
`
	if codes := runCheck(t, src); len(codes) != 0 {
		t.Errorf("expected clean (closures), got %v", codes)
	}
}

func TestClosureParamScopeIsLocal(t *testing.T) {
	// A closure parameter must not leak past the closure body.
	src := `import fmt
func main() {
  let f = (x: int) => x + 1
  fmt.println(x)
}
`
	if codes := runCheck(t, src); !contains(codes, "E0103") {
		t.Errorf("expected E0103 for closure-param leak, got %v", codes)
	}
}

func TestInterfaceConformanceNoFalsePositive(t *testing.T) {
	// A class that `implements` an interface is accepted where the
	// interface is expected; an interface-typed method call types.
	src := `import fmt
interface Shape { area(): int }
class Square implements Shape {
  let side: int
  area(): int { return this.side * this.side }
}
func describe(s: Shape): int { return s.area() }
func main() {
  let sq = Square { side: 5 }
  fmt.println(describe(sq))
}
`
	if codes := runCheck(t, src); len(codes) != 0 {
		t.Errorf("expected clean (interface conformance), got %v", codes)
	}
}

func TestTransitiveInterfaceConformance(t *testing.T) {
	// A class implementing `Solid`, which `extends Shape`, is accepted
	// where `Shape` is expected (conformance follows the extends chain).
	src := `interface Shape { area(): int }
interface Solid extends Shape { volume(): int }
class Cube implements Solid {
  let s: int
  area(): int { return this.s * this.s }
  volume(): int { return this.s * this.s * this.s }
}
func describe(sh: Shape): int { return sh.area() }
func main() { let c = Cube { s: 3 }; describe(c) }
`
	if codes := runCheck(t, src); len(codes) != 0 {
		t.Errorf("expected clean (transitive conformance), got %v", codes)
	}
}

func TestUnknownImplementsNameFiresE0103(t *testing.T) {
	src := `class C implements Nope { let n: int }
func main() {}
`
	if codes := runCheck(t, src); !contains(codes, "E0103") {
		t.Errorf("expected E0103 for unknown implements name, got %v", codes)
	}
}

func TestDuplicateInterfaceFiresE0113(t *testing.T) {
	src := `interface Foo { a(): int }
interface Foo { b(): int }
`
	if codes := runCheck(t, src); !contains(codes, "E0113") {
		t.Errorf("expected E0113 for duplicate interface, got %v", codes)
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

// --- Collections / conversions / comparability (PR-Sema-C2) ------

func TestSliceIndexInfersElementType(t *testing.T) {
	src := `func first(xs: []int): int { return xs[0] }
`
	if codes := runCheck(t, src); len(codes) != 0 {
		t.Errorf("expected clean (xs[0] : int), got %v", codes)
	}
}

func TestSliceIndexResultFlowsToReturnFiresE0203(t *testing.T) {
	src := `func first(xs: []int): string { return xs[0] }
`
	if codes := runCheck(t, src); !contains(codes, "E0203") {
		t.Errorf("expected E0203 (int element vs string return), got %v", codes)
	}
}

func TestStringFromIntSliceFiresE0205(t *testing.T) {
	src := `func main() {
  let xs = makeSlice<int>(2)
  let s = string(xs)
}
`
	if codes := runCheck(t, src); !contains(codes, "E0205") {
		t.Errorf("expected E0205 (string from []int), got %v", codes)
	}
}

func TestMapIndexInfersValueType(t *testing.T) {
	src := `func look(m: Map<int, string>): string { return m[0] }
`
	if codes := runCheck(t, src); len(codes) != 0 {
		t.Errorf("expected clean (m[0] : string), got %v", codes)
	}
}

func TestMapWrongKeyTypeFiresE0201(t *testing.T) {
	src := `func look(m: Map<int, string>): string { return m["x"] }
`
	if codes := runCheck(t, src); !contains(codes, "E0201") {
		t.Errorf("expected E0201 (string key vs int), got %v", codes)
	}
}

func TestMakeSliceInfersType(t *testing.T) {
	src := `func main() {
  let xs = makeSlice<int>(3)
  let n: int = xs[0]
}
`
	if codes := runCheck(t, src); len(codes) != 0 {
		t.Errorf("expected clean (makeSlice<int> : []int), got %v", codes)
	}
}

func TestSizedIntLiteralNarrows(t *testing.T) {
	src := `func main() {
  let x: int8 = 5
  let y: byte = 200
}
`
	if codes := runCheck(t, src); len(codes) != 0 {
		t.Errorf("expected clean (literal narrows to sized int), got %v", codes)
	}
}

func TestIntLiteralOutOfRangeFiresE0204(t *testing.T) {
	src := `func main() {
  let x: int8 = 999
}
`
	codes := runCheck(t, src)
	if !contains(codes, "E0204") {
		t.Errorf("expected E0204, got %v", codes)
	}
	if contains(codes, "E0201") {
		t.Errorf("E0204 should not also fire E0201 (literal adapts), got %v", codes)
	}
}

func TestIllegalConversionFiresE0205(t *testing.T) {
	src := `func main() {
  let n = int("hello")
}
`
	if codes := runCheck(t, src); !contains(codes, "E0205") {
		t.Errorf("expected E0205 (string -> int), got %v", codes)
	}
}

func TestValidConversionPasses(t *testing.T) {
	src := `func main() {
  let s = string(65)
  let r = int('a')
}
`
	if codes := runCheck(t, src); len(codes) != 0 {
		t.Errorf("expected clean (codepoint / rune conversions), got %v", codes)
	}
}

func TestRefEqNonClassFiresE0206(t *testing.T) {
	src := `func main() {
  let b = refEq(1, 2)
}
`
	if codes := runCheck(t, src); !contains(codes, "E0206") {
		t.Errorf("expected E0206 (refEq on non-class), got %v", codes)
	}
}

func TestRefEqSameClassPasses(t *testing.T) {
	src := `class Node { var v: int }
func main() {
  let a = Node(1)
  let b = Node(2)
  let same = refEq(a, b)
}
`
	if codes := runCheck(t, src); len(codes) != 0 {
		t.Errorf("expected clean (refEq same class), got %v", codes)
	}
}

func TestRefEqDifferentClassFiresE0206(t *testing.T) {
	src := `class A { var v: int }
class B { var v: int }
func main() {
  let x = refEq(A(1), B(2))
}
`
	if codes := runCheck(t, src); !contains(codes, "E0206") {
		t.Errorf("expected E0206 (refEq across classes), got %v", codes)
	}
}

func TestEqOnClassFiresE0401(t *testing.T) {
	src := `class Node { var v: int }
func main() {
  let a = Node(1)
  let b = Node(2)
  let same = a == b
}
`
	if codes := runCheck(t, src); !contains(codes, "E0401") {
		t.Errorf("expected E0401 (== on class), got %v", codes)
	}
}

func TestEqOnSliceFiresE0401(t *testing.T) {
	src := `func eq(a: []int, b: []int): bool { return a == b }
`
	if codes := runCheck(t, src); !contains(codes, "E0401") {
		t.Errorf("expected E0401 (== on slice), got %v", codes)
	}
}

func TestEqOnIntPasses(t *testing.T) {
	src := `func eq(a: int, b: int): bool { return a == b }
`
	if codes := runCheck(t, src); len(codes) != 0 {
		t.Errorf("expected clean (== on int), got %v", codes)
	}
}

// --- Integer-literal narrowing regressions (PR #72 review C1–C3) -

func TestSizedIntComparisonWithLiteralPasses(t *testing.T) {
	src := `func f(x: byte): bool { return x == 0 }
func g(r: rune): bool { return r >= 'a' }
`
	if codes := runCheck(t, src); len(codes) != 0 {
		t.Errorf("expected clean (literal narrows to operand type), got %v", codes)
	}
}

func TestSizedIntArithWithLiteralPasses(t *testing.T) {
	src := `func g(x: byte): byte { return x + 1 }
func h(r: rune): rune { return r - 32 }
`
	if codes := runCheck(t, src); len(codes) != 0 {
		t.Errorf("expected clean (literal narrows in arithmetic), got %v", codes)
	}
}

func TestMapIntLiteralKeyPasses(t *testing.T) {
	src := `func look(m: Map<byte, int>): int { return m[0] }
`
	if codes := runCheck(t, src); len(codes) != 0 {
		t.Errorf("expected clean (literal map key narrows), got %v", codes)
	}
}

func TestSliceLiteralNarrowsToSizedElem(t *testing.T) {
	src := `func main() {
  let xs: []byte = [1, 2, 3]
}
`
	if codes := runCheck(t, src); len(codes) != 0 {
		t.Errorf("expected clean (slice literal narrows to []byte), got %v", codes)
	}
}

func TestSliceLiteralElementOutOfRangeFiresE0204(t *testing.T) {
	src := `func main() {
  let xs: []byte = [1, 999]
}
`
	if codes := runCheck(t, src); !contains(codes, "E0204") {
		t.Errorf("expected E0204 (999 out of byte range), got %v", codes)
	}
}

func TestSliceLiteralElementMismatchFiresE0201(t *testing.T) {
	src := `func main() {
  let xs: []int = [1, true]
}
`
	if codes := runCheck(t, src); !contains(codes, "E0201") {
		t.Errorf("expected E0201 (bool element in []int), got %v", codes)
	}
}

// --- Dynamic doesn't leak (PR-Sema-3b) ---------------------------

func TestDynamicReturnWideningFiresE0209(t *testing.T) {
	src := `func f(): Dynamic { return 5 }
`
	if codes := runCheck(t, src); !contains(codes, "E0209") {
		t.Errorf("expected E0209 (return widening), got %v", codes)
	}
}

func TestDynamicVarWideningFiresE0209(t *testing.T) {
	src := `func main() { var d: Dynamic = 5 }
`
	if codes := runCheck(t, src); !contains(codes, "E0209") {
		t.Errorf("expected E0209 (var widening), got %v", codes)
	}
}

func TestDynamicSliceElementWideningFiresE0209(t *testing.T) {
	src := `func main() { let xs = []Dynamic{5} }
`
	if codes := runCheck(t, src); !contains(codes, "E0209") {
		t.Errorf("expected E0209 (slice element widening), got %v", codes)
	}
}

func TestDynamicNarrowingFiresE0210(t *testing.T) {
	src := `func f(d: Dynamic) { let x: int = d }
`
	if codes := runCheck(t, src); !contains(codes, "E0210") {
		t.Errorf("expected E0210 (narrowing), got %v", codes)
	}
}

func TestAnyDynamicMixFiresE0212(t *testing.T) {
	src := `func f(d: Dynamic): Any { return d }
`
	if codes := runCheck(t, src); !contains(codes, "E0212") {
		t.Errorf("expected E0212 (Any/Dynamic mix), got %v", codes)
	}
}

func TestDynamicPassthroughPasses(t *testing.T) {
	src := `func relay(d: Dynamic): Dynamic { return d }
`
	if codes := runCheck(t, src); len(codes) != 0 {
		t.Errorf("expected clean (Dynamic -> Dynamic), got %v", codes)
	}
}

func TestReflectParamImplicitWidenPasses(t *testing.T) {
	src := `import reflect
func main() { let t = reflect.typeOf(5) }
`
	if codes := runCheck(t, src); len(codes) != 0 {
		t.Errorf("expected clean (reflect.* implicit widen), got %v", codes)
	}
}

func TestReflectBoxToDynamicPasses(t *testing.T) {
	src := `import reflect
func sink(d: Dynamic) {}
func main() { sink(reflect.box(5)) }
`
	if codes := runCheck(t, src); len(codes) != 0 {
		t.Errorf("expected clean (reflect.box result is Dynamic), got %v", codes)
	}
}

// --- Barrier D: exhaustiveness + context legality (PR-Sema-4) ----

func TestNonExhaustiveSumMatchFiresE0303(t *testing.T) {
	src := `type Color = | Red | Green | Blue
func f(c: Color): int { return match c { Red => 1, Green => 2 } }
`
	if codes := runCheck(t, src); !contains(codes, "E0303") {
		t.Errorf("expected E0303 (missing Blue), got %v", codes)
	}
}

func TestExhaustiveSumMatchPasses(t *testing.T) {
	src := `type Color = | Red | Green | Blue
func f(c: Color): int { return match c { Red => 1, Green => 2, Blue => 3 } }
`
	if codes := runCheck(t, src); len(codes) != 0 {
		t.Errorf("expected clean (all variants covered), got %v", codes)
	}
}

func TestCatchAllMatchPasses(t *testing.T) {
	src := `type Color = | Red | Green | Blue
func f(c: Color): int { return match c { Red => 1, _ => 0 } }
`
	if codes := runCheck(t, src); len(codes) != 0 {
		t.Errorf("expected clean (wildcard covers rest), got %v", codes)
	}
}

func TestArmAfterCatchAllFiresE0304(t *testing.T) {
	src := `type Color = | Red | Green | Blue
func f(c: Color): int { return match c { Red => 1, _ => 0, Green => 2 } }
`
	if codes := runCheck(t, src); !contains(codes, "E0304") {
		t.Errorf("expected E0304 (unreachable arm), got %v", codes)
	}
}

func TestTryOutsideResultFnFiresE0402(t *testing.T) {
	src := `func g(): Result<int, error> { return Ok(1) }
func f(): int { let x = try g() }
`
	if codes := runCheck(t, src); !contains(codes, "E0402") {
		t.Errorf("expected E0402 (try in int fn), got %v", codes)
	}
}

func TestTryInsideResultFnPasses(t *testing.T) {
	src := `func g(): Result<int, error> { return Ok(1) }
func f(): Result<int, error> { let x = try g() return Ok(x) }
`
	if codes := runCheck(t, src); len(codes) != 0 {
		t.Errorf("expected clean (try in Result fn), got %v", codes)
	}
}

func TestThisOutsideInstanceMethodFiresE0501(t *testing.T) {
	src := `func f(): int { return this.n }
`
	if codes := runCheck(t, src); !contains(codes, "E0501") {
		t.Errorf("expected E0501 (this in free fn), got %v", codes)
	}
}

func TestThisInsideInstanceMethodPasses(t *testing.T) {
	src := `class Counter {
  var n: int
  get(): int { return this.n }
}
func main() { let _ = Counter(0).get() }
`
	if codes := runCheck(t, src); len(codes) != 0 {
		t.Errorf("expected clean (this in instance method), got %v", codes)
	}
}

// --- Generic type-argument inference (PR-Sema-5) -----------------

func TestGenericExplicitTypeArgReturnPasses(t *testing.T) {
	src := `func id<T>(x: T): T { return x }
func main() { let n: int = id<int>(5) }
`
	if codes := runCheck(t, src); len(codes) != 0 {
		t.Errorf("expected clean (explicit type arg), got %v", codes)
	}
}

func TestGenericInferredTypeArgReturnPasses(t *testing.T) {
	src := `func id<T>(x: T): T { return x }
func main() { let n: int = id(5) }
`
	if codes := runCheck(t, src); len(codes) != 0 {
		t.Errorf("expected clean (inferred type arg), got %v", codes)
	}
}

func TestGenericArgMismatchFiresE0201(t *testing.T) {
	src := `func id<T>(x: T): T { return x }
func main() { let _ = id<int>("hi") }
`
	if codes := runCheck(t, src); !contains(codes, "E0201") {
		t.Errorf("expected E0201 (string arg vs <int>), got %v", codes)
	}
}

func TestGenericReturnTypeMismatchFiresE0201(t *testing.T) {
	src := `func id<T>(x: T): T { return x }
func main() { let n: string = id<int>(5) }
`
	if codes := runCheck(t, src); !contains(codes, "E0201") {
		t.Errorf("expected E0201 (string annotation vs int return), got %v", codes)
	}
}

func TestGenericSliceInferencePasses(t *testing.T) {
	src := `func first<T>(xs: []T): T { return xs[0] }
func main() { let n: int = first<int>(makeSlice<int>(3)) }
`
	if codes := runCheck(t, src); len(codes) != 0 {
		t.Errorf("expected clean (slice generic infer), got %v", codes)
	}
}

func TestGenericDynamicTypeArgFiresE0211(t *testing.T) {
	src := `func id<T>(x: T): T { return x }
func main() { let _ = id<Dynamic>(reflect.box(5)) }
`
	if codes := runCheck(t, src); !contains(codes, "E0211") {
		t.Errorf("expected E0211 (Dynamic type arg), got %v", codes)
	}
}

func TestStaticContainerCtorTypedPasses(t *testing.T) {
	src := `func main() {
  var m = Map<int, string>.new()
  m[0] = "x"
}
`
	if codes := runCheck(t, src); len(codes) != 0 {
		t.Errorf("expected clean (Map<>.new() typed as Map), got %v", codes)
	}
}

func TestStaticContainerCtorWrongKeyFiresE0201(t *testing.T) {
	src := `func main() {
  var m = Map<int, string>.new()
  m["bad"] = "x"
}
`
	if codes := runCheck(t, src); !contains(codes, "E0201") {
		t.Errorf("expected E0201 (string key vs int on Map<>.new()), got %v", codes)
	}
}

func TestGenericCallWrongTypeArityFiresE0207(t *testing.T) {
	src := `func id<T>(x: T): T { return x }
func main() { let _ = id<int, string>(5) }
`
	if codes := runCheck(t, src); !contains(codes, "E0207") {
		t.Errorf("expected E0207 (wrong type-arg count), got %v", codes)
	}
}

func TestGenericBodyNoFalsePositive(t *testing.T) {
	src := `func pair<T>(a: T, b: T): bool { return a == b }
func wrap<T>(x: T): []T { return [x] }
`
	if codes := runCheck(t, src); len(codes) != 0 {
		t.Errorf("expected clean (generic body), got %v", codes)
	}
}

func TestDeferCallShapePasses(t *testing.T) {
	src := `import fmt
func main() {
  defer fmt.println("bye")
  defer (func() { fmt.println("late") })()
}
`
	if codes := runCheck(t, src); contains(codes, "E0406") {
		t.Errorf("defer of a call should not fire E0406, got %v", codes)
	}
}

func TestDeferNonCallFiresE0406(t *testing.T) {
	src := `func main() {
  let x = 1
  defer x
}
`
	if codes := runCheck(t, src); !contains(codes, "E0406") {
		t.Errorf("expected E0406 for `defer` of a non-call, got %v", codes)
	}
}

func TestChannelMethodsAndForClean(t *testing.T) {
	// T-MakeChannel + T-Chan-Send/Recv/Close + channel iteration:
	// a well-typed channel program produces no diagnostics.
	src := `import fmt
func main() {
  let ch = makeChannel<int>(2)
  ch.send(1)
  let x = ch.recv()
  ch.close()
  for v in ch {
    fmt.println(v)
  }
}
`
	if codes := runCheck(t, src); len(codes) != 0 {
		t.Errorf("clean channel program produced diags: %v", codes)
	}
}

func TestChannelWidensToSendChan(t *testing.T) {
	// T-Chan-Widen: a bidirectional Channel<int> is accepted where a
	// SendChan<int> parameter is expected.
	src := `func sink(out: SendChan<int>) { out.send(1) }
func main() {
  let ch = makeChannel<int>(1)
  sink(ch)
}
`
	if codes := runCheck(t, src); contains(codes, "E0201") {
		t.Errorf("Channel<int> should widen to SendChan<int>, got %v", codes)
	}
}

func TestSendChanDoesNotNarrowToChannel(t *testing.T) {
	// The reverse of T-Chan-Widen is rejected: a one-way SendChan<int>
	// does not fit a bidirectional Channel<int> parameter.
	src := `func use(c: Channel<int>) { c.send(1) }
func relay(out: SendChan<int>) { use(out) }
`
	if codes := runCheck(t, src); !contains(codes, "E0201") {
		t.Errorf("SendChan<int> should NOT narrow to Channel<int>, got %v", codes)
	}
}

func TestUnitLiteralFitsUnitAnnotation(t *testing.T) {
	// `()` types as unit and is accepted where `unit` is expected.
	src := `func consume(u: unit) {}
func main() {
  let u: unit = ()
  consume(())
  consume(u)
}
`
	if codes := runCheck(t, src); len(codes) != 0 {
		t.Errorf("unit literal/annotation should be clean, got %v", codes)
	}
}

func TestUnitDoesNotFitInt(t *testing.T) {
	// The unit value is not an int — a mismatch fires E0201.
	src := `func main() {
  let n: int = ()
}
`
	if codes := runCheck(t, src); !contains(codes, "E0201") {
		t.Errorf("expected E0201 for unit-where-int, got %v", codes)
	}
}

func TestScopeSpawnClean(t *testing.T) {
	// T-ScopeExpr + T-Spawn: a well-formed scope with spawns and a
	// trailing value is clean.
	src := `func main() {
  let ch = makeChannel<int>(2)
  let total = scope<int, error> {
    spawn { ch.send(1); return Ok(()) }
    let a = ch.recv()
    a
  }
}
`
	if codes := runCheck(t, src); len(codes) != 0 {
		t.Errorf("clean scope/spawn produced diags: %v", codes)
	}
}

func TestSpawnOutsideScopeFiresE0405(t *testing.T) {
	src := `func main() {
  spawn { return Ok(()) }
}
`
	if codes := runCheck(t, src); !contains(codes, "E0405") {
		t.Errorf("expected E0405 for spawn outside scope, got %v", codes)
	}
}

func TestSpawnInClosureInsideScopeFiresE0405(t *testing.T) {
	// A closure boundary breaks the lexical scope enclosure: a spawn
	// inside a closure (which may run outside the scope) is illegal.
	src := `func main() {
  let r = scope<unit, error> {
    let f = () => spawn { return Ok(()) }
    f()
  }
}
`
	if codes := runCheck(t, src); !contains(codes, "E0405") {
		t.Errorf("expected E0405 for spawn inside a closure-in-scope, got %v", codes)
	}
}

func TestScopeNonErrorParamFiresE0407(t *testing.T) {
	src := `func main() {
  let r = scope<int, string> { 5 }
}
`
	if codes := runCheck(t, src); !contains(codes, "E0407") {
		t.Errorf("expected E0407 for scope<_, string>, got %v", codes)
	}
}

func TestSelectClean(t *testing.T) {
	// T-Select: all case forms clean; a recv binding takes the
	// channel's element type and is usable in the body.
	src := `import fmt
func main() {
  let a = makeChannel<int>(1)
  let b = makeChannel<int>(1)
  a.send(1)
  select {
    case v = <-a => { fmt.println(v + 1) },
    case <-b => {},
    case b.send(2) => {},
    default => {},
  }
}
`
	if codes := runCheck(t, src); len(codes) != 0 {
		t.Errorf("clean select produced diags: %v", codes)
	}
}

func TestSelectRecvBindUnknownNameStillResolves(t *testing.T) {
	// A use of the recv binding resolves (no E0103); referencing an
	// unbound name inside a case body still fires E0103.
	src := `func main() {
  let a = makeChannel<int>(1)
  select {
    case v = <-a => { let _ = missing },
  }
}
`
	if codes := runCheck(t, src); !contains(codes, "E0103") {
		t.Errorf("expected E0103 for unknown name in select body, got %v", codes)
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
