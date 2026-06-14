package sema

import "testing"

// Tuple-of-sums value-switch exhaustiveness (checkTupleExhaustive) and
// tuple-arm payload binding (typeMatchTuplePayload). The soundness
// invariant under test: the checker over-approximates coverage, so it
// reports E0303 only on a genuinely-missing combination and bails
// (silently) on any pattern shape it cannot model.

const tupleExhaustPrelude = `type AB = | A | B
type XY = | X | Y(n: int)
`

func TestTupleMatchExhaustivePasses(t *testing.T) {
	src := tupleExhaustPrelude + `func f(p: AB, q: XY): int {
  match (p, q) {
    (A, X)    => 1,
    (A, Y(n)) => n,
    (B, X)    => 3,
    (B, Y(_)) => 4,
  }
}
`
	if codes := runCheck(t, src); len(codes) != 0 {
		t.Errorf("expected exhaustive tuple match, got %v", codes)
	}
}

func TestTupleMatchMissingComboFiresE0303(t *testing.T) {
	// Drops (B, Y) — a genuinely unmatched cell.
	src := tupleExhaustPrelude + `func f(p: AB, q: XY): int {
  match (p, q) {
    (A, X)    => 1,
    (A, Y(n)) => n,
    (B, X)    => 3,
  }
}
`
	if codes := runCheck(t, src); !contains(codes, "E0303") {
		t.Errorf("expected E0303 (missing (B, Y)), got %v", codes)
	}
}

func TestTupleMatchWildcardRectangleCovers(t *testing.T) {
	// A wildcard / fresh-ident component is a full-row rectangle:
	// `(B, _)` covers both X and Y, so the match is exhaustive.
	src := tupleExhaustPrelude + `func f(p: AB, q: XY): int {
  match (p, q) {
    (A, X)    => 1,
    (A, Y(_)) => 2,
    (B, _)    => 3,
  }
}
`
	if codes := runCheck(t, src); len(codes) != 0 {
		t.Errorf("expected exhaustive (B,_ covers the row), got %v", codes)
	}
}

func TestTupleMatchLiteralComponentBailsSilently(t *testing.T) {
	// A literal component (here an int subject) is a refining pattern
	// the over-approximation cannot model — the whole check must bail
	// rather than risk a false E0303, even though this match is in fact
	// non-exhaustive over the int domain.
	src := tupleExhaustPrelude + `func f(p: AB, n: int): int {
  match (p, n) {
    (A, 0) => 1,
    (A, _) => 2,
    (B, _) => 3,
  }
}
`
	if codes := runCheck(t, src); contains(codes, "E0303") {
		t.Errorf("literal-component tuple match must not fire E0303 (sound bail), got %v", codes)
	}
}

func TestTupleMatchBoolComponentExhaustive(t *testing.T) {
	// bool is a finite-ctor dimension (true/false).
	src := tupleExhaustPrelude + `func f(p: AB, b: bool): int {
  match (p, b) {
    (A, true)  => 1,
    (A, false) => 2,
    (B, true)  => 3,
    (B, false) => 4,
  }
}
`
	if codes := runCheck(t, src); len(codes) != 0 {
		t.Errorf("expected exhaustive (bool covered both sides), got %v", codes)
	}
}

func TestTupleMatchBoolComponentMissingFiresE0303(t *testing.T) {
	src := tupleExhaustPrelude + `func f(p: AB, b: bool): int {
  match (p, b) {
    (A, true)  => 1,
    (A, false) => 2,
    (B, true)  => 3,
  }
}
`
	if codes := runCheck(t, src); !contains(codes, "E0303") {
		t.Errorf("expected E0303 (missing (B, false)), got %v", codes)
	}
}

func TestTupleMatchPayloadBindingTyped(t *testing.T) {
	// `n` bound in `(A, Y(n))` must be typed int (Y's field), so the
	// arithmetic body type-checks with no diagnostic. A regression here
	// (n left Unknown) would not error, so we assert the clean run plus
	// a use that would fault if n were a non-int — string concat.
	src := tupleExhaustPrelude + `func f(p: AB, q: XY): string {
  match (p, q) {
    (A, Y(n)) => intToStr(n),
    (_, _)    => "other",
  }
}
func intToStr(x: int): string { return "x" }
`
	if codes := runCheck(t, src); len(codes) != 0 {
		t.Errorf("expected clean (n typed int from Y's payload), got %v", codes)
	}
}
