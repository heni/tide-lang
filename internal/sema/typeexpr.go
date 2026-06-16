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
		// `Any` / `Dynamic` are opaque builtins; Map / Set / Stack,
		// Channel / SendChan / RecvChan, and Result / Option are the
		// modelled parametrised builtins; `error` is the predeclared
		// Go-error boundary type.
		switch head {
		case "Any":
			return &Any{}
		case "Dynamic":
			return &Dynamic{}
		case "error":
			return &Builtin{N: "error"}
		case "Result":
			if len(v.Args) == 2 {
				return &Result{T: c.typeFromExprSeen(v.Args[0], seen), E: c.typeFromExprSeen(v.Args[1], seen)}
			}
			return &Unknown{}
		case "Option":
			if len(v.Args) == 1 {
				return &Option{T: c.typeFromExprSeen(v.Args[0], seen)}
			}
			return &Unknown{}
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
		case "Channel":
			if len(v.Args) == 1 {
				return &Channel{Elem: c.typeFromExprSeen(v.Args[0], seen)}
			}
			return &Unknown{}
		case "SendChan":
			if len(v.Args) == 1 {
				return &SendChan{Elem: c.typeFromExprSeen(v.Args[0], seen)}
			}
			return &Unknown{}
		case "RecvChan":
			if len(v.Args) == 1 {
				return &RecvChan{Elem: c.typeFromExprSeen(v.Args[0], seen)}
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
	case SymExternType:
		// Opaque foreign handle — nominal, carries its ExternTypeDecl
		// so member access / refEq can recognise it (ffi.md §ExternType).
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
	params, variadic := c.paramTypes(fn.Params)
	return &Func{Params: params, Return: c.typeFromExpr(fn.ReturnType), TypeParams: fn.TypeParams, Variadic: variadic}
}

// methodSigType builds the canonical Func type of a class method.
func (c *checker) methodSigType(m *ast.Method) *Func {
	params, variadic := c.paramTypes(m.Params)
	return &Func{Params: params, Return: c.typeFromExpr(m.ReturnType), Variadic: variadic}
}

// paramSymType is the in-scope type of a parameter symbol: its
// declared type, or `[]T` for a variadic `...T` parameter.
func (c *checker) paramSymType(p *ast.Param) Type {
	t := c.typeFromExpr(p.DeclType)
	if p.Variadic {
		return &Slice{Elem: t}
	}
	return t
}

// paramTypes builds the canonical parameter Type list, wrapping a
// trailing variadic parameter's element type in a Slice (a `...T`
// parameter is in scope and called as `[]T`; ffi.md §Variadic). The
// second result reports whether the list ends in a variadic parameter.
func (c *checker) paramTypes(params []*ast.Param) ([]Type, bool) {
	out := make([]Type, len(params))
	variadic := false
	for i, p := range params {
		t := c.typeFromExpr(p.DeclType)
		if p.Variadic {
			t = &Slice{Elem: t}
			variadic = true
		}
		out[i] = t
	}
	return out, variadic
}

// externFuncSigType builds the Func type of an extern function from
// its annotations — the curated `.td` writes the lifted return type
// (e.g. `Result<T, error>`) directly, so no boundary-lift logic is
// needed here; that lift is a codegen concern (lowering-go.md §ForeignCall).
func (c *checker) externFuncSigType(fn *ast.ExternFuncDecl) *Func {
	params, variadic := c.paramTypes(fn.Params)
	return &Func{Params: params, Return: c.typeFromExpr(fn.ReturnType), TypeParams: fn.TypeParams, Variadic: variadic}
}

// externMethodSigType builds the Func type of an extern-impl method.
func (c *checker) externMethodSigType(m *ast.ExternMethod) *Func {
	params, variadic := c.paramTypes(m.Params)
	return &Func{Params: params, Return: c.typeFromExpr(m.ReturnType), Variadic: variadic}
}

// satisfiesInterface reports whether `got` nominally conforms to the
// interface type `want` (D14): `got` is a class whose `implements`
// list names the interface, or an interface that is / `extends` it.
// The `extends` chain is followed transitively.
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
// `name`, directly or transitively through the `extends` chain.
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
	params, variadic := c.paramTypes(m.Params)
	return &Func{Params: params, Return: c.typeFromExpr(m.ReturnType), Variadic: variadic}
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
	case SymLocal, SymField, SymTopLevelLet:
		return sym.Type
	case SymFunc, SymMethod, SymExternFunc:
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
