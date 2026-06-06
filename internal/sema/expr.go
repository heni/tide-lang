package sema

import (
	"github.com/heni/tide-lang/internal/ast"
)

// resolveExpr — name resolution over expressions. Field-access
// tail names (`a.b` — the `b`) are dispatched by codegen, not
// sema-1. See docs/internals/sema.md §4.
func (c *checker) resolveExpr(e ast.Expr, scope *Scope) {
	if e == nil {
		return
	}
	switch v := e.(type) {
	case *ast.Ident:
		// `_` in expr position is the REPL last-value binding (RFC-0003).
		if v.Name == "_" {
			return
		}
		if sym := scope.lookup(v.Name); sym != nil {
			c.info.Symbol[v] = sym
		} else {
			c.report("E0103", "Unknown name "+v.Name, v.Span)
		}
	case *ast.Call:
		c.resolveExpr(v.Callee, scope)
		for _, ta := range v.TypeArgs {
			c.resolveTypeExpr(ta, scope)
		}
		for _, a := range v.Args {
			c.resolveExpr(a, scope)
		}
	case *ast.Field:
		// Only the receiver is resolved here; v.Name (the field
		// or method spelling) is dispatched by codegen against
		// the receiver's runtime shape. Sema-3 will validate
		// the field exists once Info.Type is populated.
		c.resolveExpr(v.Receiver, scope)
	case *ast.Binary:
		c.resolveExpr(v.Left, scope)
		c.resolveExpr(v.Right, scope)
	case *ast.Unary:
		c.resolveExpr(v.Operand, scope)
	case *ast.ParenExpr:
		c.resolveExpr(v.Inner, scope)
	case *ast.TupleLit:
		for _, ce := range v.Components {
			c.resolveExpr(ce, scope)
		}
	case *ast.TupleField:
		c.resolveExpr(v.Receiver, scope)
	case *ast.BraceLit:
		c.resolveTypeExpr(v.TypeName, scope)
		for _, e := range v.Entries {
			switch en := e.(type) {
			case *ast.RecordEntry:
				c.resolveExpr(en.Value, scope)
			case *ast.MapEntry:
				c.resolveExpr(en.Key, scope)
				c.resolveExpr(en.Value, scope)
			case *ast.SetEntry:
				c.resolveExpr(en.Value, scope)
			}
		}
	case *ast.SliceLit:
		c.resolveTypeExpr(v.ElemType, scope)
		for _, it := range v.Items {
			c.resolveExpr(it, scope)
		}
	case *ast.Index:
		c.resolveExpr(v.Receiver, scope)
		c.resolveExpr(v.Idx, scope)
	case *ast.Slice:
		c.resolveExpr(v.Receiver, scope)
		c.resolveExpr(v.Low, scope)
		c.resolveExpr(v.High, scope)
	case *ast.Block:
		// Block-as-expression — its own scope, like any block body.
		c.resolveBlock(v, scope)
	case *ast.IfExpr:
		c.resolveExpr(v.Cond, scope)
		c.resolveBlock(v.ThenBlock, scope)
		switch e := v.Else.(type) {
		case *ast.IfExpr:
			c.resolveExpr(e, scope)
		case *ast.Block:
			c.resolveBlock(e, scope)
		}
	case *ast.MatchExpr:
		c.resolveExpr(v.Subject, scope)
		for _, arm := range v.Arms {
			armScope := newScope(scope)
			c.bindPattern(arm.Pattern, armScope, arm)
			c.resolveExpr(arm.Body, armScope)
		}
	case *ast.ReturnExpr:
		c.resolveExpr(v.Value, scope)
	case *ast.TryExpr:
		c.resolveExpr(v.Inner, scope)
	}
}
