package sema

import "github.com/heni/tide-lang/internal/ast"

// typeFromExpr lowers a (resolved) AST type annotation to the
// canonical sema Type. It leans on Info.Symbol, which resolve.go
// has already populated for every NamedType head, so it needs no
// scope argument.
//
// PR-Sema-C1 models scalars precisely and resolves transparent
// aliases through to their underlying type; every richer shape
// (slices, the predeclared containers, generic parameters,
// qualified module types) lowers to *Unknown, which the equality
// helpers treat as a wildcard. Later barrier PRs replace those
// Unknown returns with real cases as inference learns each shape.
func (c *checker) typeFromExpr(t ast.TypeExpr) Type {
	return c.typeFromExprSeen(t, nil)
}

// typeFromExprSeen carries the set of alias names currently being
// expanded so a cyclic alias (already reported as E0114) degrades
// to *Unknown instead of looping forever.
func (c *checker) typeFromExprSeen(t ast.TypeExpr, seen map[string]bool) Type {
	switch v := t.(type) {
	case nil:
		// A nil annotation at a return position means `unit`.
		return &Unit{}
	case *ast.PrimitiveType:
		if v.Name == "unit" {
			return &Unit{}
		}
		return &Builtin{N: v.Name}
	case *ast.SliceType:
		return &Slice{Elem: c.typeFromExprSeen(v.Elem, seen)}
	case *ast.TupleType:
		comps := make([]Type, len(v.Components))
		for i, ce := range v.Components {
			comps[i] = c.typeFromExprSeen(ce, seen)
		}
		return &Tuple{Comps: comps}
	case *ast.FuncType:
		params := make([]Type, len(v.Params))
		for i, pe := range v.Params {
			params[i] = c.typeFromExprSeen(pe, seen)
		}
		var ret Type = &Unit{}
		if v.ReturnType != nil {
			ret = c.typeFromExprSeen(v.ReturnType, seen)
		}
		return &Func{Params: params, Return: ret}
	case *ast.NamedType:
		return c.namedTypeToType(v, seen)
	default:
		// Closed-sum convention (docs/internals/sema.md §5): a new
		// TypeExpr shape must add a case here. The panic is the
		// audit net — mirrors checkTypeArity / aliasRefs.
		panic("sema.typeFromExpr: unhandled TypeExpr " + t.NodeKind())
	}
}

func (c *checker) namedTypeToType(v *ast.NamedType, seen map[string]bool) Type {
	if len(v.QName) == 0 {
		return &Unknown{}
	}
	// Qualified names (`pkg.Type`) are stdlib-binding surface in
	// v1 — not modelled by sema yet.
	if len(v.QName) > 1 {
		return &Unknown{}
	}
	head := v.QName[0]

	sym := c.info.Symbol[v]
	if sym == nil {
		// Unresolved (E0103 already reported) — stay silent.
		return &Unknown{}
	}

	switch sym.Kind {
	case SymBuiltinType:
		// `Any` / `Dynamic` are opaque builtins; Map / Set / Stack
		// are the modelled containers; Option / Result / Channel
		// stay Unknown until their own PRs.
		switch head {
		case "Any":
			return &Any{}
		case "Dynamic":
			return &Dynamic{}
		case "Map":
			if len(v.Args) == 2 {
				return &Map{Key: c.typeFromExprSeen(v.Args[0], seen), Val: c.typeFromExprSeen(v.Args[1], seen)}
			}
			return &Unknown{}
		case "Set":
			if len(v.Args) == 1 {
				return &Set{Elem: c.typeFromExprSeen(v.Args[0], seen)}
			}
			return &Unknown{}
		case "Stack":
			if len(v.Args) == 1 {
				return &Stack{Elem: c.typeFromExprSeen(v.Args[0], seen)}
			}
			return &Unknown{}
		}
		return &Unknown{}
	case SymTypeParam:
		return &Generic{Name: head}
	case SymClass:
		return &Named{N: head, Decl: sym.Decl}
	case SymInterface:
		return &Named{N: head, Decl: sym.Decl}
	case SymTypeDecl:
		td, ok := sym.Decl.(*ast.TypeDecl)
		if !ok {
			return &Named{N: head, Decl: sym.Decl}
		}
		switch body := td.Body.(type) {
		case *ast.AliasBody:
			// Transparent alias: `type Cents = int` makes Cents
			// equal to int. Guard against cycles.
			if seen[head] {
				return &Unknown{}
			}
			next := map[string]bool{head: true}
			for k := range seen {
				next[k] = true
			}
			return c.typeFromExprSeen(body.Aliased, next)
		default:
			// Sum / record / tuple alias — nominal.
			return &Named{N: head, Decl: td}
		}
	default:
		return &Unknown{}
	}
}

// funcSigType builds the canonical Func type of a top-level
// function declaration from its annotations (Barrier B).
func (c *checker) funcSigType(fn *ast.FuncDecl) *Func {
	params := make([]Type, len(fn.Params))
	for i, p := range fn.Params {
		params[i] = c.typeFromExpr(p.DeclType)
	}
	return &Func{Params: params, Return: c.typeFromExpr(fn.ReturnType), TypeParams: fn.TypeParams}
}

// methodSigType builds the canonical Func type of a class method.
func (c *checker) methodSigType(m *ast.Method) *Func {
	params := make([]Type, len(m.Params))
	for i, p := range m.Params {
		params[i] = c.typeFromExpr(p.DeclType)
	}
	return &Func{Params: params, Return: c.typeFromExpr(m.ReturnType)}
}

// satisfiesInterface reports whether `got` nominally conforms to the
// interface type `want` (D14): `got` is a class whose `implements`
// list names the interface, or an interface that is / `extends` it.
// One level of `extends` is followed.
func (c *checker) satisfiesInterface(want, got Type) bool {
	wn, ok := want.(*Named)
	if !ok {
		return false
	}
	wid, ok := wn.Decl.(*ast.InterfaceDecl)
	if !ok {
		return false // want is not an interface
	}
	gn, ok := got.(*Named)
	if !ok {
		return false
	}
	switch decl := gn.Decl.(type) {
	case *ast.ClassDecl:
		for _, impl := range decl.Implements {
			if c.namesInterface(impl, wid.Name) {
				return true
			}
		}
	case *ast.InterfaceDecl:
		if decl.Name == wid.Name {
			return true
		}
		for _, ext := range decl.Extends {
			if c.namesInterface(ext, wid.Name) {
				return true
			}
		}
	}
	return false
}

// namesInterface reports whether the type expression names interface
// `name`, directly or through one level of `extends`.
func (c *checker) namesInterface(t ast.TypeExpr, name string) bool {
	nt, ok := t.(*ast.NamedType)
	if !ok || len(nt.QName) != 1 {
		return false
	}
	if nt.QName[0] == name {
		return true
	}
	if sym := c.info.Symbol[nt]; sym != nil {
		if id, ok := sym.Decl.(*ast.InterfaceDecl); ok {
			for _, ext := range id.Extends {
				if c.namesInterface(ext, name) {
					return true
				}
			}
		}
	}
	return false
}

func (c *checker) interfaceMethodType(m *ast.InterfaceMethodSig) *Func {
	params := make([]Type, len(m.Params))
	for i, p := range m.Params {
		params[i] = c.typeFromExpr(p.DeclType)
	}
	return &Func{Params: params, Return: c.typeFromExpr(m.ReturnType)}
}

// symValueType is the type of a Symbol when it is *used as a
// value* (i.e. referenced by an Ident in expression position).
// Type-only or constructor symbols that PR-C1 does not yet type
// degrade to *Unknown.
func symValueType(sym *Symbol) Type {
	if sym == nil {
		return &Unknown{}
	}
	switch sym.Kind {
	case SymLocal, SymField:
		return sym.Type
	case SymFunc, SymMethod:
		if sym.Type != nil {
			return sym.Type
		}
		return &Unknown{}
	case SymUserVariant:
		// A nullary variant used as a value has the sum's type;
		// payload variants used bare are constructors (handled at
		// the call site). sym.Type already carries Named{sum}.
		return sym.Type
	default:
		// Classes-as-values, builtin funcs/variants, modules:
		// not value-typed yet.
		return &Unknown{}
	}
}
