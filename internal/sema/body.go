package sema

import (
	"strconv"

	"github.com/heni/tide-lang/internal/ast"
)

// checkBodies — Barrier C. Walks every function body /
// method body after Sema-2 has frozen the external surface.
// Sema-3a covers call arity (E0202); type inference + the
// rest of E0201–E0208 land in follow-up PRs.
// See docs/internals/sema.md §4.
func (c *checker) checkBodies(f *ast.File) {
	for _, d := range f.Decls {
		switch v := d.(type) {
		case *ast.FuncDecl:
			if v.Body != nil {
				c.checkBlock(v.Body)
			}
		case *ast.ClassDecl:
			for _, m := range v.Methods {
				if m.Body != nil {
					c.checkBlock(m.Body)
				}
			}
		}
	}
}

func (c *checker) checkBlock(b *ast.Block) {
	for _, s := range b.Stmts {
		c.checkStmt(s)
	}
	if b.Trailing != nil {
		c.checkExpr(b.Trailing)
	}
}

func (c *checker) checkStmt(s ast.Stmt) {
	switch v := s.(type) {
	case *ast.ExprStmt:
		c.checkExpr(v.Expr)
	case *ast.LetStmt:
		c.checkExpr(v.Value)
	case *ast.VarStmt:
		c.checkExpr(v.Value)
	case *ast.AssignStmt:
		c.checkExpr(v.LValue)
		c.checkExpr(v.Value)
	case *ast.IfStmt:
		c.checkExpr(v.Cond)
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
		switch it := v.Iterable.(type) {
		case *ast.RangeExpr:
			c.checkExpr(it.Low)
			c.checkExpr(it.High)
		case ast.Expr:
			c.checkExpr(it)
		}
		if v.Body != nil {
			c.checkBlock(v.Body)
		}
	}
}

func (c *checker) checkExpr(e ast.Expr) {
	if e == nil {
		return
	}
	switch v := e.(type) {
	case *ast.Call:
		c.checkExpr(v.Callee)
		for _, a := range v.Args {
			c.checkExpr(a)
		}
		c.checkCallArity(v)
	case *ast.Field:
		c.checkExpr(v.Receiver)
	case *ast.Binary:
		c.checkExpr(v.Left)
		c.checkExpr(v.Right)
	case *ast.Unary:
		c.checkExpr(v.Operand)
	case *ast.SliceLit:
		for _, it := range v.Items {
			c.checkExpr(it)
		}
	case *ast.Index:
		c.checkExpr(v.Receiver)
		c.checkExpr(v.Idx)
	case *ast.Slice:
		c.checkExpr(v.Receiver)
		c.checkExpr(v.Low)
		c.checkExpr(v.High)
	case *ast.MatchExpr:
		c.checkExpr(v.Subject)
		for _, arm := range v.Arms {
			c.checkExpr(arm.Body)
		}
	case *ast.ReturnExpr:
		c.checkExpr(v.Value)
	case *ast.TryExpr:
		c.checkExpr(v.Inner)
	}
}

// checkCallArity — E0202. Compares the call's positional
// argument count against the callee's declared parameter
// count when the callee is a user-declared func or method
// reachable through the Info side-table. Variadic /
// stdlib-binding / class-constructor calls are skipped
// because the binding layer doesn't expose arities yet.
func (c *checker) checkCallArity(call *ast.Call) {
	id, ok := call.Callee.(*ast.Ident)
	if !ok {
		// Methods (`a.b()`) need receiver-type info — Sema-3b.
		return
	}
	sym, ok := c.info.Symbol[id]
	if !ok || sym == nil {
		return
	}
	switch sym.Kind {
	case SymFunc:
		fn, ok := sym.Decl.(*ast.FuncDecl)
		if !ok {
			return
		}
		// Generic funcs may rely on inference at the call site;
		// arity is still well-defined.
		want := len(fn.Params)
		got := len(call.Args)
		if want != got {
			c.report("E0202",
				"Wrong arity in call to "+fn.Name+
					": expects "+strconv.Itoa(want)+" "+pluralArgs(want)+
					", got "+strconv.Itoa(got),
				call.Span)
		}
	case SymClass:
		// Constructor call `ClassName(args)`. Arity = number of fields.
		cd, ok := sym.Decl.(*ast.ClassDecl)
		if !ok {
			return
		}
		want := len(cd.Fields)
		got := len(call.Args)
		if want != got {
			c.report("E0202",
				"Wrong arity in constructor "+cd.Name+
					": expects "+strconv.Itoa(want)+" field "+pluralArgs(want)+
					", got "+strconv.Itoa(got),
				call.Span)
		}
	}
}
