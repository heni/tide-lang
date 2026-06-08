package sema

import (
	"testing"

	"github.com/heni/tide-lang/internal/lexer"
	"github.com/heni/tide-lang/internal/parser"
)

// checkInfo parses + checks src and returns the Info side-table, for
// tests that assert inferred types rather than diagnostics.
func checkInfo(t *testing.T, src string) *Info {
	t.Helper()
	toks, lerr := lexer.LexFile(src, "test.td")
	if lerr != nil {
		t.Fatalf("lex: %v", lerr)
	}
	f, perr := parser.ParseFile(toks, "test.td")
	if perr != nil {
		t.Fatalf("parse: %v", perr)
	}
	info, _ := Check(f, "test.td")
	return info
}

// defTypeByName returns the inferred type of the first binding whose
// Symbol has the given name, via the Info.Def back-reference. The
// Def-map walk order is non-deterministic, so callers must use a name
// that is unique in the source (or whose same-named symbols share a
// type) — true for every test below.
func defTypeByName(info *Info, name string) Type {
	for _, sym := range info.Def {
		if sym.Name == name {
			return sym.Type
		}
	}
	return nil
}

// TestForTupleBindingTypesSliceElems locks the element-typing of a
// two-variable tuple pattern over a slice: `for (idx, d) in xs` binds
// idx:int and d to the slice element type. Regression guard for the
// d03 value-position-if inference, which depends on these types
// flowing through to codegen.
func TestForTupleBindingTypesSliceElems(t *testing.T) {
	src := `func f(xs: []int) {
  for (idx, d) in xs {
    let a = idx
    let b = d
  }
}
`
	info := checkInfo(t, src)
	if got := defTypeByName(info, "idx"); got == nil || got.String() != "int" {
		t.Errorf("idx type = %v; want int", got)
	}
	if got := defTypeByName(info, "d"); got == nil || got.String() != "int" {
		t.Errorf("d type = %v; want int", got)
	}
}

// TestForSingleBindingTypesSliceElem: `for d in xs` binds d to the
// element type.
func TestForSingleBindingTypesSliceElem(t *testing.T) {
	src := `func f(xs: []string) {
  for d in xs {
    let a = d
  }
}
`
	info := checkInfo(t, src)
	if got := defTypeByName(info, "d"); got == nil || got.String() != "string" {
		t.Errorf("d type = %v; want string", got)
	}
}

// TestForMapTupleBindingTypesKeyVal: `for (k, v) in m` over a
// Map<string, int> binds k:string and v:int.
func TestForMapTupleBindingTypesKeyVal(t *testing.T) {
	src := `func f(m: Map<string, int>) {
  for (k, v) in m {
    let a = k
    let b = v
  }
}
`
	info := checkInfo(t, src)
	if got := defTypeByName(info, "k"); got == nil || got.String() != "string" {
		t.Errorf("k type = %v; want string", got)
	}
	if got := defTypeByName(info, "v"); got == nil || got.String() != "int" {
		t.Errorf("v type = %v; want int", got)
	}
}
