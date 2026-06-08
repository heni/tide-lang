package sema

import "testing"

// TestErrorAnnotationTypes locks that an `error` annotation resolves to
// the predeclared `error` builtin (the Go-error boundary type), not the
// conservative Unknown it used to be.
func TestErrorAnnotationTypes(t *testing.T) {
	info := checkInfo(t, `func f(e: error): error { return e }`+"\n")
	if got := defTypeByName(info, "e"); got == nil || got.String() != "error" {
		t.Errorf("param e type = %v; want error", got)
	}
}

// TestResultOptionAnnotationTypes locks that `Result<T,E>` / `Option<T>`
// annotations build the modelled parametrised types (no longer Unknown).
func TestResultOptionAnnotationTypes(t *testing.T) {
	info := checkInfo(t, `func f(r: Result<int, string>, o: Option<bool>) {
  let a = r
  let b = o
}
`)
	if got := defTypeByName(info, "r"); got == nil || got.String() != "Result<int, string>" {
		t.Errorf("r type = %v; want Result<int, string>", got)
	}
	if got := defTypeByName(info, "o"); got == nil || got.String() != "Option<bool>" {
		t.Errorf("o type = %v; want Option<bool>", got)
	}
}

// TestMatchResultPayloadTypes locks the keystone of the epoch: a match
// on a Result-typed subject types its Ok / Err payload bindings from the
// subject's T / E. The subject here is a user function's declared
// return type.
func TestMatchResultPayloadTypes(t *testing.T) {
	info := checkInfo(t, `func g(): Result<int, string> { Ok(1) }
func f(): string {
  match g() {
    Ok(n)  => "n",
    Err(e) => e,
  }
}
`)
	if got := defTypeByName(info, "n"); got == nil || got.String() != "int" {
		t.Errorf("Ok payload n type = %v; want int", got)
	}
	if got := defTypeByName(info, "e"); got == nil || got.String() != "string" {
		t.Errorf("Err payload e type = %v; want string", got)
	}
}

// TestMatchOptionPayloadType locks Some(v) payload typing over an
// Option-typed subject.
func TestMatchOptionPayloadType(t *testing.T) {
	info := checkInfo(t, `func g(): Option<int> { None() }
func f(): int {
  match g() {
    Some(v) => v,
    None    => 0,
  }
}
`)
	if got := defTypeByName(info, "v"); got == nil || got.String() != "int" {
		t.Errorf("Some payload v type = %v; want int", got)
	}
}

// TestMatchUserSumPayloadTypes locks payload typing for a user-declared
// sum type's variant fields.
func TestMatchUserSumPayloadTypes(t *testing.T) {
	info := checkInfo(t, `type Shape = | Circle(r: int) | Rect(w: int, h: string)
func area(s: Shape): string {
  match s {
    Circle(r)  => "c",
    Rect(w, h) => h,
  }
}
`)
	if got := defTypeByName(info, "w"); got == nil || got.String() != "int" {
		t.Errorf("Rect.w payload type = %v; want int", got)
	}
	if got := defTypeByName(info, "h"); got == nil || got.String() != "string" {
		t.Errorf("Rect.h payload type = %v; want string", got)
	}
}
