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
	c.constructShapes(f, scope)
	c.checkBodies(f)
	sortDiags(c.diags)
	return c.info, c.diags
}

type checker struct {
	file  string
	info  *Info
	diags []*Diag

	// Per-body context, set before walking each function / method
	// body in Barrier C. v1 is single-threaded so plain fields are
	// safe; the parallel-per-body story (sema.md §8) would thread
	// these instead.
	curReturn       Type // declared return type of the body being checked
	curThis         Type // receiver type inside an instance method, else nil
	curTryForbidden bool // body returns a type that is definitely not Result/Option
	loopDepth       int  // enclosing for/while nesting — 0 ⇒ break/continue illegal (E0404)
	scopeDepth      int  // enclosing `scope` nesting — 0 ⇒ spawn illegal (E0405)
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
