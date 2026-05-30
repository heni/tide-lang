package sema

import (
	"github.com/heni/tide-lang/internal/ast"
)

// Check runs sema passes against f and returns the side-table
// plus any diagnostics, ordered by source position.
// See docs/internals/sema.md.
func Check(f *ast.File, file string) (*Info, []*Diag) {
	c := &checker{file: file, info: newInfo()}
	scope := c.indexDeclarations(f)
	c.resolveFile(f, scope)
	sortDiags(c.diags)
	return c.info, c.diags
}

type checker struct {
	file  string
	info  *Info
	diags []*Diag
}

func (c *checker) report(code, message string, span ast.Span) {
	c.diags = append(c.diags, &Diag{
		File:    c.file,
		Code:    code,
		Message: message,
		Line:    span.StartLine,
		Col:     span.StartCol,
	})
}
