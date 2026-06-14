package codegen

import (
	"strings"
	"testing"

	"github.com/heni/tide-lang/internal/lexer"
	"github.com/heni/tide-lang/internal/parser"
)

// Regression tests for the tuple value-switch component-ident scoping
// (PR #142 review W1): a tuple component's variant-vs-fresh-binding
// resolution must be scoped to that component's own sum type, not the
// global variant table — else a name colliding with an unrelated sum's
// variant is mis-lowered.

// A lowercase fresh-binding ident whose name collides with another
// sum's (lowercase) variant must lower as a binding, not a tag test.
func TestTupleMatchFreshBindingNotForeignVariantTag(t *testing.T) {
	src := `import fmt
type A = | P | Q
type B = | r | s
func tag(a: A, flag: bool): int {
  match (a, flag) {
    (P, true)  => 10,
    (other, _) => rank(other),
  }
}
func rank(a: A): int {
  match a { P => 1, Q => 2 }
}
func main() {
  fmt.println(tag(Q, false))
}
`
	got := emitString(t, src)
	// The catch-all arm binds `other` to the captured component and must
	// NOT test `.Tag` against B.r's tag (which would shadow P and drop Q).
	if !strings.Contains(got, "other := __tide_match_1") {
		t.Errorf("expected `other` bound as a fresh component binding, got:\n%s", got)
	}
	// Exactly one tag test (arm 1's P); the fresh-binding arm is a default.
	if n := strings.Count(got, ".Tag == "); n != 1 {
		t.Errorf("expected 1 tag test (P only), got %d:\n%s", n, got)
	}
	if !strings.Contains(got, "default:") {
		t.Errorf("expected the fresh-binding arm to lower to `default:`, got:\n%s", got)
	}
}

// A capitalised constructor naming a variant of a *different* sum than
// the matched component's type is a mismatched constructor — codegen
// fails loudly rather than emitting a wrong-type tag test.
func TestTupleMatchMismatchedConstructorErrors(t *testing.T) {
	src := `import fmt
type A = | P | Q
type B = | R | S
func f(a: A): int {
  match (a, a) {
    (P, _) => 1,
    (R, _) => 2,
  }
}
func main() { fmt.println(f(Q)) }
`
	toks, lerr := lexer.Lex(src)
	if lerr != nil {
		t.Fatalf("lex: %v", lerr)
	}
	file, perr := parser.Parse(toks)
	if perr != nil {
		t.Fatalf("parse: %v", perr)
	}
	_, err := Emit(file, "")
	if err == nil {
		t.Fatalf("expected a mismatched-constructor error, got none")
	}
	if !strings.Contains(err.Error(), "mismatched constructor") {
		t.Errorf("expected a mismatched-constructor error, got: %v", err)
	}
}
