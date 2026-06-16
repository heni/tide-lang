package codegen

// extern.go — Go-FFI foreign-binding lowering (ffi.md, lowering-go.md
// §ForeignCall). An `extern func`/method call lowers to a direct Go
// `pkg.Sym(args)` / `recv.Method(args)`; an opaque `extern type` lowers
// to the Go pointer type `*pkg.Sym`. A `(T,error)`-shaped return (written
// in the curated `.td` as `Result<T, error>`) is wrapped with the same
// `tideResultOf` helper the stdlib binding table uses. The Go type checker
// re-verifies every emitted call (the "verify, don't trust" property).

import (
	"fmt"
	"strings"

	"github.com/heni/tide-lang/internal/ast"
	"github.com/heni/tide-lang/internal/sema"
)

// scanExterns records the file's extern decls for later lowering.
func (g *gen) scanExterns(f *ast.File) {
	for _, d := range f.Decls {
		switch v := d.(type) {
		case *ast.ExternFuncDecl:
			g.externFunc[v.Name] = v
		case *ast.ExternTypeDecl:
			g.externType[v.Name] = v
		case *ast.ExternImplDecl:
			if g.externMethods[v.Type] == nil {
				g.externMethods[v.Type] = map[string]*ast.ExternMethod{}
				g.externFields[v.Type] = map[string]*ast.ExternField{}
			}
			for _, m := range v.Methods {
				g.externMethods[v.Type][m.Name] = m
			}
			for _, fld := range v.Fields {
				g.externFields[v.Type][fld.Name] = fld
			}
		}
	}
}

// goRefPkgSym splits an `@go("pkg")` / `@go("pkg.Sym")` on an extern
// type or func into (importPath, goSymbol). The split is on the last
// `.` after the last `/`; a bare path (or absent attribute) defaults
// the symbol to the exported Tide name. pkg is "" when no attribute is
// present (an error at the call/type site — a package is required).
//
// The heuristic assumes a **dot-free final path segment** — true for
// the stdlib (v1 scope). A non-stdlib path with a dotted directory
// segment (`gopkg.in/yaml.v3.Marshal`) would mis-split; that case is
// the binding manifest's job (ffi.md §Dependency model), not here.
func goRefPkgSym(ref *ast.GoRef, tideName string) (pkg, sym string) {
	if ref == nil || ref.Raw == "" {
		return "", exportFieldName(tideName)
	}
	raw := ref.Raw
	slash := strings.LastIndex(raw, "/")
	dot := strings.LastIndex(raw, ".")
	if dot > slash { // a `.Symbol` suffix on the final path segment
		return raw[:dot], raw[dot+1:]
	}
	return raw, exportFieldName(tideName)
}

// goPkgRef is the reference qualifier for an import path — the base name
// Go uses to address the package (`os/exec` → `exec`, `regexp` →
// `regexp`). The import statement uses the full path; every reference
// uses this base. (Distinct base names sharing a segment, e.g.
// text/template vs html/template, would need an import alias — out of
// scope until the binding manifest, ffi.md §Dependency model.)
func goPkgRef(importPath string) string {
	if i := strings.LastIndex(importPath, "/"); i >= 0 {
		return importPath[i+1:]
	}
	return importPath
}

// goRefMember returns the Go method/field name an extern-impl member's
// `@go` names — the bare string, or the exported Tide name if absent.
func goRefMember(ref *ast.GoRef, tideName string) string {
	if ref == nil || ref.Raw == "" {
		return exportFieldName(tideName)
	}
	return ref.Raw
}

// externResultKind classifies an extern return annotation for the
// boundary lift (lowering-go.md §ForeignCall):
//
//	resultNone  — not a Result; the Go call lowers bare.
//	resultValue — Result<T, E> over a Go `(T, error)`; wrap tideResultOf.
//	resultUnit  — Result<unit, error> over a Go bare `error`; wrap
//	              tideResultUnit (the success value is `unit` → struct{}).
type externResultKind int

const (
	resultNone externResultKind = iota
	resultValue
	resultUnit
)

// externResultKindOf inspects a return annotation. A `Result<unit, …>`
// names a Go referent that returns a bare `error` (no value), so it
// needs the unit-wrapper rather than the two-value tideResultOf.
func externResultKindOf(rt ast.TypeExpr) externResultKind {
	nt, ok := rt.(*ast.NamedType)
	if !ok || len(nt.QName) != 1 || nt.QName[0] != "Result" {
		return resultNone
	}
	if len(nt.Args) >= 1 {
		if p, ok := nt.Args[0].(*ast.PrimitiveType); ok && p.Name == "unit" {
			return resultUnit
		}
	}
	return resultValue
}

// markExternLift records the helper a lift of the given kind needs, so
// the predeclared-helper pass emits it. Both lifts force the Result sum.
func (g *gen) markExternLift(kind externResultKind) {
	switch kind {
	case resultValue:
		g.usesResultOf = true
		g.usesResult = true
	case resultUnit:
		g.usesResultUnit = true
		g.usesResult = true
	}
}

// externLiftOpen writes the opening wrapper call for a lift kind (and
// records the helper), returning whether a closing paren is owed.
func (g *gen) externLiftOpen(kind externResultKind) bool {
	g.markExternLift(kind)
	switch kind {
	case resultValue:
		g.b.WriteString("tideResultOf(")
	case resultUnit:
		g.b.WriteString("tideResultUnit(")
	default:
		return false
	}
	return true
}

// externHandleName returns the opaque-handle type name of recv (via its
// sema type), or "" when recv is not a foreign handle.
func (g *gen) externHandleName(recv ast.Expr) string {
	if g.info == nil {
		return ""
	}
	named, ok := g.info.Type[recv].(*sema.Named)
	if !ok {
		return ""
	}
	if _, ok := named.Decl.(*ast.ExternTypeDecl); ok {
		return named.N
	}
	return ""
}

// externMethodOf returns the extern method a `recv.name(...)` call
// selects, when recv is a foreign handle carrying that method.
func (g *gen) externMethodOf(f *ast.Field) (*ast.ExternMethod, bool) {
	h := g.externHandleName(f.Receiver)
	if h == "" {
		return nil, false
	}
	if ms := g.externMethods[h]; ms != nil {
		if m, ok := ms[f.Name]; ok {
			return m, true
		}
	}
	return nil, false
}

// externFieldOf returns the extern field a `recv.name` access selects,
// when recv is a foreign handle carrying that field.
func (g *gen) externFieldOf(f *ast.Field) (*ast.ExternField, bool) {
	h := g.externHandleName(f.Receiver)
	if h == "" {
		return nil, false
	}
	if fs := g.externFields[h]; fs != nil {
		if fld, ok := fs[f.Name]; ok {
			return fld, true
		}
	}
	return nil, false
}

// emitExternFuncCall lowers `f(args)` for an extern function to
// `[tideResultOf(]pkg.Sym(args)[)]`.
func (g *gen) emitExternFuncCall(efd *ast.ExternFuncDecl, c *ast.Call) error {
	pkg, sym := goRefPkgSym(efd.Go, efd.Name)
	if pkg == "" {
		return fmt.Errorf("codegen: extern func %s has no @go package", efd.Name)
	}
	lift := g.externLiftOpen(externResultKindOf(efd.ReturnType))
	g.b.WriteString(goPkgRef(pkg))
	g.b.WriteByte('.')
	g.b.WriteString(sym)
	if err := g.emitArgList(c.Args); err != nil {
		return err
	}
	if lift {
		g.b.WriteByte(')')
	}
	return nil
}

// emitExternMethodCall lowers `recv.m(args)` on a foreign handle to
// `[tideResultOf(]recv.GoName(args)[)]`.
func (g *gen) emitExternMethodCall(m *ast.ExternMethod, f *ast.Field, c *ast.Call) error {
	goName := goRefMember(m.Go, m.Name)
	lift := g.externLiftOpen(externResultKindOf(m.ReturnType))
	if err := g.emitExpr(f.Receiver); err != nil {
		return err
	}
	g.b.WriteByte('.')
	g.b.WriteString(goName)
	if err := g.emitArgList(c.Args); err != nil {
		return err
	}
	if lift {
		g.b.WriteByte(')')
	}
	return nil
}
