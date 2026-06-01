package sema

import "github.com/heni/tide-lang/internal/ast"

// Info is the AST-keyed side table. See docs/internals/sema.md §2.
type Info struct {
	// Symbol resolves *ast.Ident / *ast.NamedType to their Symbol.
	// Keyed by ast.Node — pointer identity into the AST is the
	// contract; non-pointer keys are not admitted.
	Symbol map[ast.Node]*Symbol

	// Type carries the inferred type of every expression Barrier C
	// visits. Keyed by the *ast.Expr node. Populated during body
	// checking; an expression sema could not type yet maps to a
	// *Unknown (the conservative wildcard).
	Type map[ast.Expr]Type

	// Def back-references a binding-introducing node (Param,
	// ClassField, VarStmt, IdentPat) to the Symbol it introduces.
	// Use-site idents already reach their Symbol through Symbol[];
	// Def closes the loop from the *declaration* side so Barrier C
	// can hang an inferred type on a let/var binding and codegen
	// can later read binding metadata without a parallel tracker.
	Def map[ast.Node]*Symbol
}

func newInfo() *Info {
	return &Info{
		Symbol: map[ast.Node]*Symbol{},
		Type:   map[ast.Expr]Type{},
		Def:    map[ast.Node]*Symbol{},
	}
}
