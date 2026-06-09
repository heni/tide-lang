package sema

import "testing"

// TestTopLevelLetInfersType locks that a module-level `let` with no
// annotation takes its initialiser's type, and that the type is
// visible from a function body that references it (name-resolution.md
// §File scope — top-level bindings are visible everywhere).
func TestTopLevelLetInfersType(t *testing.T) {
	src := `let version = 5
func f(): int {
  return version
}
`
	info := checkInfo(t, src)
	if got := defTypeByName(info, "version"); got == nil || got.String() != "int" {
		t.Errorf("version type = %v; want int", got)
	}
}

// TestTopLevelLetUseSiteTyped locks that a *use site* of a top-level
// constant reads back its concrete type (symValueType), not Unknown —
// the whole point of typing constants before bodies. Regression guard
// for the symValueType gap (PR #114 review WARNING-1): a local bound
// from a top-level constant must take the constant's type so codegen
// value-position inference and downstream checks see it.
func TestTopLevelLetUseSiteTyped(t *testing.T) {
	src := `let version = 5
func f() {
  let x = version
}
`
	info := checkInfo(t, src)
	if got := defTypeByName(info, "x"); got == nil || got.String() != "int" {
		t.Errorf("x (bound from top-level const) type = %v; want int", got)
	}
}

// TestTopLevelLetClean — an annotated + inferred pair, both referenced,
// produces no diagnostics.
func TestTopLevelLetClean(t *testing.T) {
	src := `let n = 5
let label: string = "tide"
func use(): string {
  return label
}
`
	if codes := runCheck(t, src); len(codes) != 0 {
		t.Errorf("clean top-level lets produced diags: %v", codes)
	}
}

// TestTopLevelLetAnnotationMismatch — the annotation must agree with
// the initialiser (T-Let / fits()), surfacing E0201.
func TestTopLevelLetAnnotationMismatch(t *testing.T) {
	src := `let label: string = 5
`
	if codes := runCheck(t, src); !contains(codes, "E0201") {
		t.Errorf("expected E0201 on annotation mismatch, got %v", codes)
	}
}

// TestTopLevelLetDuplicateName — a top-level `let` colliding with
// another top-level declaration is a duplicate (E0113).
func TestTopLevelLetDuplicateName(t *testing.T) {
	src := `let dup = 1
func dup(): int { return 2 }
`
	if codes := runCheck(t, src); !contains(codes, "E0113") {
		t.Errorf("expected E0113 on duplicate top-level name, got %v", codes)
	}
}
