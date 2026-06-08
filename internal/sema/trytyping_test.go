package sema

import (
	"testing"

	"github.com/heni/tide-lang/internal/lexer"
	"github.com/heni/tide-lang/internal/parser"
)

// codesOf returns the E-codes reported for src, for negative tests.
func codesOf(t *testing.T, src string) []string {
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
	var codes []string
	for _, d := range diags {
		codes = append(codes, d.Code)
	}
	return codes
}

func hasCode(codes []string, code string) bool {
	for _, c := range codes {
		if c == code {
			return true
		}
	}
	return false
}

// TestStdlibBindingReturnTyped locks that a tabled stdlib Result binding
// (strconv.atoi) types as Result<int, error>, so its match payloads and
// a `try` over it are typed (not Unknown).
func TestStdlibBindingReturnTyped(t *testing.T) {
	info := checkInfo(t, `import strconv
func f(s: string): Result<int, error> {
  let n = try strconv.atoi(s)
  match strconv.atoi(s) {
    Ok(v)  => Ok(v),
    Err(e) => Err(e),
  }
}
`)
	if got := defTypeByName(info, "n"); got == nil || got.String() != "int" {
		t.Errorf("try strconv.atoi result n = %v; want int", got)
	}
	if got := defTypeByName(info, "v"); got == nil || got.String() != "int" {
		t.Errorf("Ok payload v = %v; want int", got)
	}
	if got := defTypeByName(info, "e"); got == nil || got.String() != "error" {
		t.Errorf("Err payload e = %v; want error", got)
	}
}

// TestTryErrorTypeMatch — no E0403 when the inner `try` error type
// equals the enclosing function's error type.
func TestTryErrorTypeMatch(t *testing.T) {
	codes := codesOf(t, `import strconv
func f(s: string): Result<int, error> {
  let n = try strconv.atoi(s)
  return Ok(n)
}
`)
	if hasCode(codes, "E0403") {
		t.Errorf("unexpected E0403 when error types agree: %v", codes)
	}
}

// TestTryErrorTypeMismatch — E0403 fires when the inner `try` error type
// (error, from strconv.atoi) differs from the enclosing function's error
// type (a user class). v1 has no implicit error conversion (G11).
func TestTryErrorTypeMismatch(t *testing.T) {
	codes := codesOf(t, `import strconv
class MyErr implements error {
  error(): string { return "e" }
}
func f(s: string): Result<int, MyErr> {
  let n = try strconv.atoi(s)
  return Ok(n)
}
`)
	if !hasCode(codes, "E0403") {
		t.Errorf("expected E0403 for try error-type mismatch; got %v", codes)
	}
}

// TestTryOptionUnwrap — `try` over an Option<T> unwraps to T.
func TestTryOptionUnwrap(t *testing.T) {
	info := checkInfo(t, `func g(): Option<int> { None() }
func f(): Option<int> {
  let n = try g()
  return Some(n)
}
`)
	if got := defTypeByName(info, "n"); got == nil || got.String() != "int" {
		t.Errorf("try Option result n = %v; want int", got)
	}
}
