package sema

import (
	"github.com/heni/tide-lang/internal/ast"
	"strings"
)

// detectAliasCycles fires E0114 on a type-alias chain that
// loops back to itself (`type A = B; type B = A`).
// User-defined sums / classes don't participate — only aliases
// (`type T = OtherType`) form the alias graph.
func (c *checker) detectAliasCycles(f *ast.File) {
	aliases := map[string]*ast.TypeDecl{}
	for _, d := range f.Decls {
		td, ok := d.(*ast.TypeDecl)
		if !ok {
			continue
		}
		if _, isAlias := td.Body.(*ast.AliasBody); isAlias {
			aliases[td.Name] = td
		}
	}
	visited := map[string]int{} // 0=unseen, 1=in-stack, 2=done
	for name := range aliases {
		if visited[name] != 0 {
			continue
		}
		c.walkAlias(name, aliases, visited, []string{})
	}
}

func (c *checker) walkAlias(name string, aliases map[string]*ast.TypeDecl, visited map[string]int, stack []string) {
	if visited[name] == 2 {
		return
	}
	if visited[name] == 1 {
		// Cycle detected — find the index where it starts in stack.
		i := 0
		for ; i < len(stack); i++ {
			if stack[i] == name {
				break
			}
		}
		path := append(stack[i:], name)
		td := aliases[name]
		c.report("E0114", "Cyclic type alias: "+strings.Join(path, " → "), td.Span)
		return
	}
	visited[name] = 1
	stack = append(stack, name)
	target := aliases[name].Body.(*ast.AliasBody).Aliased
	for _, ref := range aliasRefs(target) {
		if _, isAlias := aliases[ref]; isAlias {
			c.walkAlias(ref, aliases, visited, stack)
		}
	}
	visited[name] = 2
}

// aliasRefs returns the bare names this type expression points at,
// recursing through generic args and slice elements.
func aliasRefs(t ast.TypeExpr) []string {
	if t == nil {
		return nil
	}
	switch v := t.(type) {
	case *ast.NamedType:
		out := []string{}
		if len(v.QName) > 0 {
			out = append(out, v.QName[0])
		}
		for _, a := range v.Args {
			out = append(out, aliasRefs(a)...)
		}
		return out
	case *ast.SliceType:
		return aliasRefs(v.Elem)
	}
	return nil
}
