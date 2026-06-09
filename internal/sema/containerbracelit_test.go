package sema

import "testing"

// Generic container brace literals (Set / Map / Stack) type from
// their type-args; a well-typed program produces no diagnostics.
func TestContainerBraceLitClean(t *testing.T) {
	src := `import fmt
func main() {
  let s = Set<int>{ 1, 2, 3 }
  let e = Set<int>{}
  let m = Map<string, int>{ "a": 1 }
  let st = Stack<int>{}
  fmt.println(s.len())
}
`
	if codes := runCheck(t, src); len(codes) != 0 {
		t.Errorf("clean container literals produced diags: %v", codes)
	}
}

// A set element that disagrees with the declared element type is a
// type mismatch (E0201).
func TestSetBraceLitElementMismatchFiresE0201(t *testing.T) {
	src := `func main() {
  let s = Set<int>{ 1, "two" }
}
`
	if codes := runCheck(t, src); !contains(codes, "E0201") {
		t.Errorf("expected E0201 on set element mismatch, got %v", codes)
	}
}

// A map value that disagrees with the declared value type is a type
// mismatch (E0201).
func TestMapBraceLitValueMismatchFiresE0201(t *testing.T) {
	src := `func main() {
  let m = Map<string, int>{ "a": "one" }
}
`
	if codes := runCheck(t, src); !contains(codes, "E0201") {
		t.Errorf("expected E0201 on map value mismatch, got %v", codes)
	}
}

// A Stack literal is always empty (ast.md §BraceLit); entries are an
// error (E0201).
func TestStackBraceLitWithEntriesFiresE0201(t *testing.T) {
	src := `func main() {
  let st = Stack<int>{ 1, 2 }
}
`
	if codes := runCheck(t, src); !contains(codes, "E0201") {
		t.Errorf("expected E0201 on non-empty Stack literal, got %v", codes)
	}
}
