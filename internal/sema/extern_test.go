package sema

import "testing"

// Sema coverage for the Go-FFI surface (ffi.md). Positive cases must
// type cleanly; the opaque-handle invariants must fire their codes.

// TestExternFuncCallTypes — an extern function is callable like any
// function: its declared signature types the args and the result.
func TestExternFuncCallTypes(t *testing.T) {
	src := `extern type Cmd @go("os/exec")
extern func command(name: string): Cmd @go("os/exec.Command")
func run(): Cmd {
  return command("ls")
}
`
	if codes := runCheck(t, src); len(codes) != 0 {
		t.Errorf("expected no diags for a valid extern func call, got %v", codes)
	}
}

// TestExternFuncArgMismatch — a wrong argument type to an extern func
// is an ordinary E0201, proving the frozen signature is enforced.
func TestExternFuncArgMismatch(t *testing.T) {
	src := `extern func atoi(s: string): int @go("strconv.Atoi")
func bad(): int {
  return atoi(5)
}
`
	if codes := runCheck(t, src); !contains(codes, "E0201") {
		t.Errorf("expected E0201 on extern arg mismatch, got %v", codes)
	}
}

// TestExternMethodAndFieldAccess — a method on an opaque handle gives
// its Func type (callable), and an extern field gives its declared type.
func TestExternMethodAndFieldAccess(t *testing.T) {
	src := `extern type Cmd @go("os/exec")
extern func command(name: string): Cmd @go("os/exec.Command")
extern impl Cmd {
  run(): int @go("Run")
  var dir: string @go("Dir")
}
func use(): int {
  let c = command("ls")
  let d: string = c.dir
  return c.run()
}
`
	if codes := runCheck(t, src); len(codes) != 0 {
		t.Errorf("expected no diags for handle method/field access, got %v", codes)
	}
}

// TestExternMethodArgMismatch — calling a handle method with the wrong
// argument type surfaces E0201 against the method's signature.
func TestExternMethodArgMismatch(t *testing.T) {
	src := `extern type Buf @go("bytes")
extern func newBuf(): Buf @go("bytes.NewBuffer")
extern impl Buf {
  writeString(s: string): int @go("WriteString")
}
func bad(): int {
  let b = newBuf()
  return b.writeString(42)
}
`
	if codes := runCheck(t, src); !contains(codes, "E0201") {
		t.Errorf("expected E0201 on handle method arg mismatch, got %v", codes)
	}
}

// TestExternHandleRefEq — refEq admits two opaque handles of the same
// type (the T-RefEq relaxation), firing no E0206.
func TestExternHandleRefEq(t *testing.T) {
	src := `extern type Cmd @go("os/exec")
extern func command(name: string): Cmd @go("os/exec.Command")
func same(): bool {
  let a = command("ls")
  let b = command("ls")
  return refEq(a, b)
}
`
	if codes := runCheck(t, src); contains(codes, "E0206") {
		t.Errorf("refEq on two same-type handles should not fire E0206, got %v", codes)
	}
}

// TestExternHandleRefEqMismatch — refEq across two different handle
// types still fires E0206.
func TestExternHandleRefEqMismatch(t *testing.T) {
	src := `extern type Cmd @go("os/exec")
extern type Buf @go("bytes")
extern func command(name: string): Cmd @go("os/exec.Command")
extern func newBuf(): Buf @go("bytes.NewBuffer")
func bad(): bool {
  let a = command("ls")
  let b = newBuf()
  return refEq(a, b)
}
`
	if codes := runCheck(t, src); !contains(codes, "E0206") {
		t.Errorf("expected E0206 on cross-handle refEq, got %v", codes)
	}
}

// TestExternHandleNotConstructible — a handle cannot be built by a Tide
// call (E1001); it is only obtained from an extern function.
func TestExternHandleNotConstructible(t *testing.T) {
	src := `extern type Cmd @go("os/exec")
func bad(): Cmd {
  return Cmd("ls")
}
`
	if codes := runCheck(t, src); !contains(codes, "E1001") {
		t.Errorf("expected E1001 on opaque-handle construction, got %v", codes)
	}
}

// TestExternHandleNotBraceLit — the brace-literal construction form
// `T { … }` is rejected with E1001, like the call form.
func TestExternHandleNotBraceLit(t *testing.T) {
	src := `extern type Cmd @go("os/exec")
func bad(): int {
  let c = Cmd {}
  return 0
}
`
	if codes := runCheck(t, src); !contains(codes, "E1001") {
		t.Errorf("expected E1001 on opaque-handle brace literal, got %v", codes)
	}
}

// TestExternHandleNotEqComparable — `==` on a handle is illegal (it is
// excluded from structural comparison and routed to refEq): E0401.
func TestExternHandleNotEqComparable(t *testing.T) {
	src := `extern type Cmd @go("os/exec")
extern func command(name: string): Cmd @go("os/exec.Command")
func bad(): bool {
  let a = command("ls")
  let b = command("ls")
  return a == b
}
`
	if codes := runCheck(t, src); !contains(codes, "E0401") {
		t.Errorf("expected E0401 on `==` over opaque handles, got %v", codes)
	}
}

// TestExternHandleNotDestructurable — a handle has no visible layout, so
// a tuple pattern over it fires E1002.
func TestExternHandleNotDestructurable(t *testing.T) {
	src := `extern type Cmd @go("os/exec")
extern func command(name: string): Cmd @go("os/exec.Command")
func bad(): int {
  let (a, b) = command("ls")
  return 0
}
`
	if codes := runCheck(t, src); !contains(codes, "E1002") {
		t.Errorf("expected E1002 on opaque-handle destructure, got %v", codes)
	}
}

// TestExternImplUnknownType — `extern impl` naming a non-extern-type
// handle is E0103.
func TestExternImplUnknownType(t *testing.T) {
	src := `extern impl Ghost {
  run(): int @go("Run")
}
`
	if codes := runCheck(t, src); !contains(codes, "E0103") {
		t.Errorf("expected E0103 for extern impl on unknown type, got %v", codes)
	}
}
