package codegen

import (
	"strings"
	"testing"

	"github.com/heni/tide-lang/internal/lexer"
	"github.com/heni/tide-lang/internal/parser"
)

// emitErr lexes/parses/emits src and returns the codegen error (nil on
// success) — the negative counterpart to emitString, which fatals.
func emitErr(t *testing.T, src string) error {
	t.Helper()
	toks, lerr := lexer.Lex(src)
	if lerr != nil {
		t.Fatalf("lex error: %v", lerr)
	}
	f, perr := parser.Parse(toks)
	if perr != nil {
		t.Fatalf("parse error: %v", perr)
	}
	_, err := Emit(f, "")
	return err
}

const tryExprPrelude = `import fmt
func sideA(): int { fmt.println("A"); return 1 }
func mk(): Result<int, string> { return Ok<int, string>(2) }
func combine(a: int, b: int): int { return a + b }
`

// TestTryExprHoistsWhenSafe — a `try` whose preceding siblings are pure
// (an adjacent try, a bare identifier) is hoisted to a preamble before
// the enclosing statement, with the node lowered to `<tmp>.V`.
func TestTryExprHoistsWhenSafe(t *testing.T) {
	got := emitString(t, tryExprPrelude+`func f(): Result<int, string> {
  let s = combine(try mk(), try mk())
  return Ok<int, string>(s)
}
`)
	// Two preambles emitted before the combine call, in order.
	if strings.Count(got, "if __tide_try_") < 2 {
		t.Errorf("expected two hoisted try preambles, got:\n%s", got)
	}
	if !strings.Contains(got, "combine(__tide_try_1.V, __tide_try_2.V)") {
		t.Errorf("expected both tries substituted by their temps, got:\n%s", got)
	}
}

// TestTryExprRefusesReorder — the C1 failure mode: a side-effecting
// expression (`sideA()`) is evaluated *before* a `try` in the same
// statement. Hoisting the try would defer (or, on bail, skip) that
// effect, so codegen must refuse rather than emit valid-but-reordered
// Go. A happy-path-only fixture (`combine(try, try)`) cannot catch this.
func TestTryExprRefusesReorder(t *testing.T) {
	err := emitErr(t, tryExprPrelude+`func f(): Result<int, string> {
  let s = combine(sideA(), try mk())
  return Ok<int, string>(s)
}
`)
	if err == nil {
		t.Fatal("expected codegen to refuse hoisting a try after a side-effecting expression (would reorder); got nil")
	}
	if !strings.Contains(err.Error(), "try") {
		t.Errorf("expected a `try`-position error, got: %v", err)
	}
}

// TestTryExprRefusesAfterPanicPoint — a panic-point (index out-of-range,
// division by zero) before a `try` is an *ordered observable effect*:
// hoisting the try ahead of it would let the try bail before the panic.
// Codegen refuses, keeping the conservative-by-construction property
// honest. `n + try b()` (pure left operand) stays hoistable.
func TestTryExprRefusesAfterPanicPoint(t *testing.T) {
	err := emitErr(t, tryExprPrelude+`func f(xs: []int): Result<int, string> {
  let s = xs[0] + try mk()
  return Ok<int, string>(s)
}
`)
	if err == nil {
		t.Fatal("expected refusal: an index panic-point precedes the try (ordering); got nil")
	}
}

// TestTryExprSafeAfterTryBeforeEffect — the mirror case: a `try`
// *before* a side-effecting sibling is safe (the try's preamble runs
// first, the sibling stays in place after it — original order), so it
// hoists.
func TestTryExprSafeAfterTryBeforeEffect(t *testing.T) {
	got := emitString(t, tryExprPrelude+`func f(): Result<int, string> {
  let s = combine(try mk(), sideA())
  return Ok<int, string>(s)
}
`)
	if !strings.Contains(got, "combine(__tide_try_1.V, sideA())") {
		t.Errorf("expected the leading try hoisted and sideA() left in place, got:\n%s", got)
	}
}
