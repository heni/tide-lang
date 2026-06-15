package sema

import "testing"

// Record-destructure pattern (P-Record): a `V(Type{ field: pat, … })`
// pattern types each field's sub-pattern from the record field's
// declared type. Nested tuple / wildcard sub-patterns compose.

const recordPatSrc = `type Info = {
  at:  (int, int)
  tag: string
}
type Step = | Move(info: Info) | Stop(info: Info, why: string)
func describe(s: Step): int {
  return match s {
    Move(Info{ at: (x, y), tag: t }) => x + y,
    Stop(Info{ at: (a, b), tag: _ }, why) => a - b,
  }
}
`

func TestRecordPatBindsFieldTypes(t *testing.T) {
	info := checkInfo(t, recordPatSrc)
	for _, name := range []string{"x", "y", "a", "b"} {
		if got := defTypeByName(info, name); got == nil || got.String() != "int" {
			t.Errorf("%s = %v; want int", name, got)
		}
	}
	if got := defTypeByName(info, "t"); got == nil || got.String() != "string" {
		t.Errorf("t = %v; want string", got)
	}
	if got := defTypeByName(info, "why"); got == nil || got.String() != "string" {
		t.Errorf("why = %v; want string", got)
	}
}

func TestRecordPatClean(t *testing.T) {
	if codes := runCheck(t, recordPatSrc); len(codes) != 0 {
		t.Errorf("expected clean, got %v", codes)
	}
}

// A float-literal sub-pattern nested in a record field is still rejected
// (E0305) — checkNoFloatPat recurses through RecordPat fields.
func TestRecordPatFloatFieldFiresE0305(t *testing.T) {
	src := `type P = { x: float }
type Box = | Holds(p: P) | Empty
func f(b: Box): int {
  return match b {
    Holds(P{ x: 1.5 }) => 1,
    _ => 0,
  }
}
`
	if codes := runCheck(t, src); !contains(codes, "E0305") {
		t.Errorf("expected E0305 (float pattern), got %v", codes)
	}
}
