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
// arm). E0305 (float-literal patterns) has no trigger — the parser
// produces no FloatLitPat node.

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
	}
}

// checkSumExhaustive reports the sum variants that no arm covers.
func (c *checker) checkSumExhaustive(m *ast.MatchExpr, body *ast.SumTypeBody) {
	covered := map[string]bool{}
	for _, arm := range m.Arms {
		if vp, ok := arm.Pattern.(*ast.VariantPat); ok && len(vp.QName) > 0 {
			covered[vp.QName[len(vp.QName)-1]] = true
		}
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

// checkBoolExhaustive requires both `true` and `false` arms.
func (c *checker) checkBoolExhaustive(m *ast.MatchExpr) {
	var sawTrue, sawFalse bool
	for _, arm := range m.Arms {
		if bp, ok := arm.Pattern.(*ast.BoolLitPat); ok {
			if bp.Value {
				sawTrue = true
			} else {
				sawFalse = true
			}
		}
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
