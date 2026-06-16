package sema

import (
	"strings"

	"github.com/heni/tide-lang/internal/ast"
)

// newPackageScope builds the package's top-level scope (parented by
// the predeclared scope). Every `.td` file in the package shares it
// (RFC-0002 §"Package = directory").
func (c *checker) newPackageScope() *Scope {
	pre := newScope(nil)
	for name, sym := range predeclaredSymbols() {
		pre.names[name] = sym
	}
	return newScope(pre)
}

// indexFile registers one file's imports + top-level declarations into
// the (possibly shared) package scope — Barrier A, see
// docs/internals/sema.md §4. Duplicate top-level names — within a file
// or across files of the same package — are E0113.
func (c *checker) indexFile(f *ast.File, file *Scope) {
	for _, im := range f.Imports {
		if im.Path == "" {
			continue
		}
		head := strings.SplitN(im.Path, "/", 2)[0]
		// Only a predeclared (builtin) module name binds a namespace
		// symbol; the predeclared scope is the package scope's parent.
		if file.parent.lookup(head) == nil {
			continue
		}
		file.declare(&Symbol{Name: head, Kind: SymBuiltinModule, Type: &Unknown{}})
	}

	for _, d := range f.Decls {
		switch v := d.(type) {
		case *ast.TypeDecl:
			c.checkReservedName(v.Name, v.Span)
			sym := &Symbol{Name: v.Name, Kind: SymTypeDecl, Decl: v, Type: &Named{N: v.Name, Decl: v}}
			if prev := file.declare(sym); prev != nil {
				c.report("E0113", "Duplicate top-level declaration "+v.Name, v.Span)
			}
			if sb, ok := v.Body.(*ast.SumTypeBody); ok {
				// Within-sum duplicate variant names are E0106
				// per diagnostics.md.
				seen := map[string]bool{}
				for _, va := range sb.Variants {
					c.checkReservedName(va.Name, va.Span)
					if seen[va.Name] {
						c.report("E0106", "Duplicate variant name "+va.Name, va.Span)
						continue
					}
					seen[va.Name] = true
					vsym := &Symbol{Name: va.Name, Kind: SymUserVariant, Decl: va, Type: &Named{N: v.Name, Decl: v}}
					// Cross-sum ambiguity (E0104) — a variant
					// name shared by two different user sums.
					if prev := file.lookup(va.Name); prev != nil && prev.Kind == SymUserVariant {
						if prev.Type != nil && vsym.Type != nil && prev.Type.String() != vsym.Type.String() {
							c.report("E0104", "Ambiguous variant name "+va.Name+" — declared by both "+prev.Type.String()+" and "+vsym.Type.String(), va.Span)
						}
					}
					file.declare(vsym)
				}
			}
		case *ast.ClassDecl:
			c.checkReservedName(v.Name, v.Span)
			sym := &Symbol{Name: v.Name, Kind: SymClass, Decl: v, Type: &Named{N: v.Name, Decl: v}}
			if prev := file.declare(sym); prev != nil {
				c.report("E0113", "Duplicate top-level declaration "+v.Name, v.Span)
			}
		case *ast.InterfaceDecl:
			c.checkReservedName(v.Name, v.Span)
			sym := &Symbol{Name: v.Name, Kind: SymInterface, Decl: v, Type: &Named{N: v.Name, Decl: v}}
			if prev := file.declare(sym); prev != nil {
				c.report("E0113", "Duplicate top-level declaration "+v.Name, v.Span)
			}
		case *ast.FuncDecl:
			c.checkReservedName(v.Name, v.Span)
			sym := &Symbol{Name: v.Name, Kind: SymFunc, Decl: v, Type: &Unknown{}}
			if prev := file.declare(sym); prev != nil {
				c.report("E0113", "Duplicate top-level declaration "+v.Name, v.Span)
			}
		case *ast.TopLevelLet:
			// Module-level constant. Type stays Unknown until the
			// body pass (checkTopLevelLet) infers it; keyed in
			// Info.Def[v] so that pass can write the resolved type
			// back onto this shared symbol.
			c.checkReservedName(v.Name, v.Span)
			sym := &Symbol{Name: v.Name, Kind: SymTopLevelLet, Decl: v, Type: &Unknown{}}
			if prev := file.declare(sym); prev != nil {
				c.report("E0113", "Duplicate top-level declaration "+v.Name, v.Span)
			}
			c.info.Def[v] = sym
		case *ast.ExternTypeDecl:
			// Opaque foreign handle (ffi.md §ExternType). Its Type is
			// the nominal Named carrying the decl, so member access can
			// recognise the handle and refEq can admit it.
			c.checkReservedName(v.Name, v.Span)
			sym := &Symbol{Name: v.Name, Kind: SymExternType, Decl: v, Type: &Named{N: v.Name, Decl: v}}
			if prev := file.declare(sym); prev != nil {
				c.report("E0113", "Duplicate top-level declaration "+v.Name, v.Span)
			}
		case *ast.ExternFuncDecl:
			// Package-level foreign function. Signature frozen in the
			// resolve pass (Barrier B), like an ordinary FuncDecl.
			c.checkReservedName(v.Name, v.Span)
			sym := &Symbol{Name: v.Name, Kind: SymExternFunc, Decl: v, Type: &Unknown{}}
			if prev := file.declare(sym); prev != nil {
				c.report("E0113", "Duplicate top-level declaration "+v.Name, v.Span)
			}
		case *ast.ExternImplDecl:
			// Not a name binding — it attaches members to an existing
			// handle. Index by handle name for member-access lookup.
			c.externImpls[v.Type] = v
		}
	}
}

func (c *checker) checkReservedName(name string, span ast.Span) {
	if goReservedIdent(name) {
		c.report("E0107", "Reserved identifier prefix `_tide_` — used by codegen", span)
	}
}
