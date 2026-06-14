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
	case [2]string{"json", "serialize"}, [2]string{"json", "serializeIndent"}:
		// json.serialize(v) / serializeIndent(v, prefix, indent) →
		// Result<[]byte, error> (binding-surface.md §encoding/json).
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

// genericBindingReturn types the generic bindings whose result type
// depends on the *call's* type arguments rather than a fixed table row:
// `json.parse<T>(data)` → `Result<T, error>`, and the `fmt.scan` family —
// `scan<T>` → `Result<T, error>`, `scan2<A,B>` →
// `Result<(A,B), error>`, `scan3<A,B,C>` → `Result<(A,B,C), error>`
// (binding-surface.md §fmt). Without this a `try fmt.scan<int>()` left
// its binding Unknown, which cascaded into "cannot infer Go type for
// tuple literal" once the value reached a tuple component (p1242).
// Mirrors codegen's scan lowering (internal/codegen/call.go,
// isFmtScan / fmtScanMultiArity).
func (c *checker) genericBindingReturn(call *ast.Call, f *ast.Field) Type {
	recv, ok := f.Receiver.(*ast.Ident)
	if !ok {
		return nil
	}
	if sym := c.info.Symbol[recv]; sym == nil || sym.Kind != SymBuiltinModule {
		return nil
	}
	errT := &Builtin{N: "error"}
	// json.parse<T>(data) → Result<T, error> — generic over the target
	// type, like the scan family (binding-surface.md §encoding/json).
	if recv.Name == "json" && f.Name == "parse" {
		if len(call.TypeArgs) != 1 {
			return nil
		}
		return &Result{T: c.typeFromExpr(call.TypeArgs[0]), E: errT}
	}
	if recv.Name != "fmt" {
		return nil
	}
	want := 0
	switch f.Name {
	case "scan":
		want = 1
	case "scan2":
		want = 2
	case "scan3":
		want = 3
	default:
		return nil
	}
	if len(call.TypeArgs) != want {
		return nil
	}
	if want == 1 {
		return &Result{T: c.typeFromExpr(call.TypeArgs[0]), E: errT}
	}
	comps := make([]Type, want)
	for i, ta := range call.TypeArgs {
		comps[i] = c.typeFromExpr(ta)
	}
	return &Result{T: &Tuple{Comps: comps}, E: errT}
}
