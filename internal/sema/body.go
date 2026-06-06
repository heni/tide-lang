package sema

import (
	"github.com/heni/tide-lang/internal/ast"
)

// checkBodies — Barrier C. Walks every function / method body
// after Barrier B has frozen the external surface, inferring a
// type for every expression (recorded in Info.Type) and emitting
// the typing diagnostics whose premises are local to a body.
//
// PR-Sema-C1 covers the scalar core: literal types, identifier /
// receiver types, arithmetic / logical operators, let / var / and
// assignment type agreement (E0201), return-type agreement
// (E0203), and call arity (E0202). Collection, conversion,
// Dynamic, exhaustiveness and context rules land in later PRs;
// every shape this PR cannot yet type degrades to *Unknown, so an
// unfinished checker never reports a false positive.
// See docs/internals/sema.md §4.
func (c *checker) checkBodies(f *ast.File) {
	for _, d := range f.Decls {
		switch v := d.(type) {
		case *ast.FuncDecl:
			if v.Body != nil {
				c.curReturn = c.typeFromExpr(v.ReturnType)
				c.curThis = nil
				c.curTryForbidden = c.definitelyNotTryable(v.ReturnType)
				c.checkBlock(v.Body)
			}
		case *ast.ClassDecl:
			for _, m := range v.Methods {
				if m.Body == nil {
					continue
				}
				c.curReturn = c.typeFromExpr(m.ReturnType)
				c.curTryForbidden = c.definitelyNotTryable(m.ReturnType)
				if m.IsStatic {
					c.curThis = nil
				} else {
					c.curThis = &Named{N: v.Name, Decl: v}
				}
				c.checkBlock(m.Body)
			}
		}
	}
}

func (c *checker) checkBlock(b *ast.Block) {
	c.inferBlock(b)
}

func (c *checker) checkStmt(s ast.Stmt) {
	switch v := s.(type) {
	case *ast.ExprStmt:
		c.inferExpr(v.Expr)
	case *ast.LetStmt:
		c.checkBinding(v, v.Pattern, v.DeclType, v.Value)
	case *ast.VarStmt:
		c.checkBinding(v, nil, v.DeclType, v.Value)
	case *ast.AssignStmt:
		lt := c.inferExpr(v.LValue)
		vt := c.inferExpr(v.Value)
		if !c.fits(lt, v.Value, vt) {
			c.report("E0201", "Type mismatch — cannot assign "+vt.String()+" to "+lt.String(), v.Span)
		}
	case *ast.IfStmt:
		c.inferExpr(v.Cond)
		if v.ThenBlock != nil {
			c.checkBlock(v.ThenBlock)
		}
		switch e := v.Else.(type) {
		case *ast.IfStmt:
			c.checkStmt(e)
		case *ast.Block:
			c.checkBlock(e)
		}
	case *ast.ForStmt:
		c.checkForBinding(v)
		switch it := v.Iterable.(type) {
		case *ast.RangeExpr:
			c.inferExpr(it.Low)
			c.inferExpr(it.High)
		case ast.Expr:
			c.inferExpr(it)
		}
		if v.Body != nil {
			c.checkBlock(v.Body)
		}
	}
}

// checkBinding handles let / var. bindNode is the AST node keyed
// in Info.Def for the introduced binding (the LetStmt's IdentPat
// or the VarStmt itself); a destructuring let with no single
// IdentPat passes a nil pattern and only type-checks the value.
func (c *checker) checkBinding(bindNode ast.Node, pat ast.Pattern, ann ast.TypeExpr, value ast.Expr) {
	var vt Type = &Unknown{}
	if value != nil {
		vt = c.inferExpr(value)
	}
	var declared Type
	if ann != nil {
		declared = c.typeFromExpr(ann)
		// Only compare against an actual initialiser; a bare
		// `var x: T` (no value) is a separate concern, not a
		// type mismatch. fits() applies integer-literal and
		// slice-literal narrowing and the E0204 range check.
		if value != nil && !c.fits(declared, value, vt) {
			c.report("E0201", "Type mismatch — annotation is "+declared.String()+" but value is "+vt.String(), value.NodeSpan())
		}
	}
	// The binding's static type is the annotation when present,
	// else the inferred value type. Hang it on the shared Symbol so
	// use sites read it back through Info.Symbol.
	bound := declared
	if bound == nil {
		bound = vt
	}
	c.setBindingType(bindNode, pat, bound)
}

// setBindingType records the resolved type on the binding's
// Symbol via Info.Def (LetStmt → its IdentPat; VarStmt → itself).
func (c *checker) setBindingType(bindNode ast.Node, pat ast.Pattern, t Type) {
	if t == nil {
		return
	}
	if ip, ok := pat.(*ast.IdentPat); ok {
		if sym := c.info.Def[ip]; sym != nil {
			sym.Type = t
		}
		return
	}
	if sym := c.info.Def[bindNode]; sym != nil {
		sym.Type = t
	}
}

// checkForBinding gives a simple loop variable its element type.
// A numeric range binds the variable to int; any other iterable's
// element type is not modelled until the collection PR (Unknown).
func (c *checker) checkForBinding(f *ast.ForStmt) {
	ip, ok := f.Pattern.(*ast.IdentPat)
	if !ok {
		return
	}
	sym := c.info.Def[ip]
	if sym == nil {
		return
	}
	if _, isRange := f.Iterable.(*ast.RangeExpr); isRange {
		sym.Type = &Builtin{N: "int"}
	}
}
