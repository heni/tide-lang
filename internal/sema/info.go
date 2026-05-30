package sema

import "github.com/heni/tide-lang/internal/ast"

// Info is the AST-keyed side table. See docs/internals/sema.md §2.
type Info struct {
	// Symbol resolves *ast.Ident / *ast.NamedType to their Symbol.
	// Keyed by ast.Node — pointer identity into the AST is the
	// contract; non-pointer keys are not admitted.
	Symbol map[ast.Node]*Symbol
}

func newInfo() *Info {
	return &Info{Symbol: map[ast.Node]*Symbol{}}
}
