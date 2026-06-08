package sema

import (
	"strings"

	"github.com/heni/tide-lang/internal/ast"
)

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

// Slice — `[]T`.
type Slice struct{ Elem Type }

func (*Slice) typeMarker()      {}
func (s *Slice) String() string { return "[]" + s.Elem.String() }

// Map — `Map<K, V>`.
type Map struct{ Key, Val Type }

func (*Map) typeMarker()      {}
func (m *Map) String() string { return "Map<" + m.Key.String() + ", " + m.Val.String() + ">" }

// Set — `Set<T>`.
type Set struct{ Elem Type }

func (*Set) typeMarker()      {}
func (s *Set) String() string { return "Set<" + s.Elem.String() + ">" }

// Stack — `Stack<T>`.
type Stack struct{ Elem Type }

func (*Stack) typeMarker()      {}
func (s *Stack) String() string { return "Stack<" + s.Elem.String() + ">" }

// Result — `Result<T, E>`, the predeclared value/error sum. Payload
// types feed match bindings: `Ok(v): T`, `Err(e): E` (builtins.md
// §Result). Equality is structural over the components.
type Result struct{ T, E Type }

func (*Result) typeMarker() {}
func (r *Result) String() string {
	return "Result<" + r.T.String() + ", " + r.E.String() + ">"
}

// Option — `Option<T>`, the predeclared optional sum. `Some(v): T`;
// `None` carries no payload (builtins.md §Option).
type Option struct{ T Type }

func (*Option) typeMarker()      {}
func (o *Option) String() string { return "Option<" + o.T.String() + ">" }

// Channel — `Channel<T>` (bidirectional). SendChan / RecvChan are
// the one-way widenings (builtins.md §Channel); Channel<T> widens
// implicitly into either at argument sites (T-Chan-Widen, enforced
// in fits()).
type Channel struct{ Elem Type }

func (*Channel) typeMarker()      {}
func (c *Channel) String() string { return "Channel<" + c.Elem.String() + ">" }

// SendChan — `SendChan<T>`, send-only widening of Channel<T>.
type SendChan struct{ Elem Type }

func (*SendChan) typeMarker()      {}
func (c *SendChan) String() string { return "SendChan<" + c.Elem.String() + ">" }

// RecvChan — `RecvChan<T>`, recv-only widening of Channel<T>.
type RecvChan struct{ Elem Type }

func (*RecvChan) typeMarker()      {}
func (c *RecvChan) String() string { return "RecvChan<" + c.Elem.String() + ">" }

// Tuple — `(A, B, ...)`, arity ≥ 2. Structural identity: two tuples
// are equal iff their components are pairwise equal.
type Tuple struct{ Comps []Type }

func (*Tuple) typeMarker() {}
func (t *Tuple) String() string {
	parts := make([]string, len(t.Comps))
	for i, c := range t.Comps {
		parts[i] = c.String()
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

// Dynamic — the runtime-erased reflection wrapper (RFC-0003 / D18).
// Governed by the introduction whitelist in dynamic.go; equality
// is nominal (only Dynamic equals Dynamic).
type Dynamic struct{}

func (*Dynamic) typeMarker()    {}
func (*Dynamic) String() string { return "Dynamic" }

// Any — Tide's other top-ish type. Shares no implicit-conversion
// path with Dynamic (E0212).
type Any struct{}

func (*Any) typeMarker()    {}
func (*Any) String() string { return "Any" }

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

// Generic — a generic type parameter (`T` in `func f<T>(...)`). It
// behaves as a wildcard for diagnostics (concrete() is false, so it
// never trips a mismatch in a generic body) but carries its name so
// call-site instantiation can substitute it. After substitution a
// Generic is replaced by the concrete type argument.
type Generic struct{ Name string }

func (*Generic) typeMarker()      {}
func (g *Generic) String() string { return g.Name }

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
	// An un-substituted generic parameter unifies with anything —
	// generic bodies are checked structurally, not instantiated.
	if isGeneric(a) || isGeneric(b) {
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
	case *Dynamic:
		_, ok := b.(*Dynamic)
		return ok
	case *Any:
		_, ok := b.(*Any)
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
	case *Slice:
		y, ok := b.(*Slice)
		return ok && equal(x.Elem, y.Elem)
	case *Map:
		y, ok := b.(*Map)
		return ok && equal(x.Key, y.Key) && equal(x.Val, y.Val)
	case *Set:
		y, ok := b.(*Set)
		return ok && equal(x.Elem, y.Elem)
	case *Stack:
		y, ok := b.(*Stack)
		return ok && equal(x.Elem, y.Elem)
	case *Channel:
		y, ok := b.(*Channel)
		return ok && equal(x.Elem, y.Elem)
	case *SendChan:
		y, ok := b.(*SendChan)
		return ok && equal(x.Elem, y.Elem)
	case *RecvChan:
		y, ok := b.(*RecvChan)
		return ok && equal(x.Elem, y.Elem)
	case *Result:
		y, ok := b.(*Result)
		return ok && equal(x.T, y.T) && equal(x.E, y.E)
	case *Option:
		y, ok := b.(*Option)
		return ok && equal(x.T, y.T)
	case *Tuple:
		y, ok := b.(*Tuple)
		if !ok || len(x.Comps) != len(y.Comps) {
			return false
		}
		for i := range x.Comps {
			if !equal(x.Comps[i], y.Comps[i]) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

// comparable reports whether `==` / `!=` is admissible on t
// (type-system.md T-Cmp / builtins.md §Comparable). Primitives and
// sum (nominal non-class) types are comparable; class types route
// to refEq, and slices / maps / sets / stacks / funcs are not
// comparable. Unknown is reported comparable so a half-typed
// operand never trips E0401 — callers also gate on concrete().
func comparable(t Type) bool {
	switch x := t.(type) {
	case *Builtin, *Unit, *Unknown, *Dynamic, *Any, *Generic:
		// Dynamic / Any operands sidestep the comparability
		// diagnostic (governed by E0209–E0212); an un-substituted
		// Generic is a wildcard, so it does not trip E0401 either.
		return true
	case *Named:
		// Class types are excluded (use refEq); sum / record
		// nominal types are comparable by tag / field-wise.
		if _, isClass := x.Decl.(*ast.ClassDecl); isClass {
			return false
		}
		return true
	case *Tuple:
		// A tuple is comparable iff every component is.
		for _, c := range x.Comps {
			if concrete(c) && !comparable(c) {
				return false
			}
		}
		return true
	case *Slice, *Map, *Set, *Stack, *Channel, *SendChan, *RecvChan, *Func, *Never, *Result, *Option:
		// Result / Option are matched, not compared with `==` in v1
		// (their payloads may themselves be non-comparable).
		return false
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

func isIntegerType(t Type) bool {
	b, ok := t.(*Builtin)
	return ok && isIntegerPrim(b.N)
}

func isDynamic(t Type) bool { _, ok := t.(*Dynamic); return ok }
func isAny(t Type) bool     { _, ok := t.(*Any); return ok }
func isGeneric(t Type) bool { _, ok := t.(*Generic); return ok }

// concrete reports whether t is pinned down enough to justify a
// diagnostic. Diagnostics fire only when both operands are
// concrete — an Unknown or an un-substituted Generic on either side
// stays silent.
func concrete(t Type) bool { return !isUnknown(t) && !isGeneric(t) }
