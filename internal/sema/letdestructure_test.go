package sema

import "testing"

// Tuple-destructuring let (T-Let-Destructure): the pattern's binders
// take the value's tuple components positionally; refutable / arity /
// non-tuple shapes are rejected.

func TestLetDestructureTypesComponents(t *testing.T) {
	src := `func pair(): (int, string) { return (1, "x") }
func f() {
  let (n, s) = pair()
  let a = n
  let b = s
}
`
	info := checkInfo(t, src)
	if got := defTypeByName(info, "n"); got == nil || got.String() != "int" {
		t.Errorf("n = %v; want int", got)
	}
	if got := defTypeByName(info, "s"); got == nil || got.String() != "string" {
		t.Errorf("s = %v; want string", got)
	}
}

func TestLetDestructureWildcardClean(t *testing.T) {
	src := `func pair(): (int, int) { return (1, 2) }
func f() { let (a, _) = pair() }
`
	if codes := runCheck(t, src); len(codes) != 0 {
		t.Errorf("expected clean, got %v", codes)
	}
}

func TestLetDestructureArityMismatchFiresE0201(t *testing.T) {
	src := `func pair(): (int, int) { return (1, 2) }
func f() { let (a, b, c) = pair() }
`
	if codes := runCheck(t, src); !contains(codes, "E0201") {
		t.Errorf("expected E0201 (arity), got %v", codes)
	}
}

func TestLetDestructureNonTupleFiresE0201(t *testing.T) {
	src := `func f() { let (a, b) = 5 }
`
	if codes := runCheck(t, src); !contains(codes, "E0201") {
		t.Errorf("expected E0201 (non-tuple), got %v", codes)
	}
}

func TestLetDestructureRefutableFiresE0201(t *testing.T) {
	src := `func pair(): (int, int) { return (1, 2) }
func f() { let (a, 2) = pair() }
`
	if codes := runCheck(t, src); !contains(codes, "E0201") {
		t.Errorf("expected E0201 (refutable), got %v", codes)
	}
}
