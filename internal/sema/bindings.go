package sema

import "github.com/heni/tide-lang/internal/ast"

// bindings.go — interim sema-side stdlib binding signatures. Sema does
// not yet derive stdlib signatures from go/packages (the D6 bindgen
// pipeline); until it does, it needs one fact per binding: the *return
// type* of a value/Result-returning binding, so a `match`/`try` subject
// over a binding call is typed instead of staying Unknown. This mirrors
// the codegen binding tables (internal/codegen/bindings.go) — the two
// collapse into one bindgen-derived source later. Keyed [pkg, method];
// grows row-by-row until bindgen lands.

// stdlibBindingReturn returns the modelled Tide return type of a stdlib
// binding `pkg.method(...)`, or nil when the pair is not (yet) tabled.
// The `(T, error)`-wrapping bindings (codegen's stdlibResultWrap) map to
// Result<T, error>; their `Err` payload is the Go `error` boundary type.
func (c *checker) stdlibBindingReturn(pkg, method string) Type {
	errT := &Builtin{N: "error"}
	switch [2]string{pkg, method} {
	case [2]string{"strconv", "atoi"}:
		return &Result{T: &Builtin{N: "int"}, E: errT}
	case [2]string{"strconv", "parseInt"}:
		return &Result{T: &Builtin{N: "int64"}, E: errT}
	case [2]string{"strconv", "parseFloat"}:
		return &Result{T: &Builtin{N: "float64"}, E: errT}
	case [2]string{"os", "readFile"}:
		return &Result{T: &Slice{Elem: &Builtin{N: "byte"}}, E: errT}
	}
	return nil
}

// bindingCallReturn types a stdlib-binding call `recv.method(...)` whose
// receiver names a package (an Ident, not a local value). Returns nil
// when the callee is not a tabled binding.
func (c *checker) bindingCallReturn(f *ast.Field) Type {
	recv, ok := f.Receiver.(*ast.Ident)
	if !ok {
		return nil
	}
	// The receiver must name an imported package (SymBuiltinModule),
	// not a value binding that happens to share the name — a local
	// shadowing the package must not be treated as a namespace.
	sym := c.info.Symbol[recv]
	if sym == nil || sym.Kind != SymBuiltinModule {
		return nil
	}
	return c.stdlibBindingReturn(recv.Name, f.Name)
}
