package sema

import "testing"

// Container-method return typing (T-Container-Method). Before this, a
// `Map`/`Set`/`Stack`/`[]T` method call typed Unknown, so a match
// payload over `m.get(k)` was untyped and downstream tuple/constructor
// inference failed.

func TestMapGetPayloadTypesValue(t *testing.T) {
	src := `func f(m: Map<int, string>, k: int) {
  match m.get(k) {
    Some(v) => { let a = v },
    None    => {},
  }
}
`
	info := checkInfo(t, src)
	if got := defTypeByName(info, "v"); got == nil || got.String() != "string" {
		t.Errorf("Some(v) payload = %v; want string", got)
	}
}

func TestStackPopPayloadTypesElem(t *testing.T) {
	src := `func f(s: Stack<int>) {
  match s.pop() {
    Ok(v)  => { let a = v },
    Err(_) => {},
  }
}
`
	info := checkInfo(t, src)
	if got := defTypeByName(info, "v"); got == nil || got.String() != "int" {
		t.Errorf("Ok(v) payload = %v; want int", got)
	}
}

func TestSetHasIsBool(t *testing.T) {
	// `s.has(e)` typed bool: a non-bool `if` head would otherwise be
	// silently Unknown; here the method result feeds a clean program.
	src := `func f(s: Set<int>): int {
  if s.has(1) { return s.len() }
  return 0
}
`
	if codes := runCheck(t, src); len(codes) != 0 {
		t.Errorf("expected clean (has:bool, len:int), got %v", codes)
	}
}

func TestMapKeysIsSliceOfKey(t *testing.T) {
	src := `func f(m: Map<int, string>): int {
  var total = 0
  for k in m.keys() { total = total + k }
  return total
}
`
	if codes := runCheck(t, src); len(codes) != 0 {
		t.Errorf("expected clean (keys:[]int), got %v", codes)
	}
}

func TestSlicePushReturnsSlice(t *testing.T) {
	src := `func f(xs: []int): []int { return xs.push(1) }
`
	if codes := runCheck(t, src); len(codes) != 0 {
		t.Errorf("expected clean (push:[]int), got %v", codes)
	}
}
