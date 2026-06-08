package codegen

// bindings.go — the stdlib binding registry. A declarative table of
// the Go-stdlib calls and values the corpus uses, with the Tide-side
// `pkg.method` spelling on the left and the lowering on the right.
// Source of truth for the *intended* surface is
// `docs/binding-surface.md`; this file is the codegen realisation of
// the value-returning slice of it. The full bindgen pipeline (D6 —
// signatures from `go/packages`) supersedes this hand-written table
// later; until then it grows row-by-row as the corpus demands.
//
// Three lowering shapes are modelled here:
//   - rename:     `pkg.method(args)` → `pkg.GoName(args)` (or a value
//                 reference `pkg.GoName` for a non-call binding). The
//                 Go referent is value-returning (no error), so the
//                 call/field lowers straight through with only an
//                 identifier swap. Handled by mapFieldName.
//   - resultWrap: `pkg.method(args)` → `tideResultOf(pkg.GoName(args))`.
//                 The Go referent returns `(T, error)`; the helper
//                 folds that into the predeclared `Result<T, error>`.
//                 Handled by emitCall.
//   - conversion: `strings.fromBytes(b)` → `string(b)` etc. — a Go
//                 type conversion, not a package call (needs no
//                 import). Predates this table; see isConversionBinding
//                 + the emitCall special case.

// stdlibRename maps a value-returning stdlib binding to its Go
// identifier. Keyed by [pkg, method]. Covers both call bindings
// (`strings.split` → `strings.Split(...)`) and value bindings
// (`os.args` → `os.Args`); the call-vs-value distinction is the
// caller's (a Field vs a Call node), not the table's.
var stdlibRename = map[[2]string]string{
	// fmt — output + formatting (value/effect returning).
	{"fmt", "println"}:  "Println",
	{"fmt", "print"}:    "Print",
	{"fmt", "printf"}:   "Printf",
	{"fmt", "sprintf"}:  "Sprintf",
	{"fmt", "sprintln"}: "Sprintln",
	{"fmt", "sprint"}:   "Sprint",

	// os — process control + args (value bindings + diverging exit).
	{"os", "exit"}:   "Exit",
	{"os", "args"}:   "Args",
	{"os", "getenv"}: "Getenv",

	// strings — pure string transforms (all value-returning).
	{"strings", "fields"}:     "Fields",
	{"strings", "split"}:      "Split",
	{"strings", "join"}:       "Join",
	{"strings", "trimSpace"}:  "TrimSpace",
	{"strings", "trimPrefix"}: "TrimPrefix",
	{"strings", "trimSuffix"}: "TrimSuffix",
	{"strings", "hasPrefix"}:  "HasPrefix",
	{"strings", "hasSuffix"}:  "HasSuffix",
	{"strings", "contains"}:   "Contains",
	{"strings", "count"}:      "Count",
	{"strings", "replace"}:    "Replace",
	{"strings", "toLower"}:    "ToLower",
	{"strings", "toUpper"}:    "ToUpper",

	// strconv — the no-error direction (parsing is resultWrap below).
	{"strconv", "itoa"}:       "Itoa",
	{"strconv", "formatBool"}: "FormatBool",
	{"strconv", "quote"}:      "Quote",

	// math — float math (no error). floor/log10 predate the table.
	{"math", "floor"}: "Floor",
	{"math", "log10"}: "Log10",
	{"math", "sqrt"}:  "Sqrt",
	{"math", "abs"}:   "Abs",
	{"math", "pow"}:   "Pow",
	{"math", "log"}:   "Log",
	{"math", "log2"}:  "Log2",
	{"math", "ceil"}:  "Ceil",

	// time — the channel/effect bindings are direct renames; the
	// Duration constructors (`milliseconds`/`seconds`) are not, and
	// lower via timeDurationUnit in emitCall.
	{"time", "after"}: "After", // → <-chan time.Time
	{"time", "tick"}:  "Tick",  // → <-chan time.Time
	{"time", "sleep"}: "Sleep",
}

// stdlibResultWrap maps a stdlib binding whose Go referent returns
// `(T, error)` to its Go identifier. The call lowers to
// `tideResultOf(pkg.GoName(args))`, folding the two-value Go return
// into the predeclared `Result<T, error>` (binding-surface.md — the
// `(T, error)` → Result wrapper convention). Go infers T from the
// referent's first return, so one generic helper serves every row.
var stdlibResultWrap = map[[2]string]string{
	{"strconv", "atoi"}:       "Atoi",
	{"strconv", "parseFloat"}: "ParseFloat",
	{"strconv", "parseInt"}:   "ParseInt",
	{"os", "readFile"}:        "ReadFile",
}

// timeDurationUnit maps a `time.<ctor>(n)` Duration constructor to
// its Go `time.<Unit>` constant. The call lowers to
// `time.Duration(n) * time.<Unit>` (binding-surface.md §time —
// Tide hides Go's `time.Second * N` idiom behind factory funcs).
// ("", false) when name is not a Duration constructor.
func timeDurationUnit(name string) (string, bool) {
	switch name {
	case "seconds":
		return "Second", true
	case "milliseconds":
		return "Millisecond", true
	}
	return "", false
}

// stdlibConversion maps a binding that lowers to a Go *type
// conversion* `<target>(arg)` rather than a package call — so it pulls
// in no import (binding-surface.md). Single source of truth for both
// the lowering (emitCall) and the import-suppression check
// (isConversionBinding); a divergence between the two would mis-track
// imports.
var stdlibConversion = map[[2]string]string{
	{"strings", "fromBytes"}: "string", // []byte → string
}

// stdlibConversionOf returns the Go conversion target for a conversion
// binding `recv.name`, or ("", false) when the pair is not one.
func stdlibConversionOf(recv, name string) (string, bool) {
	g, ok := stdlibConversion[[2]string{recv, name}]
	return g, ok
}

// stdlibRenameOf returns the Go identifier for a value-returning
// binding `recv.name`, or ("", false) when recv is not a stdlib
// namespace ident or the pair has no rename entry.
func stdlibRenameOf(recv, name string) (string, bool) {
	if !isStdlibNamespaceName(recv) {
		return "", false
	}
	g, ok := stdlibRename[[2]string{recv, name}]
	return g, ok
}

// stdlibResultWrapOf returns the Go identifier for a `(T, error)`
// binding `recv.name`, or ("", false) when the pair is not a
// result-wrapping binding.
func stdlibResultWrapOf(recv, name string) (string, bool) {
	g, ok := stdlibResultWrap[[2]string{recv, name}]
	return g, ok
}
