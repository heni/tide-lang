package sema

// Info is the AST-keyed side table. See docs/internals/sema.md §2.
type Info struct {
	// Symbol resolves *ast.Ident / *ast.NamedType to their Symbol.
	Symbol map[any]*Symbol
}

func newInfo() *Info {
	return &Info{Symbol: map[any]*Symbol{}}
}
