package sema

import "testing"

// Sema coverage for variadic parameters (`...T`) and call-site spread
// (`...xs`) — ffi.md §Variadic, diagnostics E0202 / E0213.

// TestVariadicCallTypes — a variadic function accepts zero or more
// trailing arguments of the element type; the parameter is in scope as
// a slice `[]T`.
func TestVariadicCallTypes(t *testing.T) {
	src := `func sum(label: string, nums: ...int): int {
  var total = 0
  for n in nums { total = total + n }
  return total
}
func main() {
  let _ = sum("a", 1, 2, 3)
  let _ = sum("b")
}
`
	if codes := runCheck(t, src); len(codes) != 0 {
		t.Errorf("expected no diags for a valid variadic call, got %v", codes)
	}
}

// TestVariadicElemMismatch — a trailing argument of the wrong element
// type fires E0201 against the element type, not the slice.
func TestVariadicElemMismatch(t *testing.T) {
	src := `func sum(nums: ...int): int { return 0 }
func main() { let _ = sum(1, "two") }
`
	if codes := runCheck(t, src); !contains(codes, "E0201") {
		t.Errorf("expected E0201 on variadic element mismatch, got %v", codes)
	}
}

// TestVariadicTooFewFixed — fewer arguments than the fixed parameters
// is E0202 (at least N).
func TestVariadicTooFewFixed(t *testing.T) {
	src := `func f(a: int, rest: ...int) {}
func main() { f() }
`
	if codes := runCheck(t, src); !contains(codes, "E0202") {
		t.Errorf("expected E0202 on too-few fixed args, got %v", codes)
	}
}

// TestSpreadIntoVariadic — `...xs` of the matching slice type forwards
// cleanly into a variadic parameter (no diagnostics).
func TestSpreadIntoVariadic(t *testing.T) {
	src := `func sum(nums: ...int): int { return 0 }
func main() {
  let xs = [1, 2, 3]
  let _ = sum(...xs)
}
`
	if codes := runCheck(t, src); len(codes) != 0 {
		t.Errorf("expected no diags for a valid spread, got %v", codes)
	}
}

// TestSpreadElemMismatch — spreading a slice of the wrong element type
// fires E0201 against the `[]T` parameter.
func TestSpreadElemMismatch(t *testing.T) {
	src := `func sum(nums: ...int): int { return 0 }
func main() {
  let xs = ["a", "b"]
  let _ = sum(...xs)
}
`
	if codes := runCheck(t, src); !contains(codes, "E0201") {
		t.Errorf("expected E0201 on spread element mismatch, got %v", codes)
	}
}

// TestSpreadRequiresVariadic — a spread into a non-variadic callee is
// E0213.
func TestSpreadRequiresVariadic(t *testing.T) {
	src := `func f(a: int, b: int) {}
func main() {
  let xs = [1, 2]
  f(...xs)
}
`
	codes := runCheck(t, src)
	if !contains(codes, "E0213") {
		t.Errorf("expected E0213 on spread into non-variadic, got %v", codes)
	}
	if contains(codes, "E0202") {
		t.Errorf("a spread call should not also report E0202 arity, got %v", codes)
	}
}

// TestExternVariadicCall — the motivating FFI case: an extern variadic
// binding (exec.Command-shaped) accepts both spread and inline trailing
// arguments without diagnostics.
func TestExternVariadicCall(t *testing.T) {
	src := `extern type Cmd @go("os/exec")
extern func command(name: string, args: ...string): Cmd @go("os/exec.Command")
func use(): Cmd {
  let extra = ["a", "b"]
  let _ = command("echo", "x", "y")
  return command("echo", ...extra)
}
`
	if codes := runCheck(t, src); len(codes) != 0 {
		t.Errorf("expected no diags for extern variadic call, got %v", codes)
	}
}
