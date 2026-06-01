package sema

import "strings"

// Type — closed sum of Tide-side type representations.
// See docs/internals/sema.md §5. The §5 catalogue grows by one
// case per PR as Barrier C learns to infer the corresponding
// shape; an un-modelled shape is represented by *Unknown so a
// half-built checker never reports a false positive.
type Type interface {
	typeMarker()
	String() string
}

// Builtin — predeclared primitive (int, string, …). Spec §5 calls
// this `Prim`; the Go-side spelling stays `Builtin` to match the
// predeclared-symbol seeding in builtins.go. `Any` and `Dynamic`
// ride this case as opaque builtins until the Dynamic PR gives
// them their own rules.
type Builtin struct{ N string }

func (*Builtin) typeMarker()      {}
func (b *Builtin) String() string { return b.N }

// Named — user type (class / sum) or opaque predeclared. Decl is
// the AST source node (*ast.ClassDecl / *ast.TypeDecl), nil for
// opaques. Equality is nominal (D14): two Named are equal iff
// their names match.
type Named struct {
	N    string
	Decl any
}

func (*Named) typeMarker()      {}
func (n *Named) String() string { return n.N }

// Func — a function / method signature. TypeParams lists generic
// parameter names (empty for monomorphic functions).
type Func struct {
	Params     []Type
	Return     Type
	TypeParams []string
}

func (*Func) typeMarker() {}
func (f *Func) String() string {
	parts := make([]string, len(f.Params))
	for i, p := range f.Params {
		parts[i] = p.String()
	}
	return "func(" + strings.Join(parts, ", ") + "): " + f.Return.String()
}

// Unit — the empty type (`unit`), the value of statements and of
// a function with no declared return.
type Unit struct{}

func (*Unit) typeMarker()    {}
func (*Unit) String() string { return "unit" }

// Never — the bottom type. `return` / `panic` / a diverging
// expression has type Never; it is assignable to every expected
// type (it never produces a value).
type Never struct{}

func (*Never) typeMarker()    {}
func (*Never) String() string { return "Never" }

// Unknown — the conservative wildcard. Stands for a type Barrier C
// cannot pin down yet (an un-modelled shape, an un-typed binding,
// a stdlib-binding result). Comparisons against Unknown never fire
// a diagnostic, so an incomplete checker stays sound-by-omission.
type Unknown struct{}

func (*Unknown) typeMarker()    {}
func (*Unknown) String() string { return "<unknown>" }

// isUnknown reports whether t is the wildcard (nil counts as
// unknown — a missing annotation / un-inferred slot).
func isUnknown(t Type) bool {
	if t == nil {
		return true
	}
	_, ok := t.(*Unknown)
	return ok
}

// equal is invariant type equality (§5 — no subtyping, no
// variance). Unknown is a wildcard on either side: equality with
// an un-pinned type is vacuously true so a partial checker emits
// no false positives.
func equal(a, b Type) bool {
	if isUnknown(a) || isUnknown(b) {
		return true
	}
	switch x := a.(type) {
	case *Builtin:
		y, ok := b.(*Builtin)
		return ok && x.N == y.N
	case *Named:
		y, ok := b.(*Named)
		return ok && x.N == y.N
	case *Unit:
		_, ok := b.(*Unit)
		return ok
	case *Never:
		_, ok := b.(*Never)
		return ok
	case *Func:
		y, ok := b.(*Func)
		if !ok || len(x.Params) != len(y.Params) {
			return false
		}
		for i := range x.Params {
			if !equal(x.Params[i], y.Params[i]) {
				return false
			}
		}
		return equal(x.Return, y.Return)
	default:
		return false
	}
}

// assignable reports whether a value of type `got` is admissible
// where `want` is expected. Invariant everywhere (= equal) except
// that Never (the bottom type) flows into any position.
func assignable(want, got Type) bool {
	if _, ok := got.(*Never); ok {
		return true
	}
	return equal(want, got)
}

// numericPrims is the closed set of numeric primitive names
// (type-system.md §Arithmetic — "numeric primitives").
var numericPrims = map[string]bool{
	"int": true, "int8": true, "int16": true, "int32": true, "int64": true,
	"uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true,
	"float32": true, "float64": true, "byte": true, "rune": true,
}

func isNumeric(t Type) bool {
	b, ok := t.(*Builtin)
	return ok && numericPrims[b.N]
}

func isBool(t Type) bool {
	b, ok := t.(*Builtin)
	return ok && b.N == "bool"
}

func isString(t Type) bool {
	b, ok := t.(*Builtin)
	return ok && b.N == "string"
}

// concrete reports whether t is pinned down enough to justify a
// diagnostic. Diagnostics fire only when both operands are
// concrete — an Unknown on either side stays silent.
func concrete(t Type) bool { return !isUnknown(t) }
