package sema

import (
	"github.com/heni/tide-lang/internal/ast"
)

// Check runs sema passes against f and returns the side-table
// plus any diagnostics, ordered by source position.
// See docs/internals/sema.md.
func Check(f *ast.File, file string) (*Info, []*Diag) {
	return CheckFiles([]*ast.File{f}, []string{file})
}

// CheckFiles runs sema over a whole package — every `.td` file in a
// directory shares one top-level scope (RFC-0002 §"Package =
// directory"). The phases run file-by-file over the shared scope, with
// the per-file path tracked so each diagnostic carries its own source
// file. `paths[i]` is the source path of `files[i]`.
func CheckFiles(files []*ast.File, paths []string) (*Info, []*Diag) {
	c := &checker{
		info:          newInfo(),
		closureExpect: map[*ast.ClosureLit]*Func{},
		externImpls:   map[string]*ast.ExternImplDecl{},
	}
	scope := c.newPackageScope()
	for i, f := range files {
		c.file = paths[i]
		c.indexFile(f, scope)
	}
	for i, f := range files {
		c.file = paths[i]
		c.resolveFile(f, scope)
	}
	for i, f := range files {
		c.file = paths[i]
		c.constructShapes(f, scope)
	}
	for i, f := range files {
		c.file = paths[i]
		c.checkBodies(f)
	}
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

	// closureExpect carries the expected Func signature for a closure
	// passed as a call argument, so an unannotated short-closure
	// parameter is typed from call context (a comparator to
	// `sort.sorted`, etc.) rather than left Unknown. Keyed by the
	// closure node; set just before its enclosing argument is inferred.
	closureExpect map[*ast.ClosureLit]*Func

	// externImpls maps an opaque foreign handle's name to the
	// `extern impl T { … }` block carrying its methods/fields, so
	// member access on a handle (ffi.md §ExternImpl) can resolve.
	externImpls map[string]*ast.ExternImplDecl
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
