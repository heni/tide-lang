package sema

import (
	"strings"

	"github.com/heni/tide-lang/internal/ast"
)

// Barrier D — exhaustiveness + reachability over a single match.
// See docs/internals/sema.md §4.2 and lang-spec/type-system.md
// §match. v1 does variant-level coverage (a sum variant is covered
// when some arm matches its constructor); full nested Maranget
// refinement is deferred, so coverage is *under*-approximated —
// the checker never rejects a genuinely-exhaustive match.
//
// Reachable diagnostics: E0303 (non-exhaustive), E0304 (unreachable
// arm). E0305 (float-literal patterns) fires from checkNoFloatPat in
// inferMatch, not here.

// checkExhaustive validates one match against its subject type.
func (c *checker) checkExhaustive(m *ast.MatchExpr, subject Type) {
	// First catch-all arm (wildcard or bare ident) makes every
	// later arm unreachable and the match trivially exhaustive.
	catchAll := -1
	for i, arm := range m.Arms {
		if isCatchAll(arm.Pattern) {
			catchAll = i
			break
		}
	}
	if catchAll >= 0 {
		for _, arm := range m.Arms[catchAll+1:] {
			c.report("E0304", "Unreachable arm — an earlier pattern already covers it", arm.Span)
		}
		return
	}

	switch s := subject.(type) {
	case *Named:
		td, ok := s.Decl.(*ast.TypeDecl)
		if !ok {
			return
		}
		body, ok := td.Body.(*ast.SumTypeBody)
		if !ok {
			return
		}
		c.checkSumExhaustive(m, body)
	case *Builtin:
		if s.N == "bool" {
			c.checkBoolExhaustive(m)
		}
		// Other primitives have an effectively infinite domain;
		// without a catch-all they cannot be exhaustively matched,
		// but enumerating coverage is out of scope for v1.
	case *Tuple:
		c.checkTupleExhaustive(m, s.Comps)
	}
}

// tupleExhaustCellLimit bounds the cartesian product enumerated by
// checkTupleExhaustive — a guard against a pathological many-component
// tuple of large sums. Past it the check bails (no report), keeping the
// "never reject a genuinely-exhaustive match" invariant.
const tupleExhaustCellLimit = 4096

// checkTupleExhaustive reports a non-exhaustive tuple-of-sums match
// (E0303). Soundness rule (exhaust.go invariant): it *over-approximates
// coverage* and so can only ever under-report — a refining component
// pattern it cannot model (a literal, a nested tuple, or a component
// whose type is not a finite-constructor sum/bool) makes it **bail the
// whole check** rather than risk a false rejection.
//
// Each component type contributes a finite constructor set (sum variant
// names, or bool true/false). Each arm covers a *rectangle*: the
// per-component set of constructors it admits (a VariantPat or a
// variant-named ident → that one constructor; a wildcard or a
// fresh-ident binding → every constructor of that component). The match
// is exhaustive iff the arms' rectangles tile the full cartesian
// product of the component constructor sets. Per type-system.md
// §T-Match-Tuple-Exhaustive.
func (c *checker) checkTupleExhaustive(m *ast.MatchExpr, comps []Type) {
	dims := make([][]string, len(comps))
	for j, comp := range comps {
		ctors := finiteCtors(comp)
		if ctors == nil {
			return // non-enumerable component → bail (stay sound)
		}
		dims[j] = ctors
	}
	cells := 1
	for _, d := range dims {
		cells *= len(d)
		if cells == 0 || cells > tupleExhaustCellLimit {
			return // empty or too large → bail
		}
	}
	// index lookup per dimension
	idx := make([]map[string]int, len(dims))
	for j, d := range dims {
		idx[j] = make(map[string]int, len(d))
		for k, name := range d {
			idx[j][name] = k
		}
	}
	covered := make([]bool, cells)
	for _, arm := range m.Arms {
		tp, ok := arm.Pattern.(*ast.TuplePat)
		if !ok || len(tp.Sub) != len(dims) {
			return // a non-tuple / mis-arity arm → bail (stay sound)
		}
		rect := make([][]int, len(dims))
		for j, sub := range tp.Sub {
			ks := compCoveredCtors(sub, dims[j], idx[j])
			if ks == nil {
				return // refining pattern we don't model → bail
			}
			rect[j] = ks
		}
		markRect(covered, dims, rect)
	}
	for _, c0 := range covered {
		if !c0 {
			c.report("E0303", "Non-exhaustive match — some (state, event) combination is unmatched", m.Span)
			return
		}
	}
}

// compCoveredCtors returns the dimension indices a tuple-component
// pattern admits: a wildcard or fresh-ident binding → all of them; a
// variant-named ident or VariantPat → that single constructor. Returns
// nil for a pattern shape that *refines* (a literal, a nested tuple) —
// the caller bails to stay sound.
func compCoveredCtors(sub ast.Pattern, names []string, idx map[string]int) []int {
	switch p := sub.(type) {
	case *ast.WildcardPat:
		return allIndices(names)
	case *ast.IdentPat:
		if k, ok := idx[p.Name]; ok {
			return []int{k} // nullary-variant ref
		}
		return allIndices(names) // fresh binding → covers all
	case *ast.VariantPat:
		if len(p.QName) == 0 {
			return nil
		}
		if k, ok := idx[p.QName[len(p.QName)-1]]; ok {
			return []int{k}
		}
		return nil
	case *ast.BoolLitPat:
		// bool's finite dimension is named true/false (finiteCtors).
		name := "false"
		if p.Value {
			name = "true"
		}
		if k, ok := idx[name]; ok {
			return []int{k}
		}
		return nil
	case *ast.AltPat:
		var out []int
		for _, a := range p.Atoms {
			ks := compCoveredCtors(a, names, idx)
			if ks == nil {
				return nil
			}
			out = append(out, ks...)
		}
		return out
	}
	return nil
}

func allIndices(names []string) []int {
	out := make([]int, len(names))
	for i := range names {
		out[i] = i
	}
	return out
}

// markRect marks every cell in the cartesian rectangle `rect` (a set of
// admitted indices per dimension) as covered, walking the row-major
// linearisation of the dims grid.
func markRect(covered []bool, dims [][]string, rect [][]int) {
	// strides for row-major indexing
	strides := make([]int, len(dims))
	s := 1
	for j := len(dims) - 1; j >= 0; j-- {
		strides[j] = s
		s *= len(dims[j])
	}
	var walk func(j, base int)
	walk = func(j, base int) {
		if j == len(dims) {
			covered[base] = true
			return
		}
		for _, k := range rect[j] {
			walk(j+1, base+k*strides[j])
		}
	}
	walk(0, 0)
}

// finiteCtors returns the finite constructor names of a type usable as
// a tuple-match dimension: a sum type's variant names, or true/false
// for bool. Returns nil for any type with no finite enumeration
// (primitives, classes, records, aliases this checker can't see
// through) — the caller bails to stay sound.
func finiteCtors(t Type) []string {
	switch v := t.(type) {
	case *Named:
		td, ok := v.Decl.(*ast.TypeDecl)
		if !ok {
			return nil
		}
		body, ok := td.Body.(*ast.SumTypeBody)
		if !ok {
			return nil
		}
		names := make([]string, len(body.Variants))
		for i, vr := range body.Variants {
			names[i] = vr.Name
		}
		return names
	case *Builtin:
		if v.N == "bool" {
			return []string{"true", "false"}
		}
	}
	return nil
}

// isVariantName reports whether name is one of type t's finite
// constructors — used to tell a nullary-variant pattern ident
// (`Idle`) from a fresh binding in a tuple-match component.
func isVariantName(t Type, name string) bool {
	for _, n := range finiteCtors(t) {
		if n == name {
			return true
		}
	}
	return false
}

// checkSumExhaustive reports the sum variants that no arm covers.
func (c *checker) checkSumExhaustive(m *ast.MatchExpr, body *ast.SumTypeBody) {
	covered := map[string]bool{}
	for _, arm := range m.Arms {
		collectCoveredVariants(arm.Pattern, covered)
	}
	var missing []string
	for _, v := range body.Variants {
		if !covered[v.Name] {
			missing = append(missing, v.Name)
		}
	}
	if len(missing) > 0 {
		c.report("E0303", "Non-exhaustive match — missing "+strings.Join(missing, ", "), m.Span)
	}
}

// collectCoveredVariants records the variant names a pattern covers,
// descending into AltPat atoms (`Up | Left`). Non-variant patterns
// contribute nothing.
func collectCoveredVariants(p ast.Pattern, covered map[string]bool) {
	switch v := p.(type) {
	case *ast.VariantPat:
		if len(v.QName) > 0 {
			covered[v.QName[len(v.QName)-1]] = true
		}
	case *ast.AltPat:
		for _, a := range v.Atoms {
			collectCoveredVariants(a, covered)
		}
	}
}

// checkBoolExhaustive requires both `true` and `false` arms (either
// as standalone arms or AltPat atoms — `true | false`).
func (c *checker) checkBoolExhaustive(m *ast.MatchExpr) {
	var sawTrue, sawFalse bool
	var scan func(p ast.Pattern)
	scan = func(p ast.Pattern) {
		switch v := p.(type) {
		case *ast.BoolLitPat:
			if v.Value {
				sawTrue = true
			} else {
				sawFalse = true
			}
		case *ast.AltPat:
			for _, a := range v.Atoms {
				scan(a)
			}
		}
	}
	for _, arm := range m.Arms {
		scan(arm.Pattern)
	}
	if !sawTrue || !sawFalse {
		c.report("E0303", "Non-exhaustive match — bool match must cover both true and false", m.Span)
	}
}

// isCatchAll reports whether p matches every value and binds no
// refutable structure (wildcard or a bare identifier binding).
func isCatchAll(p ast.Pattern) bool {
	switch p.(type) {
	case *ast.WildcardPat, *ast.IdentPat:
		return true
	}
	return false
}

// definitelyNotTryable reports whether a function's declared return
// type is unambiguously neither Result nor Option, so a `try` in
// its body is illegal (E0402). It returns false (try permitted) for
// Result/Option, and conservatively for aliases / unresolved types
// it cannot see through — only a clearly-non-tryable return forbids
// `try`, so the check never false-fires.
func (c *checker) definitelyNotTryable(rt ast.TypeExpr) bool {
	switch v := rt.(type) {
	case nil:
		return true // unit return
	case *ast.PrimitiveType:
		return true
	case *ast.SliceType:
		return true
	case *ast.TupleType:
		return true
	case *ast.FuncType:
		return true
	case *ast.NamedType:
		if len(v.QName) == 0 {
			return false
		}
		switch v.QName[0] {
		case "Result", "Option":
			return false
		case "Map", "Set", "Stack":
			return true
		}
		if sym := c.info.Symbol[v]; sym != nil {
			switch sym.Kind {
			case SymClass:
				return true
			case SymTypeDecl:
				if td, ok := sym.Decl.(*ast.TypeDecl); ok {
					if _, isSum := td.Body.(*ast.SumTypeBody); isSum {
						return true
					}
				}
			}
		}
		// Alias / type parameter / unresolved → not certain.
		return false
	default:
		return false
	}
}
