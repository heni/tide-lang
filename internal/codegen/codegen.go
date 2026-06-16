package codegen

import (
	"fmt"
	"go/format"
	"strconv"
	"strings"

	"github.com/heni/tide-lang/internal/ast"
	"github.com/heni/tide-lang/internal/sema"
)

// Emit lowers the given Tide AST to a Go source string. The
// returned text is gofmt-stable (round-trips through gofmt -s).
// file is the source path embedded into //line directives;
// pass "" to suppress them.
func Emit(f *ast.File, file string) (string, error) {
	// Codegen reads variable / receiver types from the sema
	// side-table. When called standalone (tests, tooling) it
	// computes Info here; cmd/tide passes the Info it already
	// produced via EmitWithInfo. Diagnostics are the caller's
	// concern — codegen only needs the type side-table.
	info, _ := sema.Check(f, file)
	return EmitWithInfo(f, file, info)
}

// EmitWithInfo is Emit with a pre-computed sema side-table.
func EmitWithInfo(f *ast.File, file string, info *sema.Info) (string, error) {
	return EmitFilesWithInfo([]*ast.File{f}, []string{file}, info)
}

// EmitFilesWithInfo lowers a whole package — every `.td` file in a
// directory shares one Go `package main` (RFC-0002 §"Package =
// directory"). The files are merged into one compile unit: the union of
// their imports plus the concatenation of their declarations. Each decl
// remembers its own source path so the //line directives attribute it
// to the right `.td` file. `paths[i]` is the //line path for files[i]
// ("" suppresses directives for that file).
func EmitFilesWithInfo(files []*ast.File, paths []string, info *sema.Info) (string, error) {
	merged := &ast.File{}
	declFile := map[ast.Decl]string{}
	seenImport := map[string]bool{}
	firstFile := ""
	for i, src := range files {
		if i == 0 {
			firstFile = paths[i]
		}
		for _, im := range src.Imports {
			if !seenImport[im.Path] {
				seenImport[im.Path] = true
				merged.Imports = append(merged.Imports, im)
			}
		}
		for _, d := range src.Decls {
			merged.Decls = append(merged.Decls, d)
			declFile[d] = paths[i]
		}
	}
	f := merged
	file := firstFile
	g := &gen{
		file:          file,
		info:          info,
		variant:       map[string]variantInfo{},
		class:         map[string]classInfo{},
		fieldTypes:    map[string]map[string]ast.TypeExpr{},
		usedGoPkgs:    map[string]bool{},
		externFunc:    map[string]*ast.ExternFuncDecl{},
		externType:    map[string]*ast.ExternTypeDecl{},
		externMethods: map[string]map[string]*ast.ExternMethod{},
		externFields:  map[string]map[string]*ast.ExternField{},
		externPkgs:    map[string]bool{},
	}
	// Pre-scan foreign-binding decls (ffi.md) so call / type / member
	// lowering and the import pre-walk can resolve them.
	g.scanExterns(f)
	// Predeclared sum-type variants per `lang-spec/builtins.md`
	// §Option / §Result / §Variant constructors. Registered up
	// front so identifier resolution and match-arm payload
	// binding find them. Payload-field names match the
	// auto-emitted Go-side struct (e.g. `Some.value` → field
	// `SomeValue` per the `<Var><FieldName>` convention).
	g.variant["None"] = variantInfo{owner: "Option", tag: 0}
	g.variant["Some"] = variantInfo{owner: "Option", tag: 1, fields: []*ast.FieldDecl{{Name: "value"}}}
	g.variant["Ok"] = variantInfo{owner: "Result", tag: 0, fields: []*ast.FieldDecl{{Name: "value"}}}
	g.variant["Err"] = variantInfo{owner: "Result", tag: 1, fields: []*ast.FieldDecl{{Name: "err"}}}
	// Predeclared container classes per `lang-spec/builtins.md`
	// §Map / §Set / §Stack. Registered with static-method names
	// so `Map<K, V>.new()` lowers through the PR-G2
	// static-method-on-generic-class path. Instance method calls
	// (`m.get(k)`, `s.add(e)`, ...) lower to plain Go method
	// dispatch on the inline-emitted struct.
	g.class["Map"] = classInfo{
		generic: true,
		statics: map[string]bool{"new": true},
	}
	g.class["Set"] = classInfo{
		generic: true,
		statics: map[string]bool{"new": true, "from": true},
	}
	g.class["Stack"] = classInfo{
		generic: true,
		statics: map[string]bool{"new": true},
	}
	// `import reflect` is a Tide-internal module — signals that
	// codegen should emit the reflection layer (Dynamic struct,
	// TypeDescriptor, registry, helper funcs). It is NOT a
	// Go-stdlib binding (D6 / D18); the Go-side `import "reflect"`
	// added by writeHeader is for the descriptor registry's
	// runtime type lookup, not user code.
	for _, im := range f.Imports {
		if im.Path == "reflect" {
			g.usesReflect = true
		}
	}
	// Pre-walk: detect predeclared-sum / container usage so the
	// header emits only the corresponding Go-side definitions.
	// Programs that touch none of them emit identical Go to
	// pre-F5b (no fixture churn for unrelated tests).
	g.detectPredeclaredUsage(f)
	// Transitive deps: container methods produce Option / Result
	// values, so any use of those containers forces those
	// predeclared sums into the binary too.
	if g.usesMap || g.usesStack {
		g.usesOption = true
	}
	if g.usesStack {
		g.usesResult = true
	}
	// reflect.unbox<T> returns Result<T, error>; reflect.fields /
	// reflect.fieldValue (PR-R2) will return Result<Dynamic>, so
	// any reflection use pulls Result + Option into the binary.
	if g.usesReflect {
		g.usesResult = true
		g.usesOption = true
		// Pre-collect runtime descriptors for every user-declared
		// class and sum type so the reflection-prelude emitted
		// from writeHeader can include the init() registration
		// block.
		// Collect set of declared class names first so field-type
		// resolution can reference them (descRef for a field of
		// class-type X is `tideDesc_X`).
		classNames := map[string]bool{}
		for _, d := range f.Decls {
			if cd, ok := d.(*ast.ClassDecl); ok && len(cd.TypeParams) == 0 {
				classNames[cd.Name] = true
			}
		}
		for _, d := range f.Decls {
			switch v := d.(type) {
			case *ast.ClassDecl:
				if len(v.TypeParams) != 0 {
					continue // generic-instantiation descriptors land later
				}
				var fields []fieldDescInfo
				for _, cf := range v.Fields {
					fields = append(fields, fieldDescInfo{
						tideName: cf.Name,
						descRef:  descRefForType(cf.DeclType, classNames),
					})
				}
				g.descriptors = append(g.descriptors, descInfo{
					tideName: v.Name,
					goType:   "*main." + v.Name,
					kind:     "KindClass",
					fields:   fields,
				})
			case *ast.TypeDecl:
				if _, ok := v.Body.(*ast.SumTypeBody); ok {
					g.descriptors = append(g.descriptors, descInfo{
						tideName: v.Name,
						goType:   "main." + v.Name,
						kind:     "KindSum",
					})
				}
			}
		}
	}
	// First pass — register sum-type variants so later
	// expression / pattern lowering can qualify Variant idents
	// to their Go-side constants and tag numbers. Also register
	// classes (PR-F4) so Call/Field lowering can detect
	// constructor calls and static-method calls.
	for _, d := range f.Decls {
		if td, ok := d.(*ast.TypeDecl); ok {
			if sb, ok := td.Body.(*ast.SumTypeBody); ok {
				for i, v := range sb.Variants {
					g.variant[v.Name] = variantInfo{owner: td.Name, tag: i, fields: v.Fields, sumTypeParams: td.TypeParams}
				}
			}
			if rb, ok := td.Body.(*ast.RecordTypeBody); ok {
				fts := map[string]ast.TypeExpr{}
				for _, fd := range rb.Fields {
					fts[fd.Name] = fd.DeclType
				}
				g.fieldTypes[td.Name] = fts
			}
		}
		if cd, ok := d.(*ast.ClassDecl); ok {
			ci := classInfo{
				statics: map[string]bool{},
				generic: len(cd.TypeParams) > 0,
			}
			for _, m := range cd.Methods {
				if m.IsStatic {
					ci.statics[m.Name] = true
				}
			}
			g.class[cd.Name] = ci
			fts := map[string]ast.TypeExpr{}
			for _, cf := range cd.Fields {
				fts[cf.Name] = cf.DeclType
			}
			g.fieldTypes[cd.Name] = fts
		}
	}
	// Register the predeclared Kind variants per
	// `lang-spec/builtins.md` §reflect AFTER user sum decls have
	// populated g.variant so collision is detectable. Once
	// `import reflect` is present, the names Primitive / Class /
	// Sum / Slice / Function / Unit are reserved at the variant
	// namespace; user sums sharing any of them yield E0104-style
	// ambiguity (sema PR moves this to a proper `.td`-coordinate
	// diagnostic).
	if g.usesReflect {
		kindVariants := []string{"Primitive", "Class", "Sum", "Slice", "Function", "Unit"}
		for i, name := range kindVariants {
			if existing, ok := g.variant[name]; ok && existing.owner != "Kind" {
				return "", fmt.Errorf("codegen: variant name %q in user sum-type %q collides with predeclared reflect.Kind.%s — rename the variant or drop `import reflect`", name, existing.owner, name)
			}
			g.variant[name] = variantInfo{owner: "Kind", tag: i}
		}
	}
	g.writeHeader(f)
	for _, d := range f.Decls {
		// Attribute this decl's //line directives to its own source
		// file (multi-file package, RFC-0002). Reset the emitted-line
		// memo so a same-numbered line in a different file still emits
		// a fresh directive.
		if g.file != declFile[d] {
			g.file = declFile[d]
			g.emittedLine = 0
		}
		switch v := d.(type) {
		case *ast.FuncDecl:
			if err := g.emitFuncDecl(v); err != nil {
				return "", err
			}
		case *ast.TypeDecl:
			if err := g.emitTypeDecl(v); err != nil {
				return "", err
			}
		case *ast.ClassDecl:
			if err := g.emitClassDecl(v); err != nil {
				return "", err
			}
		case *ast.InterfaceDecl:
			if err := g.emitInterfaceDecl(v); err != nil {
				return "", err
			}
		case *ast.TopLevelLet:
			// Module-level constant → package-level `var Name [T] =
			// value`. Go resolves package-var init order, so source
			// order need not be topological. Indent is 0 at package
			// scope; emitLetOrVar emits the same `var` form as a
			// body-level `let` (lowering-go.md §TopLevelLet).
			if err := g.emitLetOrVar(v.Span, v.Name, v.DeclType, v.Value); err != nil {
				return "", err
			}
		case *ast.ExternTypeDecl, *ast.ExternFuncDecl, *ast.ExternImplDecl:
			// Foreign bindings emit no Go of their own — the binding is
			// lowered at each call / type / member use site (ffi.md,
			// lowering-go.md §ForeignCall). The declarations are pure
			// signature metadata, pre-scanned by scanExterns.
		default:
			return "", fmt.Errorf("codegen: unhandled top-level decl %T", d)
		}
	}
	// gofmt -s pass — guarantees the output round-trips through
	// gofmt to itself (test-contract.md §GO, lowering-go.md
	// §Output formatting).
	out, err := format.Source([]byte(g.b.String()))
	if err != nil {
		// E0801 internal: codegen emitted malformed Go. This
		// should never reach a user under correct sema; if it
		// does, it's a compiler bug and the raw buffer is
		// included for compiler-developer triage only.
		return "", fmt.Errorf("internal[E0801]: codegen produced unparseable Go (please file a bug): %w\n--- raw output ---\n%s", err, g.b.String())
	}
	return string(out), nil
}

type gen struct {
	b      strings.Builder
	file   string
	info   *sema.Info
	indent int
	// emittedLine tracks the source line whose //line directive
	// has most recently been written, so we avoid emitting the
	// same directive twice in a row.
	emittedLine int
	// variant maps a variant identifier (e.g. "Red") to its
	// owning sum-type and declaration-order tag (per
	// lowering-go.md §Variant-tag numbering). Populated during
	// the first decl pass in Emit and consumed by expression /
	// pattern lowering.
	variant map[string]variantInfo
	// class maps a class name (e.g. "Counter") to its static
	// methods. Populated during the first decl pass in Emit.
	// emitCall uses this to detect constructor calls
	// (`Counter(...)` → `&Counter{...}`) and static-method
	// calls (`Counter.make(...)` → `counterMake(...)`).
	class map[string]classInfo
	// fieldTypes maps a record/class name to its fields' declared Tide
	// TypeExprs. emitBraceLit sets expectType from it so a constructor
	// field value whose type Go can't infer — a bare `None` / `Ok` in
	// `Envelope{ q: None, … }` — gets its type args stamped from the
	// field's declared type (§Constructor type-argument stamping).
	fieldTypes map[string]map[string]ast.TypeExpr
	// matchTempCounter generates unique temp names for the
	// subject of a `match` when any arm binds payload fields.
	// Per `lowering-go.md` §MatchIR — capture subject once to
	// keep side-effects from re-running per arm.
	matchTempCounter int
	// usesOption / usesResult — set by the pre-walk in Emit
	// when the program references the corresponding predeclared
	// sum type, either by NamedType ("Option"/"Result") or by
	// variant constructor / pattern (Some/None/Ok/Err). The
	// header emits the Go-side definitions only when set.
	usesOption    bool
	usesResult    bool
	usesMap       bool
	usesSet       bool
	usesStack     bool
	usesReflect   bool
	usesMakeSlice bool
	usesScan      bool
	// usesScan2 / usesScan3 — the multi-value stdin bindings
	// `fmt.scan2<A,B>()` / `fmt.scan3<A,B,C>()`, lowered to the
	// tideScan2 / tideScan3 helpers (Result<(A,B[,C]), error>).
	usesScan2    bool
	usesScan3    bool
	usesResultOf bool
	// usesResultUnit — an extern referent returning a bare Go `error`
	// (Tide `Result<unit, error>`) is lifted via the tideResultUnit
	// helper (lowering-go.md §ForeignCall).
	usesResultUnit bool
	// usesJSON — any json.* binding is used, so Go's encoding/json is
	// imported and (with usesOption) the Option ⇄ null/value JSON
	// methods are emitted. usesJSONParse additionally forces the
	// tideJSONParse helper (json.parse<T>).
	usesJSON      bool
	usesJSONParse bool
	usesTryRecv   bool
	usesScope     bool
	// usesSortSorted — `sort.sorted(s, less)` is used, so its inline
	// tideSorted helper (copy + sort.SliceStable) and Go's "sort"
	// import are needed.
	usesSortSorted bool
	// usesErrorCtor — the `error(msg)` free constructor (builtins.md)
	// is used, so its lowering `errors.New(msg)` needs Go's "errors".
	usesErrorCtor bool
	// groupVars is the stack of structured-concurrency group binding
	// names, one per enclosing `scope` IIFE. A `spawn` registers on
	// the innermost (top-of-stack). inSpawnBody flags that `return
	// Ok(())` / `return Err(e)` must lower to the group's error
	// channel (`return nil` / `return <e>`) rather than a Result.
	groupVars   []string
	inSpawnBody bool
	// usedGoPkgs — Go stdlib packages actually referenced in the
	// emitted output (a `pkg.Sym`). Populated by the pre-walk; the
	// import block is this set ∩ the .td imports, so a binding that
	// lowers to a Go conversion (strings.fromBytes → string(...))
	// does not drag in an unused import.
	usedGoPkgs map[string]bool
	// Foreign bindings (ffi.md), pre-scanned from the file's extern
	// decls before the import pre-walk. externFunc/externType key on
	// the Tide name; externMethods/externFields key handle→member.
	// externPkgs collects the Go import paths the emitted bindings
	// reference, added to the import block by writeHeader (these come
	// from `@go`, not the .td imports).
	externFunc    map[string]*ast.ExternFuncDecl
	externType    map[string]*ast.ExternTypeDecl
	externMethods map[string]map[string]*ast.ExternMethod
	externFields  map[string]map[string]*ast.ExternField
	externPkgs    map[string]bool
	// descriptors collected during emit — for each user-declared
	// type that has a Tide-side descriptor, we emit a
	// `tideDesc_<Name>` package-level var plus an init()
	// registration into the descriptor map keyed by the Go-side
	// type name. Consumed by reflect.box runtime lookup.
	descriptors []descInfo
	// curFuncReturn — the Tide return TypeExpr of the function /
	// method currently being emitted. Consumed by TryExpr
	// lowering to know whether the early-return target is
	// `Option<U>` or `Result<U, E>` and to extract U / E for
	// the wrapped return value.
	curFuncReturn ast.TypeExpr
	// expectType — the Tide TypeExpr the next emitted expression is
	// expected to produce, set at return / typed-binding positions and
	// consumed (then cleared) by emitCall to supply explicit type args
	// to predeclared Result/Option constructors whose un-constrained
	// type parameter Go cannot infer from the argument alone (`Ok(v)`
	// leaves E open, `Err(e)` leaves T open). nil when no context flows.
	expectType ast.TypeExpr
	// sumCtorArgs — Go type-arg strings for the generic sum currently
	// being constructed, threaded through a payload-variant ctor call's
	// arguments (§Generics). A nested *nullary* variant (`Leaf`) has no
	// argument for Go to infer from, so it stamps these explicitly
	// (`TreeLeaf[int]()`). nil outside a generic-sum ctor call.
	sumCtorArgs []string
	// tryTempCounter generates unique temp names for `try`
	// emission. Same hygiene as matchTempCounter.
	tryTempCounter int
	// tryHoist maps a `try` expression that has been pre-emitted as a
	// statement preamble (by hoistExprTries) to its temp identifier.
	// emitExpr substitutes `<tmp>.V` for the node, enabling `try` in
	// expression position (call args, operands) — desugaring.md §T-Try.
	tryHoist map[*ast.TryExpr]string
	// destructureTempCounter generates unique temp names for the
	// `let (a, b) = e` tuple-destructuring binding.
	destructureTempCounter int
	// loopTempCounter generates unique throwaway counter names for
	// `for _ in low..high` (a wildcard loop var over a numeric range,
	// where Go's `i++` form needs a named — not `_` — counter).
	loopTempCounter int
}

type variantInfo struct {
	owner         string           // owning sum-type name (e.g. "Color")
	tag           int              // declaration order, used for the Tag field
	fields        []*ast.FieldDecl // payload fields, nil/empty for nullary variants
	sumTypeParams []string         // owning sum's type params (`Tree<T>`); nil for non-generic
}

type classInfo struct {
	statics map[string]bool // names of `static` methods
	generic bool            // true iff the class has type parameters
}

// descInfo records one runtime type descriptor that codegen
// will emit at the bottom of the prelude (per
// `lang-spec/builtins.md` §reflect / `lang-spec/lowering-go.md`
// §Container types). `goType` is the Go-side type spelling used
// as the registry key (`*main.Counter` for classes,
// `main.Color` for sum types, etc.); `kind` is the spec's Kind
// enum value (KindClass / KindSum / KindPrimitive ...).
// `fields` carries per-class field metadata for PR-R2's
// `reflect.fields(t)` and `reflect.fieldValue(v, name)`. Empty
// for non-class descriptors.
type descInfo struct {
	tideName string
	goType   string
	kind     string // "KindClass" / "KindSum" / etc.
	fields   []fieldDescInfo
}

// fieldDescInfo is one entry in a class descriptor's field
// list. `tideName` is the Tide-source spelling (also the
// Go-side struct field name — emitted lowercase per the
// class-field lowering convention); `descRef` is the Go-side
// var name pointing at the field's type descriptor (e.g.,
// "tideDesc_int" for int, "tideDesc_Counter" for a class
// instance). When the field's static type has no resolvable
// descriptor (slices, generics, ...) descRef is the empty
// string and reflect.fields synthesises a placeholder.
type fieldDescInfo struct {
	tideName string
	descRef  string
}

func (g *gen) writeHeader(f *ast.File) {
	g.b.WriteString("package main\n\n")
	// PR-C bindings shortcut: every Tide import resolves to the
	// matching Go stdlib package by the same name. fmt → "fmt".
	// strconv → "strconv". etc. Sorted for determinism.
	//
	// `reflect` is Tide-internal (D6 / D18 — runtime-supplied,
	// not a Go-stdlib binding); it does NOT translate to a Go
	// import for the user, but if usesReflect is set we add Go's
	// `import "reflect"` for the descriptor registry's internal
	// runtime type lookup.
	seen := map[string]bool{}
	var paths []string
	add := func(p string) {
		if seen[p] {
			return
		}
		seen[p] = true
		paths = append(paths, p)
	}
	for _, im := range f.Imports {
		if im.Path == "reflect" {
			continue // Tide-internal
		}
		// Drop a stdlib import the generated Go never references —
		// e.g. a program whose only `strings` use is the
		// conversion-binding `strings.fromBytes`. Non-stdlib paths
		// (user modules) are always kept.
		if isStdlibNamespaceName(im.Path) && !g.usedGoPkgs[im.Path] {
			continue
		}
		// Tide import name → Go import path (json → encoding/json); all
		// other stdlib bindings share the name.
		add(goImportPath(im.Path))
	}
	if g.usesReflect {
		// reflect.TypeOf for the descriptor registry lookup, plus
		// strconv for the show-helper's primitive formatting.
		add("reflect")
		add("strconv")
	}
	if g.usesScope {
		// Structured-concurrency scopes lower onto an inline group
		// helper built from the standard library (no errgroup dep —
		// generated modules are stdlib-only).
		add("context")
		add("sync")
	}
	if g.usesErrorCtor {
		// `error(msg)` lowers to errors.New(msg) (builtins.md §error).
		add("errors")
	}
	if g.usesSortSorted {
		// sort.sorted lowers onto the tideSorted helper (sort.SliceStable).
		add("sort")
	}
	// Foreign-binding packages (ffi.md) — the Go import paths named by
	// `@go` attributes of the extern funcs/handles actually used. These
	// come from `@go`, not the .td imports, so they are added directly.
	for p := range g.externPkgs {
		add(p)
	}
	// Sort for determinism.
	for i := 1; i < len(paths); i++ {
		for j := i; j > 0 && paths[j-1] > paths[j]; j-- {
			paths[j-1], paths[j] = paths[j], paths[j-1]
		}
	}
	if len(paths) == 1 {
		g.b.WriteString("import \"")
		g.b.WriteString(paths[0])
		g.b.WriteString("\"\n\n")
	} else if len(paths) > 1 {
		g.b.WriteString("import (\n")
		for _, p := range paths {
			g.b.WriteString("\t\"")
			g.b.WriteString(p)
			g.b.WriteString("\"\n")
		}
		g.b.WriteString(")\n\n")
	}
	g.writePredeclaredSums()
	g.writePredeclaredContainers()
	g.writePredeclaredMakeSlice()
	g.writePredeclaredScan()
	g.writePredeclaredScan2()
	g.writePredeclaredScan3()
	g.writePredeclaredResultOf()
	g.writePredeclaredResultUnit()
	g.writePredeclaredJSONParse()
	g.writeOptionJSONMethods()
	g.writePredeclaredSortSorted()
	g.writePredeclaredTryRecv()
	g.writePredeclaredGroup()
	g.writePredeclaredReflect()
}

// writePredeclaredSums emits Go-side definitions for Option<T>
// and Result<T, E> per `lang-spec/builtins.md` §Option / §Result,
// **only** for the sum types the program actually references
// (per the pre-walk in `detectPredeclaredUsage`). Reflection-free
// programs that touch neither pay zero — same emitted Go as
// pre-F5b.
//
// Constructor functions take type-args explicitly so the user
// can write `Some(42)` (Go infers T from the arg), `None<int>()`,
// or `Err<int, string>("boom")` at sites where inference can't
// proceed. The bare-identifier shape `let x: Option<int> = None`
// (no parens) needs sema-driven type inference and lands later;
// until then the user writes the call form.
func (g *gen) writePredeclaredSums() {
	// Struct shape exactly per `lang-spec/lowering-go.md`
	// §Container types — runtime representation: `Option[T]`
	// fields `Tag` + `V`; `Result[T, E]` fields `Tag` + `V` + `E`.
	// The spec puts these in `tidert/runtime.go` and refers to
	// them as `tidert.Option[T]` / `tidert.Result[T, E]`; PR-F5b
	// emits them inline in `main` as a v1 transitional state.
	// Block R relocates them to `tidert/` without changing the
	// struct shape.
	if g.usesOption {
		g.b.WriteString("type Option[T any] struct {\n\tTag uint8\n\tV   T\n}\n")
		g.b.WriteString("func OptionSome[T any](value T) Option[T] {\n\treturn Option[T]{Tag: 1, V: value}\n}\n")
		g.b.WriteString("func OptionNone[T any]() Option[T] {\n\treturn Option[T]{Tag: 0}\n}\n")
	}
	if g.usesResult {
		g.b.WriteString("type Result[T any, E any] struct {\n\tTag uint8\n\tV   T\n\tE   E\n}\n")
		g.b.WriteString("func ResultOk[T any, E any](value T) Result[T, E] {\n\treturn Result[T, E]{Tag: 0, V: value}\n}\n")
		g.b.WriteString("func ResultErr[T any, E any](err E) Result[T, E] {\n\treturn Result[T, E]{Tag: 1, E: err}\n}\n")
	}
}

// writePredeclaredMakeSlice emits the inline helper for the
// `makeSlice<T>(n: int): []T` predeclared builtin (per
// `lang-spec/builtins.md` §makeSlice). Returns a fresh slice
// of length n with every element initialised to T's Go
// zero-value — which for a sum type spelt
// `type S = | First | ...` is the first variant (tag 0), so
// `makeSlice<S>(n)` naturally yields `[First, First, ...]`.
// Conditional on usage.
func (g *gen) writePredeclaredMakeSlice() {
	if !g.usesMakeSlice {
		return
	}
	g.b.WriteString(`func tideMakeSlice[T any](n int) []T { return make([]T, n) }
`)
}

// writePredeclaredScan emits the tideScan helper backing the
// `fmt.scan<T>()` binding (binding-surface.md §fmt). It wraps Go's
// pointer-mutation `fmt.Scan(&v)` into Result<T, error>: a read error
// becomes Err, a successful parse becomes Ok(v). Requires the
// predeclared Result sum (usesResult, forced alongside usesScan).
func (g *gen) writePredeclaredScan() {
	if !g.usesScan {
		return
	}
	g.b.WriteString(`func tideScan[T any]() Result[T, error] {
	var v T
	if _, err := fmt.Scan(&v); err != nil {
		return ResultErr[T, error](err)
	}
	return ResultOk[T, error](v)
}
`)
}

// writePredeclaredScan2 / writePredeclaredScan3 emit the multi-value
// stdin helpers backing `fmt.scan2<A,B>()` / `fmt.scan3<A,B,C>()`
// (binding-surface.md §fmt). Each wraps one `fmt.Scan(&a, &b, …)` of N
// pointers into Result<(A, B[, C]), error>, the tuple lowered to the
// anonymous `struct { _0 A; _1 B[; _2 C] }` codegen spells everywhere
// (matching goTypeFromSema), so the Ok payload destructures through the
// normal tuple-in-variant-payload match path. Conditional on usage;
// pulls in Result.
func (g *gen) writePredeclaredScan2() {
	if !g.usesScan2 {
		return
	}
	g.b.WriteString(`func tideScan2[A any, B any]() Result[struct { _0 A; _1 B }, error] {
	var a A
	var b B
	if _, err := fmt.Scan(&a, &b); err != nil {
		return ResultErr[struct { _0 A; _1 B }, error](err)
	}
	return ResultOk[struct { _0 A; _1 B }, error](struct { _0 A; _1 B }{a, b})
}
`)
}

func (g *gen) writePredeclaredScan3() {
	if !g.usesScan3 {
		return
	}
	g.b.WriteString(`func tideScan3[A any, B any, C any]() Result[struct { _0 A; _1 B; _2 C }, error] {
	var a A
	var b B
	var c C
	if _, err := fmt.Scan(&a, &b, &c); err != nil {
		return ResultErr[struct { _0 A; _1 B; _2 C }, error](err)
	}
	return ResultOk[struct { _0 A; _1 B; _2 C }, error](struct { _0 A; _1 B; _2 C }{a, b, c})
}
`)
}

// writePredeclaredResultOf emits the tideResultOf helper backing the
// `(T, error)` → Result<T, error> stdlib bindings (bindings.go —
// `strconv.atoi`, `os.readFile`, …). A non-nil error becomes Err, a
// successful value becomes Ok. Requires the predeclared Result sum
// (usesResult, forced alongside usesResultOf). Conditional on usage.
func (g *gen) writePredeclaredResultOf() {
	if !g.usesResultOf {
		return
	}
	g.b.WriteString(`func tideResultOf[T any](v T, err error) Result[T, error] {
	if err != nil {
		return ResultErr[T, error](err)
	}
	return ResultOk[T, error](v)
}
`)
}

// writePredeclaredResultUnit emits the tideResultUnit helper backing
// the bare-`error` → Result<unit, error> boundary lift for extern
// referents that return only an `error` (`os.Chdir`, `os.WriteFile`,
// …). `unit` lowers to Go's zero-byte struct{} (lowering-go.md
// §ForeignCall). Requires the predeclared Result sum (usesResult,
// forced alongside usesResultUnit). Conditional on usage.
func (g *gen) writePredeclaredResultUnit() {
	if !g.usesResultUnit {
		return
	}
	g.b.WriteString(`func tideResultUnit(err error) Result[struct{}, error] {
	if err != nil {
		return ResultErr[struct{}, error](err)
	}
	return ResultOk[struct{}, error](struct{}{})
}
`)
}

// writePredeclaredSortSorted emits the tideSorted helper backing
// `sort.sorted(s, less)` (binding-surface.md §sort): a comparator sort
// that returns a NEW slice (Tide preserves the input's immutability),
// built on Go's sort.SliceStable for a stable order. Conditional on use.
func (g *gen) writePredeclaredSortSorted() {
	if !g.usesSortSorted {
		return
	}
	g.b.WriteString(`func tideSorted[T any](s []T, less func(T, T) bool) []T {
	out := make([]T, len(s))
	copy(out, s)
	sort.SliceStable(out, func(i, j int) bool { return less(out[i], out[j]) })
	return out
}
`)
}

// writePredeclaredTryRecv emits the inline helper backing
// `ch.tryRecv()` (lowering-go.md §Channel lowering): a non-blocking
// receive that returns Some(v) when a value is ready, None when the
// channel buffer is empty. Conditional on usage; pulls in Option.
func (g *gen) writePredeclaredTryRecv() {
	if !g.usesTryRecv {
		return
	}
	g.b.WriteString(`func tideTryRecv[T any](ch <-chan T) Option[T] {
	select {
	case v := <-ch:
		return OptionSome[T](v)
	default:
		return OptionNone[T]()
	}
}
`)
}

// writePredeclaredGroup emits the inline structured-concurrency
// group helper backing `scope` / `spawn` (lowering-go.md §ScopeIR /
// §SpawnIR). It replicates errgroup.WithContext semantics with only
// `sync` + `context` (generated modules carry no external deps): the
// first spawned func to return a non-nil error stores it and cancels
// the derived context; Wait blocks for all spawns and returns that
// error. Conditional on usage.
func (g *gen) writePredeclaredGroup() {
	if !g.usesScope {
		return
	}
	g.b.WriteString(`type tideGroup struct {
	wg     sync.WaitGroup
	once   sync.Once
	err    error
	cancel context.CancelFunc
}

func tideNewGroup(parent context.Context) (*tideGroup, context.Context) {
	ctx, cancel := context.WithCancel(parent)
	return &tideGroup{cancel: cancel}, ctx
}

func (g *tideGroup) Go(f func() error) {
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		if err := f(); err != nil {
			g.once.Do(func() {
				g.err = err
				g.cancel()
			})
		}
	}()
}

func (g *tideGroup) Wait() error {
	g.wg.Wait()
	g.cancel()
	return g.err
}
`)
}

// writePredeclaredContainers emits the Go-side definitions for
// the predeclared container types Map / Set / Stack per
// `lang-spec/builtins.md` §Map / §Set / §Stack and
// `lang-spec/lowering-go.md` §Container types. Conditional —
// programs that don't reference a container emit no Go-side
// noise for it. Like writePredeclaredSums, the spec authoritative
// location is `tidert/runtime.go`; PR-F6 emits them inline in
// `main` as a v1 transitional state. Block R relocates without
// changing the struct shape.
func (g *gen) writePredeclaredContainers() {
	if g.usesMap {
		g.b.WriteString(`type Map[K comparable, V any] struct {
	m     map[K]V
	order []K
}
func mapNew[K comparable, V any]() *Map[K, V] {
	return &Map[K, V]{m: map[K]V{}, order: nil}
}
func (m *Map[K, V]) len() int { return len(m.order) }
func (m *Map[K, V]) has(k K) bool { _, ok := m.m[k]; return ok }
func (m *Map[K, V]) get(k K) Option[V] {
	if v, ok := m.m[k]; ok {
		return Option[V]{Tag: 1, V: v}
	}
	return Option[V]{Tag: 0}
}
func (m *Map[K, V]) set(k K, v V) {
	if _, ok := m.m[k]; !ok {
		m.order = append(m.order, k)
	}
	m.m[k] = v
}
func (m *Map[K, V]) delete(k K) {
	if _, ok := m.m[k]; !ok {
		return
	}
	delete(m.m, k)
	for i, kk := range m.order {
		if kk == k {
			m.order = append(m.order[:i], m.order[i+1:]...)
			return
		}
	}
}
func (m *Map[K, V]) keys() []K {
	out := make([]K, len(m.order))
	copy(out, m.order)
	return out
}
func (m *Map[K, V]) values() []V {
	out := make([]V, 0, len(m.order))
	for _, k := range m.order {
		out = append(out, m.m[k])
	}
	return out
}
`)
	}
	if g.usesSet {
		g.b.WriteString(`type Set[T comparable] struct {
	m     map[T]struct{}
	order []T
}
func setNew[T comparable]() *Set[T] {
	return &Set[T]{m: map[T]struct{}{}, order: nil}
}
func setFrom[T comparable](elems []T) *Set[T] {
	s := setNew[T]()
	for _, e := range elems {
		s.add(e)
	}
	return s
}
func (s *Set[T]) len() int { return len(s.order) }
func (s *Set[T]) has(e T) bool { _, ok := s.m[e]; return ok }
func (s *Set[T]) add(e T) {
	if _, ok := s.m[e]; ok {
		return
	}
	s.m[e] = struct{}{}
	s.order = append(s.order, e)
}
func (s *Set[T]) delete(e T) {
	if _, ok := s.m[e]; !ok {
		return
	}
	delete(s.m, e)
	for i, ee := range s.order {
		if ee == e {
			s.order = append(s.order[:i], s.order[i+1:]...)
			return
		}
	}
}
func (s *Set[T]) toSlice() []T {
	out := make([]T, len(s.order))
	copy(out, s.order)
	return out
}
`)
	}
	if g.usesStack {
		g.b.WriteString(`type Stack[T any] struct {
	xs []T
}
func stackNew[T any]() *Stack[T] {
	return &Stack[T]{xs: nil}
}
func (s *Stack[T]) len() int { return len(s.xs) }
func (s *Stack[T]) push(e T) {
	s.xs = append(s.xs, e)
}
func (s *Stack[T]) pop() Result[T, error] {
	n := len(s.xs)
	if n == 0 {
		var zero T
		return Result[T, error]{Tag: 1, E: tideEmptyStack, V: zero}
	}
	v := s.xs[n-1]
	s.xs = s.xs[:n-1]
	return Result[T, error]{Tag: 0, V: v}
}
func (s *Stack[T]) peek() Option[T] {
	n := len(s.xs)
	if n == 0 {
		return Option[T]{Tag: 0}
	}
	return Option[T]{Tag: 1, V: s.xs[n-1]}
}

var tideEmptyStack = tideEmptyStackError{}

type tideEmptyStackError struct{}

func (tideEmptyStackError) Error() string { return "empty stack" }
`)
	}
}

// predeclaredPayloadField returns the Go-side struct field name
// for a predeclared sum-type variant's payload, per spec
// (`lang-spec/lowering-go.md` §Container types). Returns the
// empty string for non-predeclared variants; callers fall back
// to the PR-F5a `<Variant><FieldName>` convention.
func predeclaredPayloadField(variantName string) string {
	switch variantName {
	case "Some", "Ok":
		return "V"
	case "Err":
		return "E"
	}
	return ""
}

// detectPredeclaredUsage walks the file AST and sets
// g.usesOption / g.usesResult when any reference is found —
// type position (NamedType), variant constructor (Ident lookup
// in g.variant), or match-arm pattern (VariantPat or
// IdentPat-bound-to-variant). Conservative: any of the four
// constructor names (Some/None/Ok/Err) flips the corresponding
// flag even if the reference is later determined to be a
// shadow. Acceptable for v1; sema will tighten.
func (g *gen) detectPredeclaredUsage(f *ast.File) {
	var walk func(n ast.Node)
	walk = func(n ast.Node) {
		if n == nil {
			return
		}
		switch v := n.(type) {
		case *ast.NamedType:
			if len(v.QName) == 1 {
				switch v.QName[0] {
				case "Option":
					g.usesOption = true
				case "Result":
					g.usesResult = true
				case "Map":
					g.usesMap = true
				case "Set":
					g.usesSet = true
				case "Stack":
					g.usesStack = true
				}
				// A handle named in a type position (a param / return /
				// field annotation) lowers to `*pkg.Sym` and so needs the
				// package imported, even when no constructor from that
				// package is called in this file (ffi.md §ForeignCall).
				if etd, isHandle := g.externType[v.QName[0]]; isHandle {
					if pkg, _ := goRefPkgSym(etd.Go, etd.Name); pkg != "" {
						g.externPkgs[pkg] = true
					}
				}
			}
			for _, a := range v.Args {
				walk(a)
			}
		case *ast.Ident:
			switch v.Name {
			case "None", "Some":
				g.usesOption = true
			case "Ok", "Err":
				g.usesResult = true
			case "Map":
				g.usesMap = true
			case "Set":
				g.usesSet = true
			case "Stack":
				g.usesStack = true
			case "reflect":
				g.usesReflect = true
			case "makeSlice":
				g.usesMakeSlice = true
			}
		case *ast.VariantPat:
			if len(v.QName) > 0 {
				switch v.QName[len(v.QName)-1] {
				case "None", "Some":
					g.usesOption = true
				case "Ok", "Err":
					g.usesResult = true
				case "Primitive", "Class", "Sum", "Slice", "Function", "Unit":
					// Kind variants used in a match arm. The
					// match subject must be a reflect.kind() call
					// for sema; here we conservatively flag
					// usesReflect so the predeclared Kind variant
					// table is populated.
					g.usesReflect = true
				}
			}
			for _, s := range v.Sub {
				walk(s)
			}
		case *ast.IdentPat:
			switch v.Name {
			case "None", "Some":
				g.usesOption = true
			case "Ok", "Err":
				g.usesResult = true
			}
		case *ast.FuncDecl:
			for _, p := range v.Params {
				walk(p)
			}
			walk(v.ReturnType)
			walk(v.Body)
		case *ast.ClassDecl:
			for _, fd := range v.Fields {
				walk(fd)
			}
			for _, m := range v.Methods {
				walk(m)
			}
		case *ast.InterfaceDecl:
			for _, e := range v.Extends {
				walk(e)
			}
			for _, m := range v.Methods {
				for _, prm := range m.Params {
					walk(prm.DeclType)
				}
				walk(m.ReturnType)
			}
		case *ast.ClassField:
			walk(v.DeclType)
		case *ast.Method:
			for _, p := range v.Params {
				walk(p)
			}
			walk(v.ReturnType)
			walk(v.Body)
		case *ast.Param:
			walk(v.DeclType)
		case *ast.TypeDecl:
			walk(v.Body)
		case *ast.TopLevelLet:
			walk(v.DeclType)
			walk(v.Value)
		case *ast.AliasBody:
			walk(v.Aliased)
		case *ast.SumTypeBody:
			for _, vr := range v.Variants {
				for _, fd := range vr.Fields {
					walk(fd)
				}
			}
		case *ast.RecordTypeBody:
			for _, fd := range v.Fields {
				walk(fd)
			}
		case *ast.FieldDecl:
			walk(v.DeclType)
		case *ast.Block:
			for _, s := range v.Stmts {
				walk(s)
			}
			if v.Trailing != nil {
				walk(v.Trailing)
			}
		case *ast.ExprStmt:
			walk(v.Expr)
		case *ast.LetStmt:
			walk(v.Pattern)
			walk(v.DeclType)
			walk(v.Value)
		case *ast.VarStmt:
			walk(v.DeclType)
			walk(v.Value)
		case *ast.AssignStmt:
			walk(v.LValue)
			walk(v.Value)
		case *ast.IfStmt:
			walk(v.Cond)
			walk(v.ThenBlock)
			walk(v.Else)
		case *ast.IfExpr:
			walk(v.Cond)
			walk(v.ThenBlock)
			walk(v.Else)
		case *ast.ForStmt:
			walk(v.Pattern)
			walk(v.Iterable)
			walk(v.Body)
		case *ast.WhileStmt:
			walk(v.Cond)
			walk(v.Body)
		case *ast.DeferStmt:
			walk(v.Call)
		case *ast.ScopeExpr:
			// A scope evaluates to Result<T, error> and lowers onto
			// the inline group helper — pull both into the binary.
			g.usesScope = true
			g.usesResult = true
			for _, ta := range v.TypeArgs {
				walk(ta)
			}
			walk(v.Parent)
			walk(v.Body)
		case *ast.SpawnExpr:
			walk(v.Body)
		case *ast.SelectStmt:
			for _, sc := range v.Cases {
				switch cse := sc.(type) {
				case *ast.SelectRecv:
					walk(cse.Channel)
					walk(cse.Body)
				case *ast.SelectSend:
					walk(cse.Channel)
					walk(cse.Value)
					walk(cse.Body)
				case *ast.SelectDefault:
					walk(cse.Body)
				}
			}
		case *ast.ReturnExpr:
			walk(v.Value)
		case *ast.TryExpr:
			// `try e` — recurse into the wrapped expression so a
			// binding nested under it (e.g. `try strconv.atoi(s)`)
			// still registers its package import + helper usage.
			walk(v.Inner)
		case *ast.MatchExpr:
			walk(v.Subject)
			for _, arm := range v.Arms {
				walk(arm.Pattern)
				walk(arm.Body)
			}
		case *ast.Call:
			// `fmt.scan<T>()` lowers to the tideScan helper, which
			// returns Result<T, error> — pull both into the binary.
			if isFmtScan(v.Callee) {
				g.usesScan = true
				g.usesResult = true
			}
			// `fmt.scan2`/`fmt.scan3` lower to the tideScan2/tideScan3
			// helpers, which return Result<(…), error> — pull both in.
			if n := fmtScanMultiArity(v.Callee); n == 2 {
				g.usesScan2 = true
				g.usesResult = true
			} else if n == 3 {
				g.usesScan3 = true
				g.usesResult = true
			}
			// A `(T, error)` stdlib binding (`strconv.atoi`,
			// `os.readFile`, …) lowers via the tideResultOf helper,
			// which returns Result<T, error> — pull both in.
			if f, ok := v.Callee.(*ast.Field); ok {
				if recv, ok := f.Receiver.(*ast.Ident); ok {
					if _, isWrap := stdlibResultWrapOf(recv.Name, f.Name); isWrap {
						g.usesResultOf = true
						g.usesResult = true
					}
				}
			}
			// `ch.tryRecv()` lowers to the tideTryRecv helper, which
			// returns Option<T> — pull both into the binary. Keyed on
			// the method name (the receiver's channel kind is a sema
			// fact); a same-named user method would over-pull the
			// helper, harmless dead code.
			// `error(msg)` free constructor → errors.New(msg).
			if g.isErrorCtorCall(v) {
				g.usesErrorCtor = true
			}
			// `sort.sorted(s, less)` lowers to the inline tideSorted
			// helper, which needs Go's "sort". Gated on the sema symbol
			// (as the emitCall intercept is) so a user `sort` value
			// doesn't drag in the import + helper.
			if f, ok := v.Callee.(*ast.Field); ok && f.Name == "sorted" {
				if recv, ok := f.Receiver.(*ast.Ident); ok && recv.Name == "sort" && g.isBuiltinModule(recv) {
					g.usesSortSorted = true
				}
			}
			// json.* bindings (binding-surface.md §encoding/json). Gated
			// on the sema symbol like sort.sorted. parse<T> needs the
			// tideJSONParse helper; serialize/serializeIndent reuse
			// tideResultOf. Either marks usesJSON so the Option JSON
			// methods + encoding/json import are pulled in.
			if f, ok := v.Callee.(*ast.Field); ok {
				if recv, ok := f.Receiver.(*ast.Ident); ok && recv.Name == "json" && g.isBuiltinModule(recv) {
					switch f.Name {
					case "parse":
						g.usesJSON = true
						g.usesJSONParse = true
						g.usesResult = true
					case "serialize", "serializeIndent":
						g.usesJSON = true
						g.usesResultOf = true
						g.usesResult = true
					}
				}
			}
			if f, ok := v.Callee.(*ast.Field); ok && f.Name == "tryRecv" {
				g.usesTryRecv = true
				g.usesOption = true
			}
			// Foreign bindings (ffi.md): an extern func call pulls its
			// `@go` package into the import block, and a `Result<…>`
			// return (func or handle method) pulls the tideResultOf helper.
			if id, ok := v.Callee.(*ast.Ident); ok {
				if efd, isExtern := g.externFunc[id.Name]; isExtern {
					if pkg, _ := goRefPkgSym(efd.Go, efd.Name); pkg != "" {
						g.externPkgs[pkg] = true
					}
					g.markExternLift(externResultKindOf(efd.ReturnType))
				}
			}
			if f, ok := v.Callee.(*ast.Field); ok {
				if m, isExtern := g.externMethodOf(f); isExtern {
					g.markExternLift(externResultKindOf(m.ReturnType))
				}
			}
			walk(v.Callee)
			for _, ta := range v.TypeArgs {
				walk(ta)
			}
			for _, a := range v.Args {
				walk(a)
			}
		case *ast.SpreadArg:
			// A spread arg `...e` carries its expression in Inner; the
			// pre-walk must descend so an import/helper used only inside
			// the spread is registered (else the emitted Go drops it).
			walk(v.Inner)
		case *ast.Field:
			// A `pkg.method` reference marks the Go package used —
			// unless the (pkg, method) pair lowers to a Go conversion
			// rather than a package call (strings.fromBytes →
			// string(...)), which needs no import.
			if recv, ok := v.Receiver.(*ast.Ident); ok && isStdlibNamespaceName(recv.Name) {
				if !isConversionBinding(recv.Name, v.Name) {
					g.usedGoPkgs[recv.Name] = true
				}
			}
			walk(v.Receiver)
		case *ast.ParenExpr:
			walk(v.Inner)
		case *ast.TupleLit:
			for _, ce := range v.Components {
				walk(ce)
			}
		case *ast.TupleField:
			walk(v.Receiver)
		case *ast.BraceLit:
			walk(v.TypeName)
			for _, e := range v.Entries {
				switch en := e.(type) {
				case *ast.RecordEntry:
					walk(en.Value)
				case *ast.MapEntry:
					walk(en.Key)
					walk(en.Value)
				case *ast.SetEntry:
					walk(en.Value)
				}
			}
		case *ast.Binary:
			walk(v.Left)
			walk(v.Right)
		case *ast.Unary:
			walk(v.Operand)
		case *ast.Index:
			walk(v.Receiver)
			walk(v.Idx)
		case *ast.Slice:
			walk(v.Receiver)
			walk(v.Low)
			walk(v.High)
		case *ast.SliceLit:
			walk(v.ElemType)
			for _, it := range v.Items {
				walk(it)
			}
		case *ast.SliceType:
			walk(v.Elem)
		case *ast.TupleType:
			for _, ct := range v.Components {
				walk(ct)
			}
		case *ast.FuncType:
			for _, pt := range v.Params {
				walk(pt)
			}
			walk(v.ReturnType)
		case *ast.ClosureLit:
			for _, prm := range v.Params {
				walk(prm.DeclType)
			}
			walk(v.ReturnType)
			walk(v.Body)
		case *ast.RangeExpr:
			walk(v.Low)
			walk(v.High)
		}
	}
	for _, d := range f.Decls {
		walk(d)
	}
}

// emitTypeDecl lowers a TypeDecl. PR-F2 handles SumTypeBody
// (nullary-only) → Go `type T int` + `const (TVariant T = iota;
// ...)`, and AliasBody → Go `type T = U`.
func (g *gen) emitTypeDecl(td *ast.TypeDecl) error {
	switch body := td.Body.(type) {
	case *ast.AliasBody:
		g.line(td.Span.StartLine)
		g.b.WriteString("type ")
		g.b.WriteString(goIdent(td.Name))
		g.b.WriteString(" = ")
		if err := g.emitTypeExpr(body.Aliased); err != nil {
			return err
		}
		g.b.WriteByte('\n')
		return nil
	case *ast.RecordTypeBody:
		// A nominal record lowers to a named Go struct. Fields are
		// EXPORTED and carry a `json:"<tideName>"` tag so encoding/json
		// (reflecting from outside package main) round-trips them
		// (lowering-go.md §Record lowering).
		g.line(td.Span.StartLine)
		g.b.WriteString("type ")
		g.b.WriteString(goIdent(td.Name))
		g.emitTypeParamBrackets(td.TypeParams, true)
		g.b.WriteString(" struct {\n")
		g.indent++
		for _, f := range body.Fields {
			g.writeIndent()
			g.b.WriteString(exportFieldName(f.Name))
			g.b.WriteByte(' ')
			if err := g.emitTypeExpr(f.DeclType); err != nil {
				return err
			}
			g.writeJSONTag(f.Name)
			g.b.WriteByte('\n')
		}
		g.indent--
		g.b.WriteString("}\n")
		return nil
	case *ast.SumTypeBody:
		// Lower to a tagged struct per lowering-go.md §MatchIR.
		// The struct holds Tag + every variant's payload fields
		// (renamed to <VariantName><FieldName> to avoid clash
		// across variants). Nullary variants get a `var T_V =
		// T{Tag: N}`; payload variants get a `func T_V(...) T`
		// constructor. Tag is declaration order (§Variant-tag
		// numbering). A payload field that names the sum itself is
		// pointer-ized to break Go's infinite-size cycle (§Recursive
		// sum types).
		g.line(td.Span.StartLine)
		g.b.WriteString("type ")
		g.b.WriteString(goIdent(td.Name))
		g.emitTypeParamBrackets(td.TypeParams, true)
		g.b.WriteString(" struct {\n\tTag uint8\n")
		for _, v := range body.Variants {
			for _, f := range v.Fields {
				g.b.WriteByte('\t')
				g.b.WriteString(payloadFieldName(v.Name, f.Name))
				g.b.WriteByte(' ')
				if isSelfRefField(f, td.Name) {
					g.b.WriteByte('*')
				}
				if err := g.emitTypeExpr(f.DeclType); err != nil {
					return err
				}
				g.b.WriteByte('\n')
			}
		}
		g.b.WriteString("}\n")
		generic := len(td.TypeParams) > 0
		// Nullary variants. For a non-generic sum they are package-level
		// `var T_V = T{Tag: N}` consts; for a generic sum the value
		// needs a type argument Go can't supply at package scope, so each
		// becomes a parameterless generic constructor `func T_V[..]() T[..]`
		// (the OptionNone shape — §Generics).
		if generic {
			for i, v := range body.Variants {
				if len(v.Fields) != 0 {
					continue
				}
				g.b.WriteString("func ")
				g.b.WriteString(goIdent(td.Name))
				g.b.WriteString(goIdent(v.Name))
				g.emitTypeParamBrackets(td.TypeParams, true)
				g.b.WriteString("() ")
				g.b.WriteString(goIdent(td.Name))
				g.emitTypeParamBrackets(td.TypeParams, false)
				g.b.WriteString(" {\n\treturn ")
				g.b.WriteString(goIdent(td.Name))
				g.emitTypeParamBrackets(td.TypeParams, false)
				g.b.WriteString("{Tag: ")
				g.b.WriteString(strconv.Itoa(i))
				g.b.WriteString("}\n}\n")
			}
		} else {
			anyNullary := false
			for _, v := range body.Variants {
				if len(v.Fields) == 0 {
					anyNullary = true
					break
				}
			}
			if anyNullary {
				g.b.WriteString("var (\n")
				for i, v := range body.Variants {
					if len(v.Fields) != 0 {
						continue
					}
					g.b.WriteByte('\t')
					g.b.WriteString(goIdent(td.Name))
					g.b.WriteString(goIdent(v.Name))
					g.b.WriteString(" = ")
					g.b.WriteString(goIdent(td.Name))
					g.b.WriteByte('{')
					g.b.WriteString("Tag: ")
					g.b.WriteString(strconv.Itoa(i))
					g.b.WriteString("}\n")
				}
				g.b.WriteString(")\n")
			}
		}
		for i, v := range body.Variants {
			if len(v.Fields) == 0 {
				continue
			}
			g.b.WriteString("func ")
			g.b.WriteString(goIdent(td.Name))
			g.b.WriteString(goIdent(v.Name))
			g.emitTypeParamBrackets(td.TypeParams, true)
			g.b.WriteByte('(')
			for j, f := range v.Fields {
				if j > 0 {
					g.b.WriteString(", ")
				}
				g.b.WriteString(goIdent(f.Name))
				g.b.WriteByte(' ')
				if err := g.emitTypeExpr(f.DeclType); err != nil {
					return err
				}
			}
			g.b.WriteString(") ")
			g.b.WriteString(goIdent(td.Name))
			g.emitTypeParamBrackets(td.TypeParams, false)
			g.b.WriteString(" {\n\treturn ")
			g.b.WriteString(goIdent(td.Name))
			g.emitTypeParamBrackets(td.TypeParams, false)
			g.b.WriteByte('{')
			g.b.WriteString("Tag: ")
			g.b.WriteString(strconv.Itoa(i))
			for _, f := range v.Fields {
				g.b.WriteString(", ")
				g.b.WriteString(payloadFieldName(v.Name, f.Name))
				g.b.WriteString(": ")
				if isSelfRefField(f, td.Name) {
					g.b.WriteByte('&')
				}
				g.b.WriteString(goIdent(f.Name))
			}
			g.b.WriteString("}\n}\n")
		}
		return nil
	}
	return fmt.Errorf("codegen: unhandled TypeBody %T", td.Body)
}

// emitClassDecl lowers a ClassDecl per lowering-go.md
// §Implicit receiver. v1 always uses a pointer receiver for
// instance methods (§"For v1 every class uses a pointer
// receiver unconditionally"). Static methods lower to
// package-level functions named `<class-lowercase> + Cap(method)`.
func (g *gen) emitClassDecl(cd *ast.ClassDecl) error {
	g.line(cd.Span.StartLine)
	g.b.WriteString("type ")
	g.b.WriteString(goIdent(cd.Name))
	g.emitTypeParamBrackets(cd.TypeParams, true) // declaration: with `any` constraints
	g.b.WriteString(" struct {\n")
	for _, f := range cd.Fields {
		g.b.WriteByte('\t')
		g.b.WriteString(exportFieldName(f.Name))
		g.b.WriteByte(' ')
		if err := g.emitTypeExpr(f.DeclType); err != nil {
			return err
		}
		g.writeJSONTag(f.Name)
		g.b.WriteByte('\n')
	}
	g.b.WriteString("}\n")
	for _, m := range cd.Methods {
		if err := g.emitMethod(cd.Name, cd.TypeParams, m); err != nil {
			return err
		}
	}
	return nil
}

// emitInterfaceDecl lowers `interface Name { sig … }` to a Go
// interface. `extends` interfaces are embedded; method signatures
// emit `name(paramTypes) R`. Conformance is structural in Go, so a
// class that has the methods satisfies it (D14's explicit `implements`
// is sema-checked, not encoded in the Go interface).
func (g *gen) emitInterfaceDecl(id *ast.InterfaceDecl) error {
	g.line(id.Span.StartLine)
	g.b.WriteString("type ")
	g.b.WriteString(goIdent(id.Name))
	g.b.WriteString(" interface {\n")
	for _, e := range id.Extends {
		g.b.WriteByte('\t')
		if err := g.emitTypeExpr(e); err != nil {
			return err
		}
		g.b.WriteByte('\n')
	}
	for _, m := range id.Methods {
		g.b.WriteByte('\t')
		g.b.WriteString(goIdent(m.Name))
		g.b.WriteByte('(')
		for i, prm := range m.Params {
			if i > 0 {
				g.b.WriteString(", ")
			}
			if err := g.emitTypeExpr(prm.DeclType); err != nil {
				return err
			}
		}
		g.b.WriteByte(')')
		if m.ReturnType != nil {
			g.b.WriteByte(' ')
			if err := g.emitTypeExpr(m.ReturnType); err != nil {
				return err
			}
		}
		g.b.WriteByte('\n')
	}
	g.b.WriteString("}\n")
	return nil
}

func (g *gen) emitMethod(className string, classTypeParams []string, m *ast.Method) error {
	g.line(m.Span.StartLine)
	g.b.WriteString("func ")
	if !m.IsStatic {
		g.b.WriteString("(t *")
		g.b.WriteString(goIdent(className))
		g.emitTypeParamBrackets(classTypeParams, false) // receiver: type params without constraints
		g.b.WriteString(") ")
		g.b.WriteString(goIdent(m.Name))
	} else {
		g.b.WriteString(staticMethodName(className, m.Name))
		g.emitTypeParamBrackets(classTypeParams, true) // static = package-level func, declare constraints
	}
	g.b.WriteByte('(')
	for i, p := range m.Params {
		if i > 0 {
			g.b.WriteString(", ")
		}
		g.b.WriteString(goIdent(p.Name))
		g.b.WriteByte(' ')
		if p.Variadic {
			g.b.WriteString("...")
		}
		if err := g.emitTypeExpr(p.DeclType); err != nil {
			return err
		}
	}
	g.b.WriteByte(')')
	if m.ReturnType != nil {
		g.b.WriteByte(' ')
		if err := g.emitTypeExpr(m.ReturnType); err != nil {
			return err
		}
	}
	g.b.WriteString(" {\n")
	g.indent++
	prevRet := g.curFuncReturn
	g.curFuncReturn = m.ReturnType
	if err := g.emitFuncBody(m.Body, m.ReturnType, isUnitReturn(m.ReturnType)); err != nil {
		return err
	}
	g.curFuncReturn = prevRet
	g.indent--
	g.b.WriteString("}\n")
	return nil
}

// emitTypeParamBrackets writes a Go-side type-parameter
// list. With `withConstraints` it emits `[T any, U any, ...]`
// (used on declarations); without, it emits `[T, U, ...]`
// (used on uses like receiver types where the constraint has
// already been declared). PR-G1 uses `any` as the default
// constraint; user-written constraints land with PR-G3.
func (g *gen) emitTypeParamBrackets(tps []string, withConstraints bool) {
	if len(tps) == 0 {
		return
	}
	g.b.WriteByte('[')
	for i, tp := range tps {
		if i > 0 {
			g.b.WriteString(", ")
		}
		g.b.WriteString(goIdent(tp))
		if withConstraints {
			g.b.WriteString(" any")
		}
	}
	g.b.WriteByte(']')
}

// staticMethodName returns the package-level Go name for a
// static method per lowering-go.md §Generics: `<className>` in
// camelCase + capitalised method name (`Counter.make` →
// `counterMake`).
func staticMethodName(className, methodName string) string {
	return lowerFirst(className) + capFirst(methodName)
}

func (g *gen) emitFuncDecl(fn *ast.FuncDecl) error {
	g.line(fn.Span.StartLine)
	g.b.WriteString("func ")
	g.b.WriteString(goIdent(fn.Name))
	g.emitTypeParamBrackets(fn.TypeParams, true) // declaration: with `any` constraints
	g.b.WriteByte('(')
	for i, p := range fn.Params {
		if i > 0 {
			g.b.WriteString(", ")
		}
		g.b.WriteString(goIdent(p.Name))
		g.b.WriteByte(' ')
		// A variadic `...T` parameter carries its element type in
		// DeclType; lower to Go's `...T` (ffi.md §Variadic).
		if p.Variadic {
			g.b.WriteString("...")
		}
		if err := g.emitTypeExpr(p.DeclType); err != nil {
			return err
		}
	}
	g.b.WriteByte(')')
	if fn.ReturnType != nil {
		g.b.WriteByte(' ')
		if err := g.emitTypeExpr(fn.ReturnType); err != nil {
			return err
		}
	}
	g.b.WriteString(" {\n")
	g.indent++
	prevRet := g.curFuncReturn
	g.curFuncReturn = fn.ReturnType
	if err := g.emitFuncBody(fn.Body, fn.ReturnType, isUnitReturn(fn.ReturnType)); err != nil {
		return err
	}
	g.curFuncReturn = prevRet
	g.indent--
	g.b.WriteString("}\n")
	return nil
}

// emitTypeExpr lowers a TypeExpr to its Go form. PR-F1 handles
// PrimitiveType and NamedType; SliceType / TupleType / FuncType /
// InlineInterface land with later PRs.
func (g *gen) emitTypeExpr(t ast.TypeExpr) error {
	switch v := t.(type) {
	case *ast.PrimitiveType:
		// Tide primitive names map 1:1 onto Go's by spec
		// (lowering-go.md §Primitive type lowering); the only
		// transform is `unit` → Go's zero-byte `struct{}`.
		if v.Name == "unit" {
			g.b.WriteString("struct{}")
			return nil
		}
		g.b.WriteString(v.Name)
		return nil
	case *ast.SliceType:
		g.b.WriteString("[]")
		return g.emitTypeExpr(v.Elem)
	case *ast.TupleType:
		// Tuples lower to anonymous Go structs with positional
		// fields `_0`, `_1`, … — structurally typed, so equal-shape
		// tuples share a Go type without a named declaration
		// (lowering-go.md §Tuple lowering).
		g.b.WriteString("struct { ")
		for i, ct := range v.Components {
			if i > 0 {
				g.b.WriteString("; ")
			}
			g.b.WriteString("_")
			g.b.WriteString(strconv.Itoa(i))
			g.b.WriteByte(' ')
			if err := g.emitTypeExpr(ct); err != nil {
				return err
			}
		}
		g.b.WriteString(" }")
		return nil
	case *ast.FuncType:
		// `func(A, B): R` → Go `func(A, B) R`.
		g.b.WriteString("func(")
		for i, pt := range v.Params {
			if i > 0 {
				g.b.WriteString(", ")
			}
			if err := g.emitTypeExpr(pt); err != nil {
				return err
			}
		}
		g.b.WriteByte(')')
		if v.ReturnType != nil {
			g.b.WriteByte(' ')
			if err := g.emitTypeExpr(v.ReturnType); err != nil {
				return err
			}
		}
		return nil
	case *ast.NamedType:
		// Channel types lower to Go's native channel types
		// (lowering-go.md §Channel lowering): Channel<T> → `chan T`,
		// SendChan<T> → `chan<- T`, RecvChan<T> → `<-chan T`. Not a
		// wrapper struct — channels are a first-class Go primitive.
		if len(v.QName) == 1 && len(v.Args) == 1 {
			var prefix string
			switch v.QName[0] {
			case "Channel":
				prefix = "chan "
			case "SendChan":
				prefix = "chan<- "
			case "RecvChan":
				prefix = "<-chan "
			}
			if prefix != "" {
				g.b.WriteString(prefix)
				return g.emitTypeExpr(v.Args[0])
			}
		}
		// An opaque foreign handle (ffi.md §ExternType) lowers to the
		// Go pointer type `*pkg.Sym` — the `*regexp.Regexp` / `*exec.Cmd`
		// shape Go libraries are used through.
		if len(v.QName) == 1 {
			if etd, isHandle := g.externType[v.QName[0]]; isHandle {
				pkg, sym := goRefPkgSym(etd.Go, etd.Name)
				g.b.WriteByte('*')
				if pkg != "" {
					g.b.WriteString(goPkgRef(pkg))
					g.b.WriteByte('.')
				}
				g.b.WriteString(sym)
				return nil
			}
		}
		// Per G16 / lowering-go.md §Implicit receiver, classes
		// are reference types — a NamedType naming a class in
		// scope lowers to `*ClassName` in Go so that field
		// mutation through methods is visible to all aliases.
		if len(v.QName) == 1 {
			if _, isClass := g.class[v.QName[0]]; isClass {
				g.b.WriteByte('*')
			}
		}
		g.b.WriteString(strings.Join(v.QName, "."))
		if err := g.emitTypeArgs(v.Args); err != nil {
			return err
		}
		return nil
	}
	return fmt.Errorf("codegen: unhandled type expression %T", t)
}

func (g *gen) emitBlockBody(b *ast.Block) error {
	for _, s := range b.Stmts {
		if err := g.emitStmt(s); err != nil {
			return err
		}
	}
	if b.Trailing != nil {
		// Statement-context block: the trailing value is discarded.
		// Value-context blocks are lowered to an IIFE by
		// emitBlockAsExpr, which never calls this.
		return g.emitStmt(&ast.ExprStmt{Span: b.Trailing.NodeSpan(), Expr: b.Trailing})
	}
	return nil
}

// emitFuncBody lowers a function / method / closure body. Unlike a
// statement-position block, the trailing expression of a body whose
// result is a value is an *implicit return* (block-as-expression value
// rule; lowering-go.md §"Implicit tail return"): it is emitted in tail
// position (emitTailReturn) so a trailing match/if distributes the
// `return` into its leaves and the declared return type flows down for
// constructor type-arg stamping. A unit-returning body keeps the
// statement-position discard; a body with no trailing (ends in explicit
// `return`s) emits nothing extra. isUnit is passed explicitly because a
// closure's unit-ness can come from sema inference, not just a nil
// annotation.
func (g *gen) emitFuncBody(b *ast.Block, ret ast.TypeExpr, isUnit bool) error {
	for _, s := range b.Stmts {
		if err := g.emitStmt(s); err != nil {
			return err
		}
	}
	if b.Trailing == nil {
		return nil
	}
	if isUnit {
		return g.emitStmt(&ast.ExprStmt{Span: b.Trailing.NodeSpan(), Expr: b.Trailing})
	}
	prev := g.expectType
	g.expectType = ret
	err := g.emitTailReturn(b.Trailing)
	g.expectType = prev
	return err
}

// isUnitReturn reports whether a declared return type carries no Go
// value: a nil annotation (the implicit unit return) or an explicit
// `unit`.
func isUnitReturn(t ast.TypeExpr) bool {
	if t == nil {
		return true
	}
	p, ok := t.(*ast.PrimitiveType)
	return ok && p.Name == "unit"
}

func (g *gen) emitStmt(s ast.Stmt) error {
	switch v := s.(type) {
	case *ast.ExprStmt:
		// ReturnExpr (DivergingExpr): lower to Go `return` stmt.
		if r, ok := v.Expr.(*ast.ReturnExpr); ok {
			// Inside a `spawn` body the func returns `error`, so a
			// `return Ok(())` / `return Err(e)` (Result<unit, E>) is
			// converted to the group's error channel (lowering-go.md
			// §SpawnIR).
			if g.inSpawnBody {
				return g.emitSpawnReturn(r)
			}
			// `return try e` — emit the try preamble, then
			// `return tmp.V`.
			if try, ok := r.Value.(*ast.TryExpr); ok {
				tmp, err := g.emitTryPreamble(try)
				if err != nil {
					return err
				}
				g.line(r.Span.StartLine)
				g.writeIndent()
				g.b.WriteString("return ")
				g.b.WriteString(tmp)
				g.b.WriteString(".V\n")
				return nil
			}
			if r.Value == nil {
				g.line(v.Span.StartLine)
				g.writeIndent()
				g.b.WriteString("return\n")
				return nil
			}
			// `return f(try g())` / `return a + try b()` — hoist the
			// nested try preambles before the `return` line.
			if err := g.hoistTriesIfSafe(r.Value); err != nil {
				return err
			}
			g.line(v.Span.StartLine)
			g.writeIndent()
			g.b.WriteString("return ")
			// The returned value is expected to be the function's
			// declared return type; thread it so a predeclared
			// Result/Option constructor gets explicit type args.
			prevExpect := g.expectType
			g.expectType = g.curFuncReturn
			err := g.emitExpr(r.Value)
			g.expectType = prevExpect
			if err != nil {
				return err
			}
			g.b.WriteByte('\n')
			return nil
		}
		// Bare `try e` as a discarded expression statement.
		if try, ok := v.Expr.(*ast.TryExpr); ok {
			_, err := g.emitTryPreamble(try)
			return err
		}
		// Diverging loop expressions lower to Go statements.
		if _, ok := v.Expr.(*ast.BreakExpr); ok {
			g.line(v.Span.StartLine)
			g.writeIndent()
			g.b.WriteString("break\n")
			return nil
		}
		if _, ok := v.Expr.(*ast.ContinueExpr); ok {
			g.line(v.Span.StartLine)
			g.writeIndent()
			g.b.WriteString("continue\n")
			return nil
		}
		// MatchExpr: lower to Go `switch` statement.
		if m, ok := v.Expr.(*ast.MatchExpr); ok {
			return g.emitMatchAsStmt(m)
		}
		// Block-as-expression in statement position: run the
		// statements inline, discarding the trailing value.
		if blk, ok := v.Expr.(*ast.Block); ok {
			return g.emitBlockBody(blk)
		}
		// IfExpr in statement position: same shape as an if-statement.
		if ie, ok := v.Expr.(*ast.IfExpr); ok {
			return g.emitIfExprAsStmt(ie)
		}
		// `spawn { … }` registers a goroutine on the enclosing
		// scope's group (lowering-go.md §SpawnIR).
		if sp, ok := v.Expr.(*ast.SpawnExpr); ok {
			return g.emitSpawnStmt(sp)
		}
		// Expression-statement (`stack.push(try …)`) — hoist any
		// nested `try` to preambles before emitting the call.
		if err := g.hoistTriesIfSafe(v.Expr); err != nil {
			return err
		}
		g.line(v.Span.StartLine)
		g.writeIndent()
		if err := g.emitExpr(v.Expr); err != nil {
			return err
		}
		g.b.WriteByte('\n')
		return nil
	case *ast.IfStmt:
		return g.emitIfStmt(v)
	case *ast.WhileStmt:
		return g.emitWhileStmt(v)
	case *ast.SelectStmt:
		return g.emitSelectStmt(v)
	case *ast.DeferStmt:
		// lowering-go.md §Defer: `defer call(args)` → Go `defer
		// call(args)` directly (G27 — adopted from Go).
		g.line(v.Span.StartLine)
		g.writeIndent()
		g.b.WriteString("defer ")
		if err := g.emitExpr(v.Call); err != nil {
			return err
		}
		g.b.WriteByte('\n')
		return nil
	case *ast.ForStmt:
		return g.emitForStmt(v)
	case *ast.LetStmt:
		switch pat := v.Pattern.(type) {
		case *ast.IdentPat:
			return g.emitLetOrVar(v.Span, pat.Name, v.DeclType, v.Value)
		case *ast.TuplePat:
			return g.emitDestructureLet(v.Span, pat, v.Value)
		default:
			return fmt.Errorf("codegen: unsupported `let` pattern %T", v.Pattern)
		}
	case *ast.VarStmt:
		return g.emitLetOrVar(v.Span, v.Name, v.DeclType, v.Value)
	case *ast.AssignStmt:
		// `total = total + try f()` / `m[try k()] = v` — hoist nested
		// try preambles before any of the assignment is emitted. LValue
		// then Value as one frame, so the order check spans both.
		if err := g.hoistTriesIfSafe(v.LValue, v.Value); err != nil {
			return err
		}
		g.line(v.Span.StartLine)
		g.writeIndent()
		// `m[k] = val` where m is a Map<K, V> lowers to
		// `m.set(k, val)` — the wrapper's set() updates both the
		// internal map and the insertion-order slice. Direct
		// `m.m[k] = val` would bypass that and break iteration
		// order for any later `.entries()`/`.keys()` call.
		if idx, ok := v.LValue.(*ast.Index); ok {
			if id, ok := idx.Receiver.(*ast.Ident); ok && g.varKindOf(id) == "Map" {
				if err := g.emitExpr(id); err != nil {
					return err
				}
				g.b.WriteString(".set(")
				if err := g.emitExpr(idx.Idx); err != nil {
					return err
				}
				g.b.WriteString(", ")
				if err := g.emitExpr(v.Value); err != nil {
					return err
				}
				g.b.WriteString(")\n")
				return nil
			}
		}
		if err := g.emitExpr(v.LValue); err != nil {
			return err
		}
		g.b.WriteString(" = ")
		if err := g.emitExpr(v.Value); err != nil {
			return err
		}
		g.b.WriteByte('\n')
		return nil
	}
	return fmt.Errorf("codegen: unhandled stmt %T", s)
}

// emitBraceLit lowers a brace literal. A record literal becomes a Go
// struct literal `TypeName{ field: value, … }` (same-package field
// names map directly). Map / Set / Stack literals lower to the
// predeclared container helpers, sharing the `.new()` / `.from()`
// representation (builtins.md §Map / §Set / §Stack).
func (g *gen) emitBraceLit(b *ast.BraceLit) error {
	if len(b.TypeName.QName) == 1 {
		switch b.TypeName.QName[0] {
		case "Map":
			return g.emitMapBraceLit(b)
		case "Set":
			return g.emitSetBraceLit(b)
		case "Stack":
			return g.emitStackBraceLit(b)
		}
	}
	if b.Kind != ast.BraceRecord {
		return fmt.Errorf("codegen: %s brace literal not yet supported — use the container constructor / `.new()`", b.Kind)
	}
	if len(b.TypeName.QName) != 1 {
		return fmt.Errorf("codegen: qualified record type name not supported")
	}
	name := b.TypeName.QName[0]
	ci, isClass := g.class[name]
	if isClass && ci.generic && len(b.TypeName.Args) == 0 {
		return fmt.Errorf("codegen: brace literal on generic class %s needs explicit type arguments — write %s<T>{…}", name, name)
	}
	// A class is a reference type — `Bar{ x: 6 }` constructs `&Bar{…}`
	// so its methods (declared on `*Bar`) are reachable.
	if isClass {
		g.b.WriteByte('&')
	}
	g.b.WriteString(goIdent(name))
	// Generic record/class literal `Box<int>{…}` lowers to the
	// instantiated Go type `Box[int]{…}` — Go cannot infer struct
	// type parameters from a composite literal.
	if err := g.emitTypeArgs(b.TypeName.Args); err != nil {
		return err
	}
	g.b.WriteByte('{')
	for i, e := range b.Entries {
		re, ok := e.(*ast.RecordEntry)
		if !ok {
			return fmt.Errorf("codegen: non-record entry %T in record literal", e)
		}
		if i > 0 {
			g.b.WriteString(", ")
		}
		g.b.WriteString(exportFieldName(re.Name))
		g.b.WriteString(": ")
		// Flow the field's declared type as the expected type so a
		// constructor value Go can't infer — `q: None` /
		// `r: Ok(v)` — gets its type args stamped (§Constructor
		// type-argument stamping). nil when the field type is unknown.
		prevExpect := g.expectType
		if fts, ok := g.fieldTypes[name]; ok {
			g.expectType = fts[re.Name]
		} else {
			g.expectType = nil
		}
		err := g.emitExpr(re.Value)
		g.expectType = prevExpect
		if err != nil {
			return err
		}
	}
	g.b.WriteByte('}')
	return nil
}

// emitSetBraceLit lowers `Set<T>{}` → `setNew[T]()` and
// `Set<T>{e1,…}` → `setFrom([]T{e1,…})`, reusing the predeclared Set
// helpers (Go infers `setFrom`'s `T` from the slice literal).
func (g *gen) emitSetBraceLit(b *ast.BraceLit) error {
	if len(b.Entries) == 0 {
		g.b.WriteString("setNew")
		if err := g.emitTypeArgs(b.TypeName.Args); err != nil {
			return err
		}
		g.b.WriteString("()")
		return nil
	}
	if len(b.TypeName.Args) != 1 {
		return fmt.Errorf("codegen: Set literal needs an element type argument — write Set<T>{…}")
	}
	g.b.WriteString("setFrom([]")
	if err := g.emitTypeExpr(b.TypeName.Args[0]); err != nil {
		return err
	}
	g.b.WriteByte('{')
	for i, e := range b.Entries {
		se, ok := e.(*ast.SetEntry)
		if !ok {
			return fmt.Errorf("codegen: non-set entry %T in Set literal", e)
		}
		if i > 0 {
			g.b.WriteString(", ")
		}
		if err := g.emitExpr(se.Value); err != nil {
			return err
		}
	}
	g.b.WriteString("})")
	return nil
}

// emitMapBraceLit lowers `Map<K,V>{}` → `mapNew[K,V]()` and a
// non-empty `Map<K,V>{ k: v, … }` to an insertion IIFE
// (`func() *Map[K,V] { m := mapNew[K,V](); m.set(k, v); …; return m }()`)
// — Map has no construct-from-entries helper, and an IIFE keeps the
// literal a single Go expression.
func (g *gen) emitMapBraceLit(b *ast.BraceLit) error {
	if len(b.Entries) == 0 {
		g.b.WriteString("mapNew")
		if err := g.emitTypeArgs(b.TypeName.Args); err != nil {
			return err
		}
		g.b.WriteString("()")
		return nil
	}
	if len(b.TypeName.Args) != 2 {
		return fmt.Errorf("codegen: Map literal needs key and value type arguments — write Map<K,V>{…}")
	}
	g.b.WriteString("func() *Map")
	if err := g.emitTypeArgs(b.TypeName.Args); err != nil {
		return err
	}
	g.b.WriteString(" { m := mapNew")
	if err := g.emitTypeArgs(b.TypeName.Args); err != nil {
		return err
	}
	g.b.WriteString("(); ")
	for _, e := range b.Entries {
		me, ok := e.(*ast.MapEntry)
		if !ok {
			return fmt.Errorf("codegen: non-map entry %T in Map literal", e)
		}
		g.b.WriteString("m.set(")
		if err := g.emitExpr(me.Key); err != nil {
			return err
		}
		g.b.WriteString(", ")
		if err := g.emitExpr(me.Value); err != nil {
			return err
		}
		g.b.WriteString("); ")
	}
	g.b.WriteString("return m }()")
	return nil
}

// emitStackBraceLit lowers `Stack<T>{}` → `stackNew[T]()`. A Stack
// literal is always empty (ast.md §BraceLit); sema rejects entries.
func (g *gen) emitStackBraceLit(b *ast.BraceLit) error {
	if len(b.Entries) != 0 {
		return fmt.Errorf("codegen: Stack literal must be empty — push elements after construction")
	}
	g.b.WriteString("stackNew")
	if err := g.emitTypeArgs(b.TypeName.Args); err != nil {
		return err
	}
	g.b.WriteString("()")
	return nil
}

// emitTupleLit lowers a tuple literal to an anonymous-struct literal.
// The struct type comes from sema's inferred Tuple so the literal
// shares its Go type with any matching annotation / field access
// (structural equivalence).
func (g *gen) emitTupleLit(t *ast.TupleLit) error {
	var structType string
	if g.info != nil {
		if tt, ok := g.info.Type[t].(*sema.Tuple); ok {
			if s, ok := g.goTypeFromSema(tt); ok {
				structType = s
			}
		}
	}
	if structType == "" {
		return fmt.Errorf("codegen: cannot infer Go type for tuple literal — annotate the binding")
	}
	g.b.WriteString(structType)
	g.b.WriteByte('{')
	for i, ce := range t.Components {
		if i > 0 {
			g.b.WriteString(", ")
		}
		g.b.WriteString("_")
		g.b.WriteString(strconv.Itoa(i))
		g.b.WriteString(": ")
		if err := g.emitExpr(ce); err != nil {
			return err
		}
	}
	g.b.WriteByte('}')
	return nil
}

// emitReflectCall lowers a `reflect.X(args)` call to the
// corresponding inline tidert helper emitted by
// `writePredeclaredReflect`. Current surface: box / unbox /
// typeOf / typeName / kind / fields / fieldValue / show
// (PR-R1 .. PR-R3). Variants / methods / typeArgs / elementType
// land with later Block-R PRs.
func (g *gen) emitReflectCall(name string, typeArgs []ast.TypeExpr, args []ast.Expr) error {
	switch name {
	case "box":
		g.b.WriteString("tideBox")
		if len(typeArgs) > 0 {
			g.b.WriteByte('[')
			if err := g.emitTypeExpr(typeArgs[0]); err != nil {
				return err
			}
			g.b.WriteByte(']')
		}
		g.b.WriteByte('(')
		if len(args) != 1 {
			return fmt.Errorf("codegen: reflect.box expects exactly one argument, got %d", len(args))
		}
		if err := g.emitExpr(args[0]); err != nil {
			return err
		}
		g.b.WriteByte(')')
		return nil
	case "unbox":
		if len(typeArgs) != 1 {
			return fmt.Errorf("codegen: reflect.unbox requires exactly one explicit type argument `reflect.unbox<T>(d)`")
		}
		g.b.WriteString("tideUnbox[")
		if err := g.emitTypeExpr(typeArgs[0]); err != nil {
			return err
		}
		g.b.WriteString("](")
		if len(args) != 1 {
			return fmt.Errorf("codegen: reflect.unbox expects exactly one argument, got %d", len(args))
		}
		if err := g.emitExpr(args[0]); err != nil {
			return err
		}
		g.b.WriteByte(')')
		return nil
	case "typeOf", "typeName", "kind", "fields", "fieldValue", "show":
		g.b.WriteString("tide")
		g.b.WriteString(strings.ToUpper(name[:1]))
		g.b.WriteString(name[1:])
		g.b.WriteByte('(')
		for i, a := range args {
			if i > 0 {
				g.b.WriteString(", ")
			}
			if err := g.emitExpr(a); err != nil {
				return err
			}
		}
		g.b.WriteByte(')')
		return nil
	}
	return fmt.Errorf("codegen: reflect.%s is not yet supported (methods / variants / variantOf / typeArgs / elementType land later)", name)
}

// semaSliceElem returns the Go element type for an inferred slice
// literal from sema's side-table — used when literal-only inference
// (inferSliceElemType) can't see the element type (e.g. `[v]` with an
// Ident / call element). Returns ("", false) when no usable sema type
// is available, so the caller falls back to literal inference.
func (g *gen) semaSliceElem(lit *ast.SliceLit) (string, bool) {
	if g.info == nil {
		return "", false
	}
	st, ok := g.info.Type[lit].(*sema.Slice)
	if !ok {
		return "", false
	}
	return g.goTypeFromSema(st.Elem)
}

// inferSliceElemType returns the Go-side element type for an
// inferred slice literal. PR-F3 supports Int / String / Bool
// literal elements; anything else returns an error.
func inferSliceElemType(items []ast.Expr) (string, error) {
	if len(items) == 0 {
		return "", fmt.Errorf("codegen: empty inferred-type slice literal — annotate with `[]T{}`")
	}
	switch items[0].(type) {
	case *ast.IntLitExpr:
		return "int", nil
	case *ast.FloatLitExpr:
		return "float64", nil
	case *ast.StringLitExpr:
		return "string", nil
	case *ast.BoolLitExpr:
		return "bool", nil
	}
	return "", fmt.Errorf("codegen: cannot infer element type from %T — annotate the slice literal", items[0])
}

// payloadFieldName builds the Go struct field name for a payload
// field of a variant, per the lowering-go.md tagged-struct shape:
// `<VariantName><FieldName>` (both capitalised). E.g. variant
// `Just` with field `value` → `JustValue`.
func payloadFieldName(variantName, fieldName string) string {
	return capFirst(variantName) + capFirst(fieldName)
}

// isSelfRefField reports whether a payload field directly names the
// enclosing sum type `sumName` (`Tree` or `Tree<…>`). Such a field
// would make the lowered Go struct infinitely sized, so it is
// pointer-ized — `*Tree`, with `&` at construction and `*` at the
// match-binding deref (lowering-go.md §Recursive sum types).
// Indirection through a slice / map / channel is already a pointer in
// Go and needs no rewrite; only the direct-named case is detected
// (by-value recursion nested inside another type — `Option<Tree>` —
// is a v1 limitation). A nil DeclType (predeclared Option/Result
// payload registration) fails the assertion and returns false.
func isSelfRefField(f *ast.FieldDecl, sumName string) bool {
	nt, ok := f.DeclType.(*ast.NamedType)
	return ok && len(nt.QName) == 1 && nt.QName[0] == sumName
}

func capFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

func lastSeg(q []string) string {
	if len(q) == 0 {
		return ""
	}
	return q[len(q)-1]
}

// emitDestructureLet lowers `let (a, b) = e` (lowering-go.md
// §Tuple destructuring). The value is bound to a fresh temp once (so a
// side-effecting RHS runs exactly once), then each component is bound
// positionally via bindSubPattern (`a := tmp._0`, …), recursing for
// nested tuples; a `_` component binds nothing. When every component is
// `_` the value is discarded (`_ = e`) so Go sees no unused temp.
func (g *gen) emitDestructureLet(span ast.Span, pat *ast.TuplePat, value ast.Expr) error {
	g.line(span.StartLine)
	g.writeIndent()
	if patternBindsNothing(pat) {
		g.b.WriteString("_ = ")
		if err := g.emitExpr(value); err != nil {
			return err
		}
		g.b.WriteByte('\n')
		return nil
	}
	tmp := g.nextDestructureTemp()
	g.b.WriteString(tmp)
	g.b.WriteString(" := ")
	if err := g.emitExpr(value); err != nil {
		return err
	}
	g.b.WriteByte('\n')
	return g.bindSubPattern(pat, tmp)
}

// patternBindsNothing reports whether an irrefutable let pattern
// introduces no binding at all (every leaf is `_`), so the temp would
// be unused — the value is discarded instead.
func patternBindsNothing(p ast.Pattern) bool {
	switch v := p.(type) {
	case *ast.WildcardPat:
		return true
	case *ast.TuplePat:
		for _, sub := range v.Sub {
			if !patternBindsNothing(sub) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

// nextDestructureTemp returns a fresh Go identifier for a
// tuple-destructuring temp, sharing the runtime-prefix convention with
// the other codegen-internal temps.
func (g *gen) nextDestructureTemp() string {
	g.destructureTempCounter++
	return fmt.Sprintf("__tide_destructure_%d", g.destructureTempCounter)
}

// emitLetOrVar lowers both `let` and `var` to Go's `var name [T] = value`.
// Immutability of `let` is a sema concern (not yet implemented); the
// generated Go is identical for both keywords.
func (g *gen) emitLetOrVar(span ast.Span, name string, declType ast.TypeExpr, value ast.Expr) error {
	// `let x = try e` / `var x = try e` — emit the try
	// preamble, then bind the unwrapped value.
	if try, ok := value.(*ast.TryExpr); ok {
		tmp, err := g.emitTryPreamble(try)
		if err != nil {
			return err
		}
		g.line(span.StartLine)
		g.writeIndent()
		g.b.WriteString("var ")
		g.b.WriteString(goIdent(name))
		if declType != nil {
			g.b.WriteByte(' ')
			if err := g.emitTypeExpr(declType); err != nil {
				return err
			}
		}
		g.b.WriteString(" = ")
		g.b.WriteString(tmp)
		g.b.WriteString(".V\n")
		return nil
	}
	// `let x = f(try g())` / `let x = a + try b()` — hoist nested try
	// preambles before the binding line.
	if err := g.hoistTriesIfSafe(value); err != nil {
		return err
	}
	g.line(span.StartLine)
	g.writeIndent()
	g.b.WriteString("var ")
	g.b.WriteString(goIdent(name))
	if declType != nil {
		g.b.WriteByte(' ')
		if err := g.emitTypeExpr(declType); err != nil {
			return err
		}
	}
	g.b.WriteString(" = ")
	// A type annotation gives the value an expected type — thread it
	// so a predeclared Result/Option constructor gets explicit type
	// args (Go does not infer a generic call's type params from the
	// assignment LHS). nil annotation leaves inference unchanged.
	prevExpect := g.expectType
	g.expectType = declType
	err := g.emitExpr(value)
	g.expectType = prevExpect
	if err != nil {
		return err
	}
	g.b.WriteByte('\n')
	return nil
}

// ---- expressions ----

func (g *gen) emitExpr(e ast.Expr) error {
	switch v := e.(type) {
	case *ast.IntLitExpr:
		g.b.WriteString(strconv.FormatInt(v.Value, 10))
		return nil
	case *ast.FloatLitExpr:
		// Re-emit source text; Go accepts the same `3.14` / `1e3`
		// float syntax for its float64.
		g.b.WriteString(v.RawText)
		return nil
	case *ast.StringLitExpr:
		g.b.WriteString(strconv.Quote(v.Value))
		return nil
	case *ast.RuneLitExpr:
		// Re-emit the source text; Go accepts the same `'a'`
		// rune-literal syntax for its rune (int32) type.
		g.b.WriteString(v.RawText)
		return nil
	case *ast.BoolLitExpr:
		if v.Value {
			g.b.WriteString("true")
		} else {
			g.b.WriteString("false")
		}
		return nil
	case *ast.UnitLit:
		// The unit value `()` is Go's zero-byte composite literal
		// (lowering-go.md §Primitive type lowering).
		g.b.WriteString("struct{}{}")
		return nil
	case *ast.ScopeExpr:
		// Value-position structured-concurrency scope → IIFE
		// returning Result[T, error] (lowering-go.md §ScopeIR).
		return g.emitScopeExpr(v)
	case *ast.ThisExpr:
		// lowering-go.md §Implicit receiver — the receiver is
		// named `t` consistently in generated method bodies.
		g.b.WriteString("t")
		return nil
	case *ast.Ident:
		// Variant identifiers (declared in any sum type in the
		// same file) get qualified to their Go-side variable:
		// `Red` → `ColorRed`.
		if info, ok := g.variant[v.Name]; ok {
			// A bare nullary constructor of a *generic* sum is a
			// parameterless generic call Go can't infer — stamp explicit
			// type args (`OptionNone[T]()`, `TreeLeaf[int]()`;
			// lowering-go.md §Container types / §Generics). The args come
			// from the enclosing ctor call's inferred instantiation
			// (g.sumCtorArgs, set while emitting a `Node(…)`'s arguments),
			// else the expected type. User sums with no type params and
			// all other variants emit bare.
			if len(info.sumTypeParams) > 0 {
				if len(g.sumCtorArgs) == len(info.sumTypeParams) {
					g.b.WriteString(goIdent(info.owner))
					g.b.WriteString(goIdent(v.Name))
					g.b.WriteByte('[')
					g.b.WriteString(strings.Join(g.sumCtorArgs, ", "))
					g.b.WriteString("]()")
					return nil
				}
				if targs, ok := g.userSumCtorArgsFromExpect(info, g.expectType); ok {
					g.b.WriteString(goIdent(info.owner))
					g.b.WriteString(goIdent(v.Name))
					if err := g.emitTypeArgs(targs); err != nil {
						return err
					}
					g.b.WriteString("()")
					return nil
				}
			}
			if targs, _, ok := g.predeclaredCtorTypeArgs(v.Name, g.expectType); ok {
				g.b.WriteString(goIdent(info.owner))
				g.b.WriteString(goIdent(v.Name))
				if err := g.emitTypeArgs(targs); err != nil {
					return err
				}
				g.b.WriteString("()")
				return nil
			}
			g.b.WriteString(goIdent(info.owner))
			g.b.WriteString(goIdent(v.Name))
			return nil
		}
		// A bare ident that sema resolved to a class field (not a
		// shadowing local/param) is an implicit-receiver reference
		// (name-resolution §Implicit receiver) — emit `t.<field>`,
		// since the Go field lives on the method receiver `t`.
		if g.info != nil {
			if sym := g.info.Symbol[v]; sym != nil && sym.Kind == sema.SymField {
				g.b.WriteString("t.")
				g.b.WriteString(exportFieldName(v.Name))
				return nil
			}
		}
		g.b.WriteString(goIdent(v.Name))
		return nil
	case *ast.SliceLit:
		// Annotated form `[]T{...}` → `[]T{...}` directly.
		// Inferred form `[e_1, ..., e_n]` → `[]TInferred{...}`.
		// PR-F3 infers from the first element when it's an Int /
		// String / Bool literal; otherwise rejects (no sema yet).
		if v.ElemType != nil {
			g.b.WriteString("[]")
			if err := g.emitTypeExpr(v.ElemType); err != nil {
				return err
			}
		} else if elem, ok := g.semaSliceElem(v); ok {
			// Sema typed the literal (e.g. `[v]` from an Ident /
			// call element): use its element type directly.
			g.b.WriteString("[]")
			g.b.WriteString(elem)
		} else {
			// No sema info — fall back to first-literal inference.
			elem, err := inferSliceElemType(v.Items)
			if err != nil {
				return err
			}
			g.b.WriteString("[]")
			g.b.WriteString(elem)
		}
		g.b.WriteByte('{')
		for i, it := range v.Items {
			if i > 0 {
				g.b.WriteString(", ")
			}
			if err := g.emitExpr(it); err != nil {
				return err
			}
		}
		g.b.WriteByte('}')
		return nil
	case *ast.Index:
		// `m[k]` where m is a Map<K, V> lowers to the wrapper's
		// internal `m.m[k]` direct map access — returns V's
		// Go zero value for a missing key (mirrors Go's map
		// semantics). `m.get(k)` is the explicit-Option form
		// when the user wants the missing case to surface.
		if id, ok := v.Receiver.(*ast.Ident); ok && g.varKindOf(id) == "Map" {
			if err := g.emitExpr(id); err != nil {
				return err
			}
			g.b.WriteString(".m[")
			if err := g.emitExpr(v.Idx); err != nil {
				return err
			}
			g.b.WriteByte(']')
			return nil
		}
		if err := g.emitExpr(v.Receiver); err != nil {
			return err
		}
		g.b.WriteByte('[')
		if err := g.emitExpr(v.Idx); err != nil {
			return err
		}
		g.b.WriteByte(']')
		return nil
	case *ast.Slice:
		if err := g.emitExpr(v.Receiver); err != nil {
			return err
		}
		g.b.WriteByte('[')
		if v.Low != nil {
			if err := g.emitExpr(v.Low); err != nil {
				return err
			}
		}
		g.b.WriteByte(':')
		if v.High != nil {
			if err := g.emitExpr(v.High); err != nil {
				return err
			}
		}
		g.b.WriteByte(']')
		return nil
	case *ast.MatchExpr:
		return g.emitMatchAsExpr(v)
	case *ast.Block:
		return g.emitBlockAsExpr(v)
	case *ast.IfExpr:
		return g.emitIfExprAsValue(v)
	case *ast.ParenExpr:
		// Reproduce the author's grouping so Go preserves the same
		// operator precedence (`a * (b + c)` must not re-associate).
		g.b.WriteByte('(')
		if err := g.emitExpr(v.Inner); err != nil {
			return err
		}
		g.b.WriteByte(')')
		return nil
	case *ast.BraceLit:
		return g.emitBraceLit(v)
	case *ast.ClosureLit:
		return g.emitClosure(v)
	case *ast.TupleLit:
		return g.emitTupleLit(v)
	case *ast.TupleField:
		if err := g.emitExpr(v.Receiver); err != nil {
			return err
		}
		g.b.WriteString("._")
		g.b.WriteString(strconv.Itoa(v.Position))
		return nil
	case *ast.BreakExpr, *ast.ContinueExpr:
		// Diverging loop expressions lower to statements, not Go
		// expressions — they're handled in emitStmt. Reaching here
		// means one was used in value position (e.g. a value-arm
		// `match x { A => break }`), which v1 codegen does not lower.
		return fmt.Errorf("codegen: `break`/`continue` is not usable in value position")
	case *ast.Field:
		return g.emitField(v)
	case *ast.Call:
		return g.emitCall(v)
	case *ast.SpreadArg:
		// `...xs` lowers to Go's trailing `xs...` spread (ffi.md §Variadic).
		if err := g.emitExpr(v.Inner); err != nil {
			return err
		}
		g.b.WriteString("...")
		return nil
	case *ast.Binary:
		if err := g.emitExpr(v.Left); err != nil {
			return err
		}
		g.b.WriteByte(' ')
		g.b.WriteString(v.Op)
		g.b.WriteByte(' ')
		return g.emitExpr(v.Right)
	case *ast.Unary:
		g.b.WriteString(v.Op)
		return g.emitExpr(v.Operand)
	case *ast.ReturnExpr:
		// ReturnExpr is a DivergingExpr; in Go it must appear as
		// a statement (`return [value]`), not in an expression
		// context. The ExprStmt wrapper emitter writes the
		// statement form via emitReturnAsStatement directly, so
		// reaching this branch means a misuse (return in a
		// non-statement context) — emit clearly.
		return fmt.Errorf("codegen: return-expression used outside statement position")
	case *ast.TryExpr:
		// Expression-position `try` (call arg, operand, …) is lowered
		// by hoistExprTries, which pre-emits the early-return preamble
		// as a statement and records the unwrap temp here. The node's
		// value is the temp's payload `<tmp>.V`. A `try` that wasn't
		// hoisted sits in an unsupported frame (value-position
		// match/if/closure arm — a different return frame): error.
		if tmp, ok := g.tryHoist[v]; ok {
			g.b.WriteString(tmp)
			g.b.WriteString(".V")
			return nil
		}
		return g.tryExprErr()
	}
	return fmt.Errorf("codegen: unhandled expression %T", e)
}

func (g *gen) emitField(f *ast.Field) error {
	if err := g.emitExpr(f.Receiver); err != nil {
		return err
	}
	g.b.WriteByte('.')
	// Foreign-handle field access (ffi.md §ExternImpl) takes the Go
	// field name from its `@go` attribute, not the exported-Tide form.
	if fld, ok := g.externFieldOf(f); ok {
		g.b.WriteString(goRefMember(fld.Go, fld.Name))
		return nil
	}
	g.b.WriteString(g.goFieldName(f.Receiver, f.Name))
	return nil
}

// goFieldName maps a Tide *field-value* access `recv.name` to its Go
// spelling. A genuine user record/class field is EXPORTED
// (exportFieldName) so encoding/json can reach it; a stdlib-namespace
// value access (`os.args` → `os.Args`) keeps its binding rename, and
// `.error()` on the predeclared `error` builtin maps to Go's
// `error.Error()` (the PascalCase↔lowerCamel boundary; D14 footnote).
// Method-call selectors do NOT come through here — they use goMethodName
// (call.go), which stays lowercase, so methods remain unexported.
//
// The package-namespace check gates on the receiver's sema *symbol*
// (SymBuiltinModule), not its spelling — a local value that shadows a
// package name (`let sort = Sorter{…}`) is a user value whose fields
// must still export (the recurring name-match footgun, AI.md §3.10/§3.13).
func (g *gen) goFieldName(receiver ast.Expr, name string) string {
	if name == "error" && g.isErrorBuiltinReceiver(receiver) {
		return "Error"
	}
	if id, ok := receiver.(*ast.Ident); ok && g.isBuiltinModule(id) {
		return mapFieldName(receiver, name)
	}
	return exportFieldName(name)
}

// isDataFieldSelector reports whether `recv.name` names a *data field*
// (as opposed to a method) of recv's record/class type. A func-typed
// data field can be *called* — `handler.fn(x)` — and the callee is then
// an `*ast.Field` whose name must take the exported field spelling
// (goFieldName), not the lowercase method spelling, or it would not
// match the exported Go struct field. Records have only data fields;
// classes split fields vs methods; interfaces/containers/stdlib have
// no data fields reachable this way (→ false, method spelling).
func (g *gen) isDataFieldSelector(receiver ast.Expr, name string) bool {
	if g.info == nil {
		return false
	}
	named, ok := g.info.Type[receiver].(*sema.Named)
	if !ok {
		return false
	}
	switch d := named.Decl.(type) {
	case *ast.ClassDecl:
		for _, fld := range d.Fields {
			if fld.Name == name {
				return true
			}
		}
	case *ast.TypeDecl:
		if rb, ok := d.Body.(*ast.RecordTypeBody); ok {
			for _, fld := range rb.Fields {
				if fld.Name == name {
					return true
				}
			}
		}
	}
	return false
}

// goMethodName maps a Tide method-call selector `recv.name(...)` to its
// Go spelling — the pre-export behaviour (stdlib renames + the
// `error`→`Error` boundary, otherwise the verbatim lowercase name).
// Methods stay unexported (package main reaches them); only data fields
// are exported (goFieldName), so the two paths must not be conflated.
func (g *gen) goMethodName(receiver ast.Expr, name string) string {
	if name == "error" && g.isErrorBuiltinReceiver(receiver) {
		return "Error"
	}
	return mapFieldName(receiver, name)
}

// isErrorBuiltinReceiver reports whether sema typed receiver as the
// predeclared `error` type (the Go-error binding boundary).
func (g *gen) isErrorBuiltinReceiver(receiver ast.Expr) bool {
	if g.info == nil {
		return false
	}
	b, ok := g.info.Type[receiver].(*sema.Builtin)
	return ok && b.N == "error"
}

// goIdent maps a Tide identifier to its Go form. PR-C handles
// the common cases (no transform); future PRs add Go-reserved-
// word escaping ("type" → "tide_type") and the `$tide_NN` →
// `_tide_NN` rewrite for codegen-synthesised names.
func goIdent(name string) string {
	if isGoReserved(name) {
		return "tide_" + name
	}
	return name
}

// exportFieldName spells a Tide record/class field as an EXPORTED Go
// field name. encoding/json reflects from outside package main, so an
// unexported Go field is invisible to it; exporting is what makes JSON
// round-trip work (lowering-go.md §Record lowering). The Tide name is
// preserved verbatim in the field's `json:"…"` tag (field-name ==
// JSON-key, binding-surface.md §encoding/json), so the capitalised Go
// spelling is invisible at the Tide-source and wire levels. Exported
// names always start uppercase, so they can never be Go-reserved — no
// goIdent escaping needed.
//
// Identifiers are ASCII `[A-Za-z_][A-Za-z0-9_]*` (the lexer rejects the
// rest), so a single-byte uppercase suffices. A leading underscore can't
// be exported by capitalising, so it gets an `X` prefix. (Collision risk
// — two fields differing only in first-letter case, or `_x` vs `X_x` —
// is a documented limitation; Go rejects the duplicate field loudly, so
// it is never a silent miscompile. See lowering-go.md §Record / struct
// field lowering.)
func exportFieldName(name string) string {
	if name == "" {
		return name
	}
	c := name[0]
	if c >= 'a' && c <= 'z' {
		return string(c-'a'+'A') + name[1:]
	}
	if c >= 'A' && c <= 'Z' {
		return name
	}
	return "X" + name
}

// writeJSONTag emits the ` `+"`json:\"<tideName>\"`"+` ` struct tag that
// pins the JSON key to the Tide field name regardless of the exported Go
// spelling (binding-surface.md §encoding/json: field-name == JSON-key).
func (g *gen) writeJSONTag(tideName string) {
	g.b.WriteString(" `json:\"")
	g.b.WriteString(tideName)
	g.b.WriteString("\"`")
}

var goReserved = map[string]bool{
	"break": true, "case": true, "chan": true, "const": true,
	"continue": true, "default": true, "defer": true, "else": true,
	"fallthrough": true, "for": true, "func": true, "go": true,
	"goto": true, "if": true, "import": true, "interface": true,
	"map": true, "package": true, "range": true, "return": true,
	"select": true, "struct": true, "switch": true, "type": true,
	"var": true,
}

func isGoReserved(name string) bool { return goReserved[name] }

// ---- helpers ----

func (g *gen) writeIndent() {
	for i := 0; i < g.indent; i++ {
		g.b.WriteByte('\t')
	}
}

// line emits a //line directive at the start of a statement
// boundary, mapping subsequent Go lines back to the Tide source
// line. Suppressed when no file path was supplied.
func (g *gen) line(srcLine int) {
	if g.file == "" || srcLine == g.emittedLine {
		return
	}
	g.writeIndent()
	g.b.WriteString("//line ")
	g.b.WriteString(g.file)
	g.b.WriteByte(':')
	g.b.WriteString(strconv.Itoa(srcLine))
	g.b.WriteString(":1\n")
	g.emittedLine = srcLine
}
