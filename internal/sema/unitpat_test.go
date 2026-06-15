package sema

import "testing"

// UnitPat (`()`) is irrefutable and binds nothing; a single-arm match on
// the unit value type-checks cleanly and yields the arm's value type.

func TestUnitPatMatchClean(t *testing.T) {
	src := `func choose(): int {
  return match () {
    () => 2,
  }
}
`
	if codes := runCheck(t, src); len(codes) != 0 {
		t.Errorf("expected clean, got %v", codes)
	}
}
