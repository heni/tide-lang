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
		"float32", "float64", "byte", "rune", "string", "error",
		"Any", "Dynamic", "unit", "Never",
	} {
		addType(t)
	}
	for _, t := range []string{"Option", "Result", "Map", "Set", "Stack", "Channel", "SendChan", "RecvChan"} {
		out[t] = &Symbol{Name: t, Kind: SymBuiltinType, Type: &Named{N: t}}
	}
	for _, v := range []string{
		"None", "Some", "Ok", "Err",
		"Primitive", "Class", "Sum", "Slice", "Function", "Unit",
	} {
		addVar(v)
	}
	for _, fn := range []string{"panic", "error", "refEq", "makeChannel", "makeSlice"} {
		addFunc(fn)
	}
	for _, m := range []string{
		"fmt", "os", "strings", "strconv", "bufio", "context",
		"time", "sync", "io", "log", "net", "encoding", "math",
		"reflect", "unicode",
	} {
		addMod(m)
	}
	return out
}

// goReservedIdent — codegen-internal `_tide_` prefix per E0107.
// Lexer already rejects this; kept as defence-in-depth for
// synthesised names that bypass the lexer.
func goReservedIdent(name string) bool {
	const reserved = "_tide_"
	return len(name) >= len(reserved) && name[:len(reserved)] == reserved
}
