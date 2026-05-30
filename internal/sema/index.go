package sema

import (
	"strings"

	"github.com/heni/tide-lang/internal/ast"
)

// indexDeclarations — Barrier A. See docs/internals/sema.md §4.
// Returns the file scope (parented by the predeclared scope).
func (c *checker) indexDeclarations(f *ast.File) *Scope {
	pre := newScope(nil)
	for name, sym := range predeclaredSymbols() {
		pre.names[name] = sym
	}
	file := newScope(pre)

	for _, im := range f.Imports {
		if im.Path == "" {
			continue
		}
		head := strings.SplitN(im.Path, "/", 2)[0]
		if pre.lookup(head) == nil {
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
		case *ast.FuncDecl:
			c.checkReservedName(v.Name, v.Span)
			sym := &Symbol{Name: v.Name, Kind: SymFunc, Decl: v, Type: &Unknown{}}
			if prev := file.declare(sym); prev != nil {
				c.report("E0113", "Duplicate top-level declaration "+v.Name, v.Span)
			}
		}
	}
	return file
}

func (c *checker) checkReservedName(name string, span ast.Span) {
	if goReservedIdent(name) {
		c.report("E0107", "Reserved identifier prefix `_tide_` — used by codegen", span)
	}
}
