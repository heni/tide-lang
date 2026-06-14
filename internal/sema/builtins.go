package sema

// predeclaredSymbols seeds the bottom scope.
// Source: lang-spec/keywords.md §Built-in identifiers + codegen.isStdlibNamespace.
func predeclaredSymbols() map[string]*Symbol {
	out := map[string]*Symbol{}

	addType := func(name string) {
		out[name] = &Symbol{Name: name, Kind: SymBuiltinType, Type: &Builtin{N: name}}
	}
	addFunc := func(name string) {
		out[name] = &Symbol{Name: name, Kind: SymBuiltinFunc, Type: &Unknown{}}
	}
	addVar := func(name string) {
		out[name] = &Symbol{Name: name, Kind: SymBuiltinVariant, Type: &Unknown{}}
	}
	addMod := func(name string) {
		out[name] = &Symbol{Name: name, Kind: SymBuiltinModule, Type: &Unknown{}}
	}

	for _, t := range []string{
		"bool", "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64",
		"float32", "float64", "byte", "rune", "string",
		"Any", "Dynamic", "unit", "Never",
	} {
		addType(t)
	}
	// `error` is both a type (the interface) and a free function
	// (the constructor) per lang-spec/builtins.md. Sema-1 keeps
	// the type role; the call form re-uses the same symbol and
	// codegen routes the value lowering. Sema-3 will split the
	// dual role once typing rules need to distinguish them.
	out["error"] = &Symbol{Name: "error", Kind: SymBuiltinType, Type: &Builtin{N: "error"}}

	for _, t := range []string{"Option", "Result", "Map", "Set", "Stack", "Channel", "SendChan", "RecvChan"} {
		out[t] = &Symbol{Name: t, Kind: SymBuiltinType, Type: &Named{N: t}}
	}
	// `reflect.Kind` variants — qualified under `reflect.`, not
	// in unqualified scope. Sema-3 attaches them when resolving
	// `reflect.Primitive` field access on the reflect module.
	for _, v := range []string{"None", "Some", "Ok", "Err"} {
		addVar(v)
	}
	for _, fn := range []string{"panic", "refEq", "makeChannel", "makeSlice"} {
		addFunc(fn)
	}
	for _, m := range []string{
		"fmt", "os", "strings", "strconv", "bufio", "context",
		"time", "sync", "io", "log", "net", "encoding", "math",
		"reflect", "unicode", "sort", "json",
	} {
		addMod(m)
	}
	return out
}

// goReservedIdent — codegen-internal `_tide_` prefix per E0107.
// Lexer already rejects this; kept as defence-in-depth for
// synthesised names that bypass the lexer.
func goReservedIdent(name string) bool {
	return len(name) >= 6 && name[:6] == "_tide_"
}
