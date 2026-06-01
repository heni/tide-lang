package sema

import "github.com/heni/tide-lang/internal/ast"

// Dynamic-doesn't-leak — the introduction whitelist.
// See docs/internals/sema.md §6.1 and lang-spec/type-system.md
// §Dynamic. A `Dynamic` value is introduced only at the two
// allowed sites (reflect.box, and a reflect.* parameter of formal
// type Dynamic); everywhere else widening a concrete `T` into
// `Dynamic` is E0209 and narrowing back is E0210. `Any` and
// `Dynamic` never implicitly convert (E0212).
//
// Enforcement piggybacks on fits() (the single value-vs-expected
// chokepoint). The reflect.* implicit-widening sites are *not*
// routed through fits — their callee type is Unknown (a stdlib
// binding), so checkArgTypes never compares their arguments, which
// is exactly the admitted-introduction behaviour.

// involvesDynamicOrAny reports whether either side is Dynamic/Any,
// i.e. whether the boundary rules apply instead of plain equality.
func involvesDynamicOrAny(want, got Type) bool {
	return isDynamic(want) || isDynamic(got) || isAny(want) || isAny(got)
}

// checkDynamicBoundary applies the Dynamic / Any boundary rules at
// a value-vs-expected site. It assumes both types are concrete and
// at least one is Dynamic/Any. It emits the appropriate diagnostic
// and always reports the site as "handled" (true) so the caller
// does not additionally emit a generic E0201 — the specific
// E0209/E0210/E0212 is the right diagnostic.
func (c *checker) checkDynamicBoundary(want, got Type, e ast.Expr) bool {
	wD, gD := isDynamic(want), isDynamic(got)
	wA, gA := isAny(want), isAny(got)

	span := e.NodeSpan()

	switch {
	case (wD && gA) || (wA && gD):
		c.report("E0212", "`Any` and `Dynamic` cannot be implicitly converted — narrow to a concrete type and re-box", span)
	case wD && !gD:
		// Concrete T widening into Dynamic outside a reflect.*
		// parameter (those never reach fits).
		c.report("E0209", "`Dynamic` widening requires `reflect.box` — wrap the value in reflect.box(v)", span)
	case gD && !wD:
		// Dynamic narrowing into a concrete type.
		c.report("E0210", "`Dynamic` narrowing requires `reflect.unbox` — recover the value with reflect.unbox<T>(d)", span)
	default:
		// Dynamic == Dynamic, Any == Any, or Any paired with a
		// concrete non-Dynamic type: admitted (Any is top-ish).
	}
	return true
}
