package sema

import (
	"github.com/heni/tide-lang/internal/ast"
)

// resolveFile — name resolution over decls + bodies. Phase 1.
// See docs/internals/sema.md §4.
func (c *checker) resolveFile(f *ast.File, fileScope *Scope) {
	for _, d := range f.Decls {
		switch v := d.(type) {
		case *ast.TypeDecl:
			c.resolveTypeDecl(v, fileScope)
		case *ast.ClassDecl:
			c.resolveClassDecl(v, fileScope)
		case *ast.FuncDecl:
			c.resolveFuncDecl(v, fileScope)
		}
	}
}

func (c *checker) resolveTypeDecl(t *ast.TypeDecl, parent *Scope) {
	switch b := t.Body.(type) {
	case *ast.AliasBody:
		c.resolveTypeExpr(b.Aliased, parent)
	case *ast.SumTypeBody:
		for _, v := range b.Variants {
			for _, f := range v.Fields {
				c.resolveTypeExpr(f.DeclType, parent)
			}
		}
	}
}

func (c *checker) resolveClassDecl(cd *ast.ClassDecl, parent *Scope) {
	classScope := newScope(parent)
	for _, tp := range cd.TypeParams {
		c.checkReservedName(tp, cd.Span)
		classScope.declare(&Symbol{Name: tp, Kind: SymTypeParam, Type: &Named{N: tp}})
	}
	// Class member scope: fields + methods visible inside any
	// instance method body via implicit receiver
	// (name-resolution.md §Implicit receiver).
	memberScope := newScope(classScope)
	for _, f := range cd.Fields {
		c.checkReservedName(f.Name, f.Span)
		memberScope.declare(&Symbol{Name: f.Name, Kind: SymField, Decl: f, Type: &Unknown{}})
		c.resolveTypeExpr(f.DeclType, classScope)
	}
	for _, m := range cd.Methods {
		memberScope.declare(&Symbol{Name: m.Name, Kind: SymMethod, Decl: m, Type: &Unknown{}})
	}
	for _, m := range cd.Methods {
		c.resolveMethod(cd, m, classScope, memberScope)
	}
}

func (c *checker) resolveMethod(cd *ast.ClassDecl, m *ast.Method, classScope, memberScope *Scope) {
	// Instance methods see members via implicit receiver; static
	// ones don't (they call other statics through the class name).
	var bodyParent *Scope
	if m.IsStatic {
		bodyParent = classScope
	} else {
		bodyParent = memberScope
	}
	bodyScope := newScope(bodyParent)
	if !m.IsStatic {
		bodyScope.declare(&Symbol{Name: "this", Kind: SymLocal, Decl: cd, Type: &Named{N: cd.Name, Decl: cd}})
	}
	for _, p := range m.Params {
		c.checkReservedName(p.Name, p.Span)
		c.resolveTypeExpr(p.DeclType, classScope)
		bodyScope.declare(&Symbol{Name: p.Name, Kind: SymLocal, Decl: p, Type: &Unknown{}})
	}
	if m.ReturnType != nil {
		c.resolveTypeExpr(m.ReturnType, classScope)
	}
	if m.Body != nil {
		c.resolveBlock(m.Body, bodyScope)
	}
}

func (c *checker) resolveFuncDecl(fn *ast.FuncDecl, parent *Scope) {
	c.checkReservedName(fn.Name, fn.Span)
	fnScope := newScope(parent)
	for _, tp := range fn.TypeParams {
		c.checkReservedName(tp, fn.Span)
		fnScope.declare(&Symbol{Name: tp, Kind: SymTypeParam, Type: &Named{N: tp}})
	}
	for _, p := range fn.Params {
		c.checkReservedName(p.Name, p.Span)
		c.resolveTypeExpr(p.DeclType, fnScope)
		fnScope.declare(&Symbol{Name: p.Name, Kind: SymLocal, Decl: p, Type: &Unknown{}})
	}
	if fn.ReturnType != nil {
		c.resolveTypeExpr(fn.ReturnType, fnScope)
	}
	if fn.Body != nil {
		c.resolveBlock(fn.Body, fnScope)
	}
}

func (c *checker) resolveBlock(b *ast.Block, parent *Scope) {
	if b == nil {
		return
	}
	scope := newScope(parent)
	for _, s := range b.Stmts {
		c.resolveStmt(s, scope)
	}
	if b.Trailing != nil {
		c.resolveExpr(b.Trailing, scope)
	}
}

func (c *checker) resolveStmt(s ast.Stmt, scope *Scope) {
	switch v := s.(type) {
	case *ast.ExprStmt:
		c.resolveExpr(v.Expr, scope)
	case *ast.LetStmt:
		if v.Value != nil {
			c.resolveExpr(v.Value, scope)
		}
		if v.DeclType != nil {
			c.resolveTypeExpr(v.DeclType, scope)
		}
		c.bindPattern(v.Pattern, scope, v)
	case *ast.VarStmt:
		if v.Value != nil {
			c.resolveExpr(v.Value, scope)
		}
		if v.DeclType != nil {
			c.resolveTypeExpr(v.DeclType, scope)
		}
		if v.Name != "" && v.Name != "_" {
			c.checkReservedName(v.Name, v.Span)
			scope.declare(&Symbol{Name: v.Name, Kind: SymLocal, Decl: v, Type: &Unknown{}})
		}
	case *ast.AssignStmt:
		c.resolveExpr(v.LValue, scope)
		c.resolveExpr(v.Value, scope)
	case *ast.IfStmt:
		c.resolveExpr(v.Cond, scope)
		c.resolveBlock(v.ThenBlock, scope)
		switch e := v.Else.(type) {
		case *ast.IfStmt:
			c.resolveStmt(e, scope)
		case *ast.Block:
			c.resolveBlock(e, scope)
		}
	case *ast.ForStmt:
		// RangeExpr is a Node but not an Expr — handle it
		// explicitly. Other iterables (slices, maps, sets) are
		// regular Expr values.
		switch it := v.Iterable.(type) {
		case *ast.RangeExpr:
			c.resolveExpr(it.Low, scope)
			c.resolveExpr(it.High, scope)
		case ast.Expr:
			c.resolveExpr(it, scope)
		}
		bodyScope := newScope(scope)
		c.bindPattern(v.Pattern, bodyScope, v)
		if v.Body != nil {
			// Re-use resolveBlock so block-internal scoping
			// stays consistent with the let-in-let rule.
			innerScope := newScope(bodyScope)
			for _, st := range v.Body.Stmts {
				c.resolveStmt(st, innerScope)
			}
			if v.Body.Trailing != nil {
				c.resolveExpr(v.Body.Trailing, innerScope)
			}
		}
	}
}

// bindPattern — introduces bindings from let/for/match patterns.
func (c *checker) bindPattern(p ast.Pattern, scope *Scope, decl any) {
	switch v := p.(type) {
	case *ast.IdentPat:
		if v.Name == "" || v.Name == "_" {
			return
		}
		c.checkReservedName(v.Name, v.Span)
		scope.declare(&Symbol{Name: v.Name, Kind: SymLocal, Decl: decl, Type: &Unknown{}})
	case *ast.VariantPat:
		for _, sub := range v.Sub {
			c.bindPattern(sub, scope, decl)
		}
	}
}

func (c *checker) resolveTypeExpr(t ast.TypeExpr, scope *Scope) {
	if t == nil {
		return
	}
	switch v := t.(type) {
	case *ast.NamedType:
		if len(v.QName) > 0 {
			head := v.QName[0]
			if sym := scope.lookup(head); sym != nil {
				c.info.Symbol[v] = sym
			} else {
				c.report("E0103", "Unknown name "+head, v.Span)
			}
		}
		for _, a := range v.Args {
			c.resolveTypeExpr(a, scope)
		}
	case *ast.SliceType:
		c.resolveTypeExpr(v.Elem, scope)
	}
}
