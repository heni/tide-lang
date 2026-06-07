package sema

import (
	"math"

	"github.com/heni/tide-lang/internal/ast"
)

// inferSliceLit types `[]T{...}` (annotated) and `[e1, ...]`
// (inferred) per T-Slice-Lit-*. An empty inferred literal has no
// element source to infer from; see the note below on E0208.
func (c *checker) inferSliceLit(s *ast.SliceLit) Type {
	if s.ElemType != nil {
		elem := c.typeFromExpr(s.ElemType)
		for _, it := range s.Items {
			at := c.inferExpr(it)
			if !c.fits(elem, it, at) {
				c.report("E0201", "Type mismatch — slice element is "+at.String()+", expected "+elem.String(), it.NodeSpan())
			}
		}
		return &Slice{Elem: elem}
	}
	if len(s.Items) == 0 {
		// Unreachable today: the grammar rejects a bare `[]`
		// (E0112). E0208 (cannot-infer-literal-type) lands with
		// the `T<...>{...}` BraceLit form the parser does not yet
		// accept; until then there is no element source to infer.
		return &Slice{Elem: &Unknown{}}
	}
	// Inferred form: all elements must agree; the first concrete
	// element type fixes the slice's element type.
	var elem Type = &Unknown{}
	for _, it := range s.Items {
		at := c.inferExpr(it)
		if isUnknown(elem) {
			elem = at
			continue
		}
		if concrete(elem) && concrete(at) && !assignable(elem, at) {
			c.report("E0201", "Type mismatch — slice elements disagree: "+elem.String()+" and "+at.String(), it.NodeSpan())
		}
	}
	return &Slice{Elem: elem}
}

// inferIndex types `recv[idx]` (T-Index-Slice / T-Index-Map).
func (c *checker) inferIndex(ix *ast.Index) Type {
	recv := c.inferExpr(ix.Receiver)
	idx := c.inferExpr(ix.Idx)
	switch r := recv.(type) {
	case *Slice:
		c.expectIntType(idx, ix.Idx)
		return r.Elem
	case *Map:
		if !c.fits(r.Key, ix.Idx, idx) {
			c.report("E0201", "Type mismatch — map key is "+idx.String()+", expected "+r.Key.String(), ix.Idx.NodeSpan())
		}
		return r.Val
	default:
		return &Unknown{}
	}
}

// expectInt infers an optional index bound and requires it to be
// int when present and concrete.
func (c *checker) expectInt(e ast.Expr) {
	if e == nil {
		return
	}
	c.expectIntType(c.inferExpr(e), e)
}

func (c *checker) expectIntType(t Type, e ast.Expr) {
	// Any integer primitive indexes a slice / bounds a re-slice
	// (rune is Go's int32, byte its uint8); only a concretely
	// non-integer index is an error.
	if b, ok := t.(*Builtin); ok && isIntegerPrim(b.N) {
		return
	}
	if concrete(t) {
		c.report("E0201", "Type mismatch — index must be an integer, got "+t.String(), e.NodeSpan())
	}
}

// inferBuiltinTypeCall types a call whose callee is a predeclared
// type name: either a primitive conversion `int(x)` (T-Conv,
// E0205) or a container constructor `Map<K,V>(...)` / `Set<T>(...)`
// / `Stack<T>(...)`.
func (c *checker) inferBuiltinTypeCall(name string, call *ast.Call, args []Type) Type {
	switch name {
	case "Map":
		if len(call.TypeArgs) == 2 {
			return &Map{Key: c.typeFromExpr(call.TypeArgs[0]), Val: c.typeFromExpr(call.TypeArgs[1])}
		}
		return &Unknown{}
	case "Set":
		if len(call.TypeArgs) == 1 {
			return &Set{Elem: c.typeFromExpr(call.TypeArgs[0])}
		}
		return &Unknown{}
	case "Stack":
		if len(call.TypeArgs) == 1 {
			return &Stack{Elem: c.typeFromExpr(call.TypeArgs[0])}
		}
		return &Unknown{}
	}
	if isConvertibleTarget(name) {
		if len(args) == 1 {
			if concrete(args[0]) && !convOK(name, args[0]) {
				c.report("E0205", "Illegal type conversion — cannot convert "+args[0].String()+" to "+name, call.Span)
			}
			return &Builtin{N: name}
		}
	}
	return &Unknown{}
}

// inferBuiltinFuncCall types calls to the predeclared free
// functions sema models: refEq (E0206) and makeSlice.
func (c *checker) inferBuiltinFuncCall(name string, call *ast.Call, args []Type) Type {
	switch name {
	case "refEq":
		// Only judge when both operands are concretely known; an
		// Unknown operand stays silent (conservative).
		if len(args) == 2 && concrete(args[0]) && concrete(args[1]) && !sameClass(args[0], args[1]) {
			c.report("E0206", "`refEq` requires class operands of the same class", call.Span)
		}
		return &Builtin{N: "bool"}
	case "makeSlice":
		if len(call.TypeArgs) == 1 {
			return &Slice{Elem: c.typeFromExpr(call.TypeArgs[0])}
		}
		return &Unknown{}
	}
	return &Unknown{}
}

// sameClass reports whether a and b are the same class type
// (both *Named backed by a *ast.ClassDecl with equal names).
func sameClass(a, b Type) bool {
	na, ok := a.(*Named)
	if !ok {
		return false
	}
	nb, ok := b.(*Named)
	if !ok {
		return false
	}
	_, aClass := na.Decl.(*ast.ClassDecl)
	_, bClass := nb.Decl.(*ast.ClassDecl)
	return aClass && bClass && na.N == nb.N
}

// isConvertibleTarget reports whether name is a primitive type
// that participates in T-Conv as a conversion target.
func isConvertibleTarget(name string) bool {
	return numericPrims[name] || name == "string"
}

// convOK encodes the closed ConvOK table (type-system.md §Conversions).
func convOK(target string, src Type) bool {
	b, ok := src.(*Builtin)
	switch target {
	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64", "byte":
		return ok && numericPrims[b.N]
	case "rune":
		return ok && isIntegerPrim(b.N)
	case "float32", "float64":
		return ok && numericPrims[b.N]
	case "string":
		// []byte, rune, or any integer codepoint.
		if s, isSlice := src.(*Slice); isSlice {
			if eb, ok := s.Elem.(*Builtin); ok && eb.N == "byte" {
				return true
			}
		}
		return ok && (b.N == "rune" || isIntegerPrim(b.N))
	default:
		return false
	}
}

func isIntegerPrim(name string) bool {
	switch name {
	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64", "byte", "rune":
		return true
	}
	return false
}

// unparen strips ParenExpr wrappers so literal-shape narrowing
// (int-literal, slice-literal) sees through author grouping like
// `(5)` or `([1, 2])`.
func unparen(e ast.Expr) ast.Expr {
	for {
		p, ok := e.(*ast.ParenExpr)
		if !ok {
			return e
		}
		e = p.Inner
	}
}

// intLiteralAdaptsTo reports whether expression e is an integer
// literal being placed at an integer target type. Such a literal
// narrows to the target (type-system.md §Literals) rather than
// being a type mismatch — the range is policed separately by
// checkIntLitRange (E0204).
func intLiteralAdaptsTo(target Type, e ast.Expr) bool {
	if _, ok := unparen(e).(*ast.IntLitExpr); !ok {
		return false
	}
	return isIntegerType(target)
}

// fits reports whether expression e (already inferred to `got`) is
// admissible at expected type `want`, applying bidirectional
// narrowing: an integer literal narrows to an integer `want`, and
// an inferred slice literal `[e1, ...]` narrows to a `Slice` want
// when every element narrows to the element type. On a successful
// narrow it updates Info.Type and runs the E0204 range check; it
// never emits E0201 itself — the caller owns the site-specific
// mismatch message. Element types are read from Info.Type (the
// literal was already inferred), so nothing is re-inferred and no
// diagnostic double-fires.
func (c *checker) fits(want Type, e ast.Expr, got Type) bool {
	if !concrete(want) || !concrete(got) {
		return true
	}
	// Dynamic / Any boundary rules take precedence over plain
	// equality so a concrete-vs-Dynamic site reports E0209/E0210/
	// E0212 rather than a generic E0201.
	if involvesDynamicOrAny(want, got) {
		return c.checkDynamicBoundary(want, got, e)
	}
	if assignable(want, got) {
		return true
	}
	// Interface conformance (D14, nominal): a class that `implements`
	// the interface — or an interface that `extends` it — fits.
	if c.satisfiesInterface(want, got) {
		return true
	}
	if intLiteralAdaptsTo(want, e) {
		c.info.Type[e] = want
		c.checkIntLitRange(want, e)
		return true
	}
	if w, ok := want.(*Slice); ok {
		if sl, ok := unparen(e).(*ast.SliceLit); ok && sl.ElemType == nil && len(sl.Items) > 0 {
			all := true
			for _, it := range sl.Items {
				if !c.fits(w.Elem, it, c.info.Type[it]) {
					all = false
				}
			}
			if all {
				c.info.Type[e] = want
				return true
			}
		}
	}
	return false
}

// checkIntLitRange fires E0204 when an integer literal is used at
// a sized-integer target it cannot fit (type-system.md §Literals
// narrowing). Only fires for a literal node against a concrete
// sized Builtin target; the default `int` is full-width and never
// narrows.
func (c *checker) checkIntLitRange(target Type, e ast.Expr) {
	lit, ok := unparen(e).(*ast.IntLitExpr)
	if !ok {
		return
	}
	b, ok := target.(*Builtin)
	if !ok {
		return
	}
	lo, hi, bounded := intBounds(b.N)
	if !bounded {
		return
	}
	if lit.Value < lo || lit.Value > hi {
		c.report("E0204", "Integer literal out of range for "+b.N, e.NodeSpan())
	}
}

// intBounds returns the inclusive bounds of a sized integer type.
// `int` / `int64` / `uint` / `uint64` are reported unbounded
// (bounded=false) because an int64-valued literal cannot exceed
// them in a way this check could observe.
func intBounds(name string) (lo, hi int64, bounded bool) {
	switch name {
	case "int8":
		return math.MinInt8, math.MaxInt8, true
	case "int16":
		return math.MinInt16, math.MaxInt16, true
	case "int32", "rune":
		return math.MinInt32, math.MaxInt32, true
	case "uint8", "byte":
		return 0, math.MaxUint8, true
	case "uint16":
		return 0, math.MaxUint16, true
	case "uint32":
		return 0, math.MaxUint32, true
	default:
		// int, int64, uint, uint64 — not observably narrowable.
		return 0, 0, false
	}
}
