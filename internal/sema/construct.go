package sema

import (
	"strconv"

	"github.com/heni/tide-lang/internal/ast"
)

// constructShapes — rest of Barrier B. See docs/internals/sema.md §4.
// Walks type declarations once names are resolved, validating
// per-decl shape (alias cycles, duplicate fields, generic arity).
// Diagnostics: E0105, E0207, E0114.
func (c *checker) constructShapes(f *ast.File, fileScope *Scope) {
	c.detectAliasCycles(f)
	for _, d := range f.Decls {
		switch v := d.(type) {
		case *ast.TypeDecl:
			c.constructTypeDecl(v, fileScope)
		case *ast.ClassDecl:
			c.constructClassDecl(v, fileScope)
		case *ast.FuncDecl:
			c.constructFuncDecl(v, fileScope)
		}
	}
}

func (c *checker) constructTypeDecl(t *ast.TypeDecl, scope *Scope) {
	if sb, ok := t.Body.(*ast.SumTypeBody); ok {
		for _, va := range sb.Variants {
			seen := map[string]bool{}
			for _, f := range va.Fields {
				if seen[f.Name] {
					c.report("E0105", "Duplicate field name "+f.Name+" in variant "+va.Name, f.Span)
					continue
				}
				seen[f.Name] = true
				c.checkTypeArity(f.DeclType, scope)
			}
		}
	}
}

func (c *checker) constructClassDecl(cd *ast.ClassDecl, scope *Scope) {
	seen := map[string]bool{}
	for _, f := range cd.Fields {
		if seen[f.Name] {
			c.report("E0105", "Duplicate field name "+f.Name+" in class "+cd.Name, f.Span)
			continue
		}
		seen[f.Name] = true
		c.checkTypeArity(f.DeclType, scope)
	}
	for _, m := range cd.Methods {
		for _, p := range m.Params {
			c.checkTypeArity(p.DeclType, scope)
		}
		if m.ReturnType != nil {
			c.checkTypeArity(m.ReturnType, scope)
		}
	}
}

func (c *checker) constructFuncDecl(fn *ast.FuncDecl, scope *Scope) {
	for _, p := range fn.Params {
		c.checkTypeArity(p.DeclType, scope)
	}
	if fn.ReturnType != nil {
		c.checkTypeArity(fn.ReturnType, scope)
	}
}

// checkTypeArity validates generic-instantiation arity against
// the predeclared arity table. Skips local types (user classes /
// aliases) — those are arbitrary-arity, validated by Sema-3 once
// per-decl arities are stored.
func (c *checker) checkTypeArity(t ast.TypeExpr, scope *Scope) {
	if t == nil {
		return
	}
	switch v := t.(type) {
	case *ast.NamedType:
		if len(v.QName) > 0 {
			head := v.QName[0]
			if want, ok := predeclaredGenericArity[head]; ok {
				got := len(v.Args)
				if got != want {
					c.report("E0207",
						"Wrong type arity on generic instantiation: "+head+
							" expects "+strconv.Itoa(want)+" type "+pluralArgs(want)+
							", got "+strconv.Itoa(got),
						v.Span)
				}
			}
		}
		for _, a := range v.Args {
			c.checkTypeArity(a, scope)
		}
	case *ast.SliceType:
		c.checkTypeArity(v.Elem, scope)
	case *ast.TupleType:
		for _, ct := range v.Components {
			c.checkTypeArity(ct, scope)
		}
	case *ast.PrimitiveType:
		// no args
	default:
		// Closed-sum convention (docs/internals/sema.md §5):
		// a new TypeExpr shape must add a case here. The panic
		// is the audit net.
		panic("sema.checkTypeArity: unhandled TypeExpr " + t.NodeKind())
	}
}

// predeclaredGenericArity is the closed table of predeclared
// generic type arities. User-declared classes get their arity
// from cd.TypeParams in Sema-3; this table only covers what
// builtins.md fixes.
var predeclaredGenericArity = map[string]int{
	"Option":   1,
	"Result":   2,
	"Map":      2,
	"Set":      1,
	"Stack":    1,
	"Channel":  1,
	"SendChan": 1,
	"RecvChan": 1,
}

func pluralArgs(n int) string {
	if n == 1 {
		return "argument"
	}
	return "arguments"
}
