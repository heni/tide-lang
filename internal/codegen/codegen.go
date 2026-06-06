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
	g := &gen{
		file:       file,
		info:       info,
		variant:    map[string]variantInfo{},
		class:      map[string]classInfo{},
		usedGoPkgs: map[string]bool{},
	}
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
					g.variant[v.Name] = variantInfo{owner: td.Name, tag: i, fields: v.Fields}
				}
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
	// usedGoPkgs — Go stdlib packages actually referenced in the
	// emitted output (a `pkg.Sym`). Populated by the pre-walk; the
	// import block is this set ∩ the .td imports, so a binding that
	// lowers to a Go conversion (strings.fromBytes → string(...))
	// does not drag in an unused import.
	usedGoPkgs map[string]bool
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
	// tryTempCounter generates unique temp names for `try`
	// emission. Same hygiene as matchTempCounter.
	tryTempCounter int
}

type variantInfo struct {
	owner  string           // owning sum-type name (e.g. "Color")
	tag    int              // declaration order, used for the Tag field
	fields []*ast.FieldDecl // payload fields, nil/empty for nullary variants
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
		add(im.Path)
	}
	if g.usesReflect {
		// reflect.TypeOf for the descriptor registry lookup, plus
		// strconv for the show-helper's primitive formatting.
		add("reflect")
		add("strconv")
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

// writePredeclaredReflect emits Tide's reflection layer per
// `lang-spec/builtins.md` §reflect and `docs/design-decisions.md`
// D18: Dynamic wrapper, TypeDescriptor, Kind sum, descriptor
// registry, and the minimal reflect.* surface (box / unbox /
// typeOf / typeName / kind). Only emitted when usesReflect.
// Per D18, the spec location is `tidert/reflect`; PR-R1 emits
// inline in main as a v1 transitional state (same precedent as
// Option/Result/containers).
func (g *gen) writePredeclaredReflect() {
	if !g.usesReflect {
		return
	}
	g.b.WriteString(`type Dynamic struct {
	Payload any
	Desc    *TypeDescriptor
}

type TypeDescriptor struct {
	Name   string
	Kind   Kind
	fields []FieldInfo
}

type FieldInfo struct {
	name string
	desc *TypeDescriptor
}

type Kind struct {
	Tag uint8
}

var (
	KindPrimitive = Kind{Tag: 0}
	KindClass     = Kind{Tag: 1}
	KindSum       = Kind{Tag: 2}
	KindSlice     = Kind{Tag: 3}
	KindFunction  = Kind{Tag: 4}
	KindUnit      = Kind{Tag: 5}
)

var tideDescRegistry = map[string]*TypeDescriptor{}

// Primitive descriptors — registered eagerly so reflect.box on
// any primitive value finds a descriptor. Notes:
//   - byte is Go's alias for uint8 (reflect.TypeOf returns "uint8"
//     for both byte and uint8 values), so we register a single
//     descriptor for that runtime type with Name "byte".
//   - rune is Go's alias for int32, so int32 / rune collapse to
//     one descriptor with Name "int32".
//   PR-Sema-2 tightens this: when sema knows the user wrote
//   "let r: rune = ...", reflect.typeName can return "rune" via
//   compile-time-resolved descriptor.
var (
	tideDesc_int     = &TypeDescriptor{Name: "int", Kind: KindPrimitive}
	tideDesc_int64   = &TypeDescriptor{Name: "int64", Kind: KindPrimitive}
	tideDesc_int32   = &TypeDescriptor{Name: "int32", Kind: KindPrimitive}
	tideDesc_string  = &TypeDescriptor{Name: "string", Kind: KindPrimitive}
	tideDesc_bool    = &TypeDescriptor{Name: "bool", Kind: KindPrimitive}
	tideDesc_float64 = &TypeDescriptor{Name: "float64", Kind: KindPrimitive}
	tideDesc_byte    = &TypeDescriptor{Name: "byte", Kind: KindPrimitive}
)

func init() {
	tideDescRegistry["int"] = tideDesc_int
	tideDescRegistry["int64"] = tideDesc_int64
	tideDescRegistry["int32"] = tideDesc_int32
	tideDescRegistry["string"] = tideDesc_string
	tideDescRegistry["bool"] = tideDesc_bool
	tideDescRegistry["float64"] = tideDesc_float64
	tideDescRegistry["uint8"] = tideDesc_byte
}

func tideBox[T any](v T) Dynamic {
	return Dynamic{Payload: v, Desc: tideDescForKey(reflect.TypeOf(v).String())}
}

// tideDescForKey looks up (and caches) a descriptor for the
// Go-runtime type-name key. The cache preserves CT1 (descriptor
// uniqueness): two reflect.box calls on the same Go-runtime
// type return Dynamic values whose Desc pointers compare equal.
// Concurrent first-time-seen unknown types may briefly race on
// the registry write — a later Block-R PR adds synchronisation.
func tideDescForKey(key string) *TypeDescriptor {
	if d, ok := tideDescRegistry[key]; ok {
		return d
	}
	d := &TypeDescriptor{Name: key, Kind: KindPrimitive}
	tideDescRegistry[key] = d
	return d
}

func tideTypeOf(d Dynamic) *TypeDescriptor { return d.Desc }
func tideTypeName(t *TypeDescriptor) string { return t.Name }
func tideKind(t *TypeDescriptor) Kind { return t.Kind }

func tideUnbox[T any](d Dynamic) Result[T, error] {
	v, ok := d.Payload.(T)
	if !ok {
		var zero T
		return Result[T, error]{Tag: 1, E: tideUnboxError(d.Desc.Name), V: zero}
	}
	return Result[T, error]{Tag: 0, V: v}
}

type tideUnboxErr struct{ typeName string }

func (e tideUnboxErr) Error() string {
	return "reflect.unbox: payload is not the requested type (have " + e.typeName + ")"
}

func tideUnboxError(typeName string) error { return tideUnboxErr{typeName: typeName} }

// Per-class field accessor functions are registered into this
// map at init time (one per non-generic class declared in the
// program). The accessor reads a named field off the Tide-side
// pointer-to-struct and returns it boxed as Dynamic.
type tideFieldAccessor = func(v any, name string) (Dynamic, bool)

var tideFieldAccessors = map[string]tideFieldAccessor{}

func tideFields(t *TypeDescriptor) []FieldInfo { return t.fields }

func tideFieldValue(d Dynamic, name string) Result[Dynamic, error] {
	if d.Desc == nil {
		var zero Dynamic
		return Result[Dynamic, error]{Tag: 1, E: tideFieldErr("Dynamic has no descriptor"), V: zero}
	}
	fn, ok := tideFieldAccessors[d.Desc.Name]
	if !ok {
		var zero Dynamic
		return Result[Dynamic, error]{Tag: 1, E: tideFieldErr("type " + d.Desc.Name + " has no field accessor"), V: zero}
	}
	v, ok := fn(d.Payload, name)
	if !ok {
		var zero Dynamic
		return Result[Dynamic, error]{Tag: 1, E: tideFieldErr("type " + d.Desc.Name + " has no field " + name), V: zero}
	}
	return Result[Dynamic, error]{Tag: 0, V: v}
}

type tideFieldErrT struct{ msg string }

func (e tideFieldErrT) Error() string { return e.msg }

func tideFieldErr(msg string) error { return tideFieldErrT{msg: msg} }

// tideBoxAny boxes an arbitrary value (with type known only at
// runtime via Go's reflect). Used by per-class field accessors
// when reading a field's value. Routes through tideDescForKey so
// descriptor identity follows CT1.
func tideBoxAny(v any) Dynamic {
	if v == nil {
		return Dynamic{Payload: nil, Desc: tideDescForKey("<nil>")}
	}
	return Dynamic{Payload: v, Desc: tideDescForKey(reflect.TypeOf(v).String())}
}

// tideShow renders a Dynamic value as a human-readable string.
// It is the runtime building block for the REPL auto-printer
// and ` + "`" + `:inspect` + "`" + ` (RFC-0003). Kinds beyond Primitive /
// Class fall back to a "<TypeName>" placeholder for now; PR-R4
// adds Sum / Slice / Map rendering once the matching descriptor
// metadata lands. Panic-free per D18 CT2 — class graphs with
// cycles render the back-edge as "<cycle>" instead of blowing
// the stack.
func tideShow(d Dynamic) string {
	return tideShowWalk(d, map[any]bool{})
}

func tideShowWalk(d Dynamic, seen map[any]bool) string {
	if d.Desc == nil {
		return "<nil>"
	}
	switch d.Desc.Kind {
	case KindClass:
		// Classes are pointer-to-struct on the Go side; use the
		// payload as a cycle key. Non-class kinds skip the check
		// because their payloads aren't reliably comparable.
		if d.Payload != nil {
			if seen[d.Payload] {
				return "<cycle " + d.Desc.Name + ">"
			}
			seen[d.Payload] = true
		}
		out := d.Desc.Name + "{"
		for i, fi := range d.Desc.fields {
			if i > 0 {
				out += ", "
			}
			fv := tideFieldValue(d, fi.name)
			if fv.Tag != 0 {
				out += fi.name + ": <unreadable>"
				continue
			}
			out += fi.name + ": " + tideShowWalk(fv.V, seen)
		}
		return out + "}"
	case KindPrimitive:
		return tideShowPrimitive(d)
	default:
		return "<" + d.Desc.Name + ">"
	}
}

func tideShowPrimitive(d Dynamic) string {
	switch v := d.Payload.(type) {
	case nil:
		return "<nil>"
	case string:
		return strconv.Quote(v)
	case bool:
		if v {
			return "true"
		}
		return "false"
	case int:
		return strconv.Itoa(v)
	case int8:
		return strconv.FormatInt(int64(v), 10)
	case int16:
		return strconv.FormatInt(int64(v), 10)
	case int32:
		return strconv.FormatInt(int64(v), 10)
	case int64:
		return strconv.FormatInt(v, 10)
	case uint:
		return strconv.FormatUint(uint64(v), 10)
	case uint8:
		return strconv.FormatUint(uint64(v), 10)
	case uint16:
		return strconv.FormatUint(uint64(v), 10)
	case uint32:
		return strconv.FormatUint(uint64(v), 10)
	case uint64:
		return strconv.FormatUint(v, 10)
	case float32:
		return strconv.FormatFloat(float64(v), 'g', -1, 32)
	case float64:
		return strconv.FormatFloat(v, 'g', -1, 64)
	default:
		return "<" + d.Desc.Name + ">"
	}
}
`)
	// Per-user-type descriptors collected during emit, with init
	// block registering them into the runtime map.
	if len(g.descriptors) == 0 {
		return
	}
	for _, d := range g.descriptors {
		g.b.WriteString("var tideDesc_")
		g.b.WriteString(d.tideName)
		g.b.WriteString(" = &TypeDescriptor{Name: ")
		g.b.WriteString(strconv.Quote(d.tideName))
		g.b.WriteString(", Kind: ")
		g.b.WriteString(d.kind)
		g.b.WriteString("}\n")
	}
	// Per-class field accessor functions — emitted after the
	// classes themselves are emitted into the package, so the
	// accessor body can refer to the class struct's field by
	// its lowercase Go-side name. The accessor takes any so
	// the dispatcher in tideFieldValue can call it through the
	// map without per-class typed indirection.
	for _, d := range g.descriptors {
		if d.kind != "KindClass" {
			continue
		}
		g.b.WriteString("func tideFieldOf_")
		g.b.WriteString(d.tideName)
		g.b.WriteString("(v any, name string) (Dynamic, bool) {\n")
		g.b.WriteString("\tc, _ := v.(*")
		g.b.WriteString(d.tideName)
		g.b.WriteString(")\n")
		g.b.WriteString("\tif c == nil {\n\t\treturn Dynamic{}, false\n\t}\n")
		g.b.WriteString("\tswitch name {\n")
		for _, fi := range d.fields {
			g.b.WriteString("\tcase ")
			g.b.WriteString(strconv.Quote(fi.tideName))
			g.b.WriteString(":\n")
			if fi.descRef != "" {
				g.b.WriteString("\t\treturn Dynamic{Payload: c.")
				g.b.WriteString(fi.tideName)
				g.b.WriteString(", Desc: ")
				g.b.WriteString(fi.descRef)
				g.b.WriteString("}, true\n")
			} else {
				// Unknown-static-type — fall back to tideBoxAny so
				// the descriptor is at least synthesised at runtime.
				g.b.WriteString("\t\treturn tideBoxAny(c.")
				g.b.WriteString(fi.tideName)
				g.b.WriteString("), true\n")
			}
		}
		g.b.WriteString("\t}\n\treturn Dynamic{}, false\n}\n")
	}
	g.b.WriteString("func init() {\n")
	for _, d := range g.descriptors {
		g.b.WriteString("\ttideDescRegistry[")
		g.b.WriteString(strconv.Quote(d.goType))
		g.b.WriteString("] = tideDesc_")
		g.b.WriteString(d.tideName)
		g.b.WriteString("\n")
	}
	// Populate field metadata + accessor registry for each
	// class descriptor. Fields list is emitted as a literal
	// inside init() so it can reference the per-field type
	// descriptors that were declared above.
	for _, d := range g.descriptors {
		if d.kind != "KindClass" {
			continue
		}
		if len(d.fields) > 0 {
			g.b.WriteString("\ttideDesc_")
			g.b.WriteString(d.tideName)
			g.b.WriteString(".fields = []FieldInfo{\n")
			for _, fi := range d.fields {
				g.b.WriteString("\t\t{name: ")
				g.b.WriteString(strconv.Quote(fi.tideName))
				g.b.WriteString(", desc: ")
				if fi.descRef != "" {
					g.b.WriteString(fi.descRef)
				} else {
					g.b.WriteString("nil")
				}
				g.b.WriteString("},\n")
			}
			g.b.WriteString("\t}\n")
		}
		g.b.WriteString("\ttideFieldAccessors[")
		g.b.WriteString(strconv.Quote(d.tideName))
		g.b.WriteString("] = tideFieldOf_")
		g.b.WriteString(d.tideName)
		g.b.WriteString("\n")
	}
	g.b.WriteString("}\n")
}

// descRefForType resolves a Tide TypeExpr to the Go-side var
// name of its type descriptor. Returns "" when the type has no
// emitted descriptor (slices, generics, function types, ...);
// callers handle the empty case by emitting a placeholder.
//
// classNames lists the names of non-generic user classes that
// will have descriptors emitted in this compilation.
func descRefForType(t ast.TypeExpr, classNames map[string]bool) string {
	switch v := t.(type) {
	case *ast.PrimitiveType:
		// rune and byte alias to int32 / uint8 at the Go-runtime
		// level (see writePredeclaredReflect's primitive notes);
		// the descriptors collapse accordingly.
		switch v.Name {
		case "rune":
			return "tideDesc_int32"
		case "byte":
			return "tideDesc_byte"
		default:
			return "tideDesc_" + v.Name
		}
	case *ast.NamedType:
		if len(v.QName) == 1 && classNames[v.QName[0]] {
			return "tideDesc_" + v.QName[0]
		}
	}
	return ""
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
		case *ast.AliasBody:
			walk(v.Aliased)
		case *ast.SumTypeBody:
			for _, vr := range v.Variants {
				for _, fd := range vr.Fields {
					walk(fd)
				}
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
		case *ast.ReturnExpr:
			walk(v.Value)
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
			walk(v.Callee)
			for _, ta := range v.TypeArgs {
				walk(ta)
			}
			for _, a := range v.Args {
				walk(a)
			}
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
	case *ast.SumTypeBody:
		// Lower to a tagged struct per lowering-go.md §MatchIR.
		// The struct holds Tag + every variant's payload fields
		// (renamed to <VariantName><FieldName> to avoid clash
		// across variants). Nullary variants get a `var T_V =
		// T{Tag: N}`; payload variants get a `func T_V(...) T`
		// constructor. Tag is declaration order (§Variant-tag
		// numbering).
		g.line(td.Span.StartLine)
		g.b.WriteString("type ")
		g.b.WriteString(goIdent(td.Name))
		g.b.WriteString(" struct {\n\tTag uint8\n")
		for _, v := range body.Variants {
			for _, f := range v.Fields {
				g.b.WriteByte('\t')
				g.b.WriteString(payloadFieldName(v.Name, f.Name))
				g.b.WriteByte(' ')
				if err := g.emitTypeExpr(f.DeclType); err != nil {
					return err
				}
				g.b.WriteByte('\n')
			}
		}
		g.b.WriteString("}\n")
		// Nullary constants in a single `var ( ... )` block;
		// payload constructors as separate funcs after it.
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
		for i, v := range body.Variants {
			if len(v.Fields) == 0 {
				continue
			}
			g.b.WriteString("func ")
			g.b.WriteString(goIdent(td.Name))
			g.b.WriteString(goIdent(v.Name))
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
			g.b.WriteString(" {\n\treturn ")
			g.b.WriteString(goIdent(td.Name))
			g.b.WriteByte('{')
			g.b.WriteString("Tag: ")
			g.b.WriteString(strconv.Itoa(i))
			for _, f := range v.Fields {
				g.b.WriteString(", ")
				g.b.WriteString(payloadFieldName(v.Name, f.Name))
				g.b.WriteString(": ")
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
		g.b.WriteString(goIdent(f.Name))
		g.b.WriteByte(' ')
		if err := g.emitTypeExpr(f.DeclType); err != nil {
			return err
		}
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
	if err := g.emitBlockBody(m.Body); err != nil {
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
	if err := g.emitBlockBody(fn.Body); err != nil {
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
		// transform is `unit` → an internal struct, which PR-F1
		// doesn't yet emit because no function returns unit at
		// the source level.
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
	case *ast.NamedType:
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
		if len(v.Args) > 0 {
			g.b.WriteByte('[')
			for i, a := range v.Args {
				if i > 0 {
					g.b.WriteString(", ")
				}
				if err := g.emitTypeExpr(a); err != nil {
					return err
				}
			}
			g.b.WriteByte(']')
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

func (g *gen) emitStmt(s ast.Stmt) error {
	switch v := s.(type) {
	case *ast.ExprStmt:
		// ReturnExpr (DivergingExpr): lower to Go `return` stmt.
		if r, ok := v.Expr.(*ast.ReturnExpr); ok {
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
			g.line(v.Span.StartLine)
			g.writeIndent()
			if r.Value == nil {
				g.b.WriteString("return\n")
				return nil
			}
			g.b.WriteString("return ")
			if err := g.emitExpr(r.Value); err != nil {
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
	case *ast.ForStmt:
		return g.emitForStmt(v)
	case *ast.LetStmt:
		// PR-F1 admits only IdentPat at let position (parser
		// enforced). Pattern destructuring lands later.
		idPat, ok := v.Pattern.(*ast.IdentPat)
		if !ok {
			return fmt.Errorf("codegen: only IdentPat in `let` for PR-F1, got %T", v.Pattern)
		}
		return g.emitLetOrVar(v.Span, idPat.Name, v.DeclType, v.Value)
	case *ast.VarStmt:
		return g.emitLetOrVar(v.Span, v.Name, v.DeclType, v.Value)
	case *ast.AssignStmt:
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

// emitMatchAsStmt lowers a MatchExpr at statement position to a
// Go `switch` whose `case` arms run the arm body as a statement.
// Per lowering-go.md §MatchIR, the case head varies by pattern
// shape:
//   - VariantPat / IdentPat-bound-to-variant → `case <tag-int>:`
//     of `switch subject.Tag`.
//   - Literal patterns → `case <literal>:` of `switch subject`.
//   - WildcardPat → `default:`.
//
// PR-F2 uses one of the two switch forms based on whether the
// arm set is variant-based or literal-based; mixing is not
// reached by the corpus and rejected.
func (g *gen) emitMatchAsStmt(m *ast.MatchExpr) error {
	hasVariant, hasLiteral, hasPayloadBinding := false, false, false
	for _, arm := range m.Arms {
		switch p := arm.Pattern.(type) {
		case *ast.VariantPat:
			hasVariant = true
			if len(p.Sub) > 0 {
				hasPayloadBinding = true
			}
		case *ast.IdentPat:
			if _, ok := g.variant[p.Name]; ok {
				hasVariant = true
			}
		case *ast.IntLitPat, *ast.StringLitPat, *ast.BoolLitPat:
			hasLiteral = true
		}
	}
	if hasVariant && hasLiteral {
		return fmt.Errorf("codegen: mixing variant and literal patterns in one match — not yet supported")
	}
	g.line(m.Span.StartLine)
	// If any arm binds payload fields, capture the subject in a
	// temp so each binding can reference it without re-evaluating
	// the subject expression (side-effect safety; lowering-go.md
	// §MatchIR style). Otherwise switch directly on the subject.
	subjectExpr := ""
	if hasPayloadBinding {
		tmp := g.nextMatchTemp()
		g.writeIndent()
		g.b.WriteString(tmp)
		g.b.WriteString(" := ")
		if err := g.emitExpr(m.Subject); err != nil {
			return err
		}
		g.b.WriteByte('\n')
		subjectExpr = tmp
	}
	g.writeIndent()
	g.b.WriteString("switch ")
	if subjectExpr != "" {
		g.b.WriteString(subjectExpr)
	} else {
		if err := g.emitExpr(m.Subject); err != nil {
			return err
		}
	}
	if hasVariant {
		g.b.WriteString(".Tag")
	}
	g.b.WriteString(" {\n")
	for _, arm := range m.Arms {
		g.writeIndent()
		if err := g.emitMatchArmHeader(arm.Pattern); err != nil {
			return err
		}
		g.b.WriteString(":\n")
		g.indent++
		// Payload bindings: emit `b := subject.<PayloadField>` for
		// each sub-pattern on a VariantPat.
		if vp, ok := arm.Pattern.(*ast.VariantPat); ok && len(vp.Sub) > 0 {
			if err := g.emitPayloadBindings(vp, subjectExpr); err != nil {
				return err
			}
		}
		if err := g.emitMatchArmBody(arm.Body, arm.Span); err != nil {
			return err
		}
		g.indent--
	}
	g.writeIndent()
	g.b.WriteString("}\n")
	return nil
}

// emitMatchAsExpr lowers a MatchExpr in value position to a Go IIFE:
// `func() T { switch subject.Tag { case N: return arm_N; … }; var z T; return z }()`.
// The trailing zero-value return is unreachable when the match is
// exhaustive but Go's type checker insists on it for any
// non-terminating branch. Payload-binding arms capture the subject in
// a temp (like emitMatchAsStmt); diverging arms (os.exit / return /
// …) emit as statements with no `return`. See lowering-go.md §MatchIR.
//
// T is matchResultType's peek of the first non-diverging arm (with a
// fmt.scan<T> type-arg fallback for the stdin idiom).
func (g *gen) emitMatchAsExpr(m *ast.MatchExpr) error {
	if len(m.Arms) == 0 {
		return fmt.Errorf("codegen: match expression has no arms")
	}
	// Result type comes from the first arm that actually yields a
	// value — diverging arms (`os.exit`, return/break/continue) have
	// no Go type to peek at (e.g. `match … { Err => os.exit(1),
	// Ok(x) => x }`).
	resultType, err := g.matchResultType(m)
	if err != nil {
		return fmt.Errorf("codegen: match-as-expression: %w", err)
	}
	hasVariant, hasLiteral, hasPayloadBinding := false, false, false
	for _, arm := range m.Arms {
		switch p := arm.Pattern.(type) {
		case *ast.VariantPat:
			hasVariant = true
			if len(p.Sub) > 0 {
				hasPayloadBinding = true
			}
		case *ast.IdentPat:
			if _, ok := g.variant[p.Name]; ok {
				hasVariant = true
			}
		case *ast.IntLitPat, *ast.StringLitPat, *ast.BoolLitPat:
			hasLiteral = true
		}
	}
	if hasVariant && hasLiteral {
		return fmt.Errorf("codegen: mixing variant and literal patterns in one match — not yet supported")
	}
	g.b.WriteString("func() ")
	g.b.WriteString(resultType)
	g.b.WriteString(" { ")
	// Payload-binding arms reference the subject's fields, so capture
	// it in a temp (side-effect safety, mirroring emitMatchAsStmt).
	subjectExpr := ""
	if hasPayloadBinding {
		tmp := g.nextMatchTemp()
		g.b.WriteString(tmp)
		g.b.WriteString(" := ")
		if err := g.emitExpr(m.Subject); err != nil {
			return err
		}
		g.b.WriteString("; ")
		subjectExpr = tmp
	}
	g.b.WriteString("switch ")
	if subjectExpr != "" {
		g.b.WriteString(subjectExpr)
	} else if err := g.emitExpr(m.Subject); err != nil {
		return err
	}
	if hasVariant {
		g.b.WriteString(".Tag")
	}
	g.b.WriteString(" {")
	for _, arm := range m.Arms {
		g.b.WriteByte(' ')
		if err := g.emitMatchArmHeader(arm.Pattern); err != nil {
			return err
		}
		g.b.WriteString(": ")
		if vp, ok := arm.Pattern.(*ast.VariantPat); ok && len(vp.Sub) > 0 {
			if err := g.emitPayloadBindings(vp, subjectExpr); err != nil {
				return err
			}
		}
		// A diverging arm (os.exit / return / …) yields no value —
		// emit it as a statement; control never falls through to the
		// trailing zero-value return.
		if isDivergingExpr(arm.Body) {
			if err := g.emitMatchArmBody(arm.Body, arm.Span); err != nil {
				return err
			}
		} else {
			g.b.WriteString("return ")
			if err := g.emitExpr(arm.Body); err != nil {
				return err
			}
		}
		g.b.WriteByte(';')
	}
	g.b.WriteString(" }; var __zero ")
	g.b.WriteString(resultType)
	g.b.WriteString("; return __zero }()")
	return nil
}

// matchResultType peeks the Go result type of a value-position match
// from the first non-diverging arm. Falls back to the first arm when
// every arm diverges (a never-valued match — the binding it feeds is
// itself unreachable).
func (g *gen) matchResultType(m *ast.MatchExpr) (string, error) {
	var firstErr error
	for _, arm := range m.Arms {
		if isDivergingExpr(arm.Body) {
			continue
		}
		rt, err := g.inferArmResultType(arm.Body)
		if err == nil {
			return rt, nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	// Fallback for the dominant stdin idiom
	// `match fmt.scan<T>() { Err(_) => os.exit(..), Ok(x) => x }`:
	// the value arm yields the Ok payload of Result<T, error>, i.e.
	// T. Codegen knows the scanned type from the call's type-arg even
	// when the payload binding itself can't be peeked.
	if ta := scanTypeArg(m.Subject); ta != nil {
		if s, ok := goTypeArgString(ta); ok {
			return s, nil
		}
	}
	if firstErr != nil {
		return "", firstErr
	}
	return g.inferArmResultType(m.Arms[0].Body)
}

// scanTypeArg returns the single type argument of a `fmt.scan<T>()`
// subject, or nil when the subject is not a fmt.scan call.
func scanTypeArg(subject ast.Expr) ast.TypeExpr {
	c, ok := subject.(*ast.Call)
	if !ok || !isFmtScan(c.Callee) || len(c.TypeArgs) != 1 {
		return nil
	}
	return c.TypeArgs[0]
}

// goTypeArgString renders a type expression to its Go spelling for
// the scalar / named / slice shapes a scan type-arg can take. Returns
// false for shapes outside that set.
func goTypeArgString(t ast.TypeExpr) (string, bool) {
	switch v := t.(type) {
	case *ast.PrimitiveType:
		return v.Name, true
	case *ast.NamedType:
		if len(v.QName) == 1 && len(v.Args) == 0 {
			return goIdent(v.QName[0]), true
		}
	case *ast.SliceType:
		if e, ok := goTypeArgString(v.Elem); ok {
			return "[]" + e, true
		}
	}
	return "", false
}

// isDivergingExpr reports whether e never produces a value: the
// diverging expressions (return/break/continue), an `os.exit(...)`
// call (Never per binding-surface.md §os), or a block whose trailing
// value diverges.
func isDivergingExpr(e ast.Expr) bool {
	switch v := e.(type) {
	case *ast.ParenExpr:
		return isDivergingExpr(v.Inner)
	case *ast.ReturnExpr, *ast.BreakExpr, *ast.ContinueExpr:
		return true
	case *ast.Call:
		return isFieldCall(v.Callee, "os", "exit")
	case *ast.Block:
		return v.Trailing != nil && isDivergingExpr(v.Trailing)
	}
	return false
}

// emitBlockAsExpr lowers a block in value position to an IIFE
// `func() T { stmts; return trailing }()`. T comes from the trailing
// expression's shallow type peek. A block with no trailing value has
// no Go value to yield and is rejected.
func (g *gen) emitBlockAsExpr(b *ast.Block) error {
	if b.Trailing == nil {
		return fmt.Errorf("codegen: value-position block needs a trailing expression")
	}
	rt, err := g.inferArmResultType(b.Trailing)
	if err != nil {
		return fmt.Errorf("codegen: block-as-expression: %w", err)
	}
	g.b.WriteString("func() ")
	g.b.WriteString(rt)
	g.b.WriteString(" {\n")
	g.indent++
	for _, s := range b.Stmts {
		if err := g.emitStmt(s); err != nil {
			return err
		}
	}
	g.line(b.Trailing.NodeSpan().StartLine)
	g.writeIndent()
	g.b.WriteString("return ")
	if err := g.emitExpr(b.Trailing); err != nil {
		return err
	}
	g.b.WriteByte('\n')
	g.indent--
	g.writeIndent()
	g.b.WriteString("}()")
	return nil
}

// emitIfExprAsValue lowers an IfExpr in value position to an IIFE
// whose branches `return` their trailing values:
// `func() T { if c { return a } else { return b } }()`.
func (g *gen) emitIfExprAsValue(e *ast.IfExpr) error {
	rt, err := g.inferArmResultType(e)
	if err != nil {
		return fmt.Errorf("codegen: if-as-expression: %w", err)
	}
	g.b.WriteString("func() ")
	g.b.WriteString(rt)
	g.b.WriteString(" {\n")
	g.indent++
	if err := g.emitIfExprReturning(e); err != nil {
		return err
	}
	g.indent--
	g.writeIndent()
	g.b.WriteString("}()")
	return nil
}

// emitIfExprReturning lowers a value-position IfExpr — each branch
// `return`s its trailing value, and both arms are required (see
// emitIf). Defined alongside emitIfStmt / emitIfExprAsStmt.
func (g *gen) emitIfExprReturning(e *ast.IfExpr) error {
	return g.emitIf(ifExprShape(e), false, g.emitBranchReturn, asIfExprShape, true)
}

// emitBranchReturn emits a value-block's statements followed by
// `return <trailing>`.
func (g *gen) emitBranchReturn(b *ast.Block) error {
	for _, s := range b.Stmts {
		if err := g.emitStmt(s); err != nil {
			return err
		}
	}
	if b.Trailing == nil {
		return fmt.Errorf("codegen: value-position `if` branch needs a trailing expression")
	}
	g.line(b.Trailing.NodeSpan().StartLine)
	g.writeIndent()
	g.b.WriteString("return ")
	if err := g.emitExpr(b.Trailing); err != nil {
		return err
	}
	g.b.WriteByte('\n')
	return nil
}

// inferArmResultType returns the Go-side type name for an
// expression at a match arm position. Covers sum-variant refs
// (owner sum-type), variant constructor calls, primitive
// literals, and Ident references to container bindings (their
// kind read from the sema side-table). Returns an error for
// shapes not yet covered by this shallow arm-type peek.
func (g *gen) inferArmResultType(e ast.Expr) (string, error) {
	// AST fast paths — resolvable without sema (variant refs map to
	// their owner sum type; literals to their natural Go type).
	switch v := e.(type) {
	case *ast.Ident:
		if info, ok := g.variant[v.Name]; ok {
			return goIdent(info.owner), nil
		}
		if k := g.varKindOf(v); k != "" {
			return k, nil
		}
	case *ast.Call:
		if id, ok := v.Callee.(*ast.Ident); ok {
			if info, ok := g.variant[id.Name]; ok {
				return goIdent(info.owner), nil
			}
		}
	case *ast.IntLitExpr:
		return "int", nil
	case *ast.StringLitExpr:
		return "string", nil
	case *ast.BoolLitExpr:
		return "bool", nil
	case *ast.RuneLitExpr:
		return "rune", nil
	case *ast.Block:
		// A value block's type is its trailing expression's type.
		if v.Trailing != nil {
			if s, err := g.inferArmResultType(v.Trailing); err == nil {
				return s, nil
			}
		}
	case *ast.IfExpr:
		// An if-expression's type is its then-branch value's type
		// (branches are required to agree — sema's concern).
		if v.ThenBlock != nil && v.ThenBlock.Trailing != nil {
			if s, err := g.inferArmResultType(v.ThenBlock.Trailing); err == nil {
				return s, nil
			}
		}
	}
	// Fallback: sema's inferred type for the expression. Covers
	// locals, typed calls, and any value the shallow peek misses.
	if g.info != nil {
		if t := g.info.Type[e]; t != nil {
			if s, ok := g.goTypeFromSema(t); ok {
				return s, nil
			}
		}
	}
	return "", fmt.Errorf("cannot infer Go type for %T arm/branch result — annotate the surrounding binding", e)
}

// goTypeFromSema renders a sema type to its Go spelling for the
// shapes a value-position block / if / match result can take. Tide
// primitives map 1:1 (lowering-go.md §Primitive type lowering);
// classes are reference types (`*T`). Shapes outside this set return
// false — the caller then reports an un-inferrable arm result.
// emitTupleLit lowers `(e0, e1, …)` to an anonymous Go struct
// literal `struct { _0 T0; … }{ _0: e0, … }`. The struct type comes
// from sema's inferred Tuple type so the literal shares its Go type
// with any matching annotation / field access.
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

func (g *gen) goTypeFromSema(t sema.Type) (string, bool) {
	switch v := t.(type) {
	case *sema.Builtin:
		// `unit` has no first-class Go spelling — a unit-valued
		// block as a value is rejected rather than emitting `unit`.
		if v.N == "unit" {
			return "", false
		}
		return v.N, true
	case *sema.Named:
		if _, isClass := g.class[v.N]; isClass {
			return "*" + goIdent(v.N), true
		}
		return goIdent(v.N), true
	case *sema.Slice:
		if elem, ok := g.goTypeFromSema(v.Elem); ok {
			return "[]" + elem, true
		}
	case *sema.Tuple:
		var sb strings.Builder
		sb.WriteString("struct { ")
		for i, c := range v.Comps {
			ct, ok := g.goTypeFromSema(c)
			if !ok {
				return "", false
			}
			if i > 0 {
				sb.WriteString("; ")
			}
			sb.WriteString("_")
			sb.WriteString(strconv.Itoa(i))
			sb.WriteByte(' ')
			sb.WriteString(ct)
		}
		sb.WriteString(" }")
		return sb.String(), true
	}
	return "", false
}

// emitPayloadBindings writes one `b := <subject>.<PayloadField>`
// line per sub-pattern of a VariantPat. IdentPat sub-patterns
// produce a binding; WildcardPat sub-patterns emit nothing.
// Other sub-pattern shapes (nested VariantPat etc.) are not
// supported in v1.
func (g *gen) emitPayloadBindings(vp *ast.VariantPat, subjectExpr string) error {
	name := lastSeg(vp.QName)
	info, ok := g.variant[name]
	if !ok {
		return fmt.Errorf("codegen: variant pattern %s does not match any declared sum-type variant", name)
	}
	if len(vp.Sub) != len(info.fields) {
		return fmt.Errorf("codegen: variant pattern %s expects %d sub-pattern(s), got %d",
			name, len(info.fields), len(vp.Sub))
	}
	for i, sub := range vp.Sub {
		switch sp := sub.(type) {
		case *ast.IdentPat:
			g.writeIndent()
			g.b.WriteString(goIdent(sp.Name))
			g.b.WriteString(" := ")
			g.b.WriteString(subjectExpr)
			g.b.WriteByte('.')
			// Predeclared sums use spec-fixed field names (V / E)
			// per `lang-spec/lowering-go.md` §Container types;
			// user-declared variants follow the PR-F5a
			// `<Variant><FieldName>` convention.
			if pf := predeclaredPayloadField(name); pf != "" {
				g.b.WriteString(pf)
			} else {
				g.b.WriteString(payloadFieldName(name, info.fields[i].Name))
			}
			g.b.WriteByte('\n')
		case *ast.WildcardPat:
			// Nothing to bind.
		default:
			return fmt.Errorf("codegen: nested sub-pattern %T in variant payload not supported in v1", sub)
		}
	}
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

// emitTryPreamble lowers a `try e` at statement position per
// `lang-spec/desugaring.md` §T-Try-Result / §T-Try-Option:
// evaluates the inner expression into a fresh temp, then emits
// an if-bail block that early-returns the wrapped Err / None
// shape of the enclosing function's return type. The returned
// Go identifier is the temp name; the caller pulls the unwrapped
// payload via `<tmp>.V`. Bail-tag is 1 for Result (Err), 0 for
// Option (None); determined from `g.curFuncReturn` which sema
// (PR-Sema-2) will tighten to also account for inner-expr type.
func (g *gen) emitTryPreamble(t *ast.TryExpr) (string, error) {
	if g.curFuncReturn == nil {
		return "", fmt.Errorf("codegen: `try` outside a function that returns Result/Option")
	}
	ret, ok := g.curFuncReturn.(*ast.NamedType)
	if !ok || len(ret.QName) != 1 {
		return "", fmt.Errorf("codegen: `try` requires the enclosing function's return type to be Result/Option, got %T", g.curFuncReturn)
	}
	var bailTag int
	switch ret.QName[0] {
	case "Result":
		bailTag = 1 // Err
	case "Option":
		bailTag = 0 // None
	default:
		return "", fmt.Errorf("codegen: `try` requires the enclosing function's return type to be Result/Option, got %s", ret.QName[0])
	}
	g.tryTempCounter++
	tmp := fmt.Sprintf("__tide_try_%d", g.tryTempCounter)
	g.line(t.Span.StartLine)
	g.writeIndent()
	g.b.WriteString(tmp)
	g.b.WriteString(" := ")
	if err := g.emitExpr(t.Inner); err != nil {
		return "", err
	}
	g.b.WriteByte('\n')
	g.writeIndent()
	g.b.WriteString("if ")
	g.b.WriteString(tmp)
	g.b.WriteString(".Tag == ")
	g.b.WriteString(strconv.Itoa(bailTag))
	g.b.WriteString(" {\n")
	g.indent++
	g.writeIndent()
	g.b.WriteString("return ")
	if err := g.emitTypeExpr(ret); err != nil {
		return "", err
	}
	g.b.WriteByte('{')
	g.b.WriteString("Tag: ")
	g.b.WriteString(strconv.Itoa(bailTag))
	if ret.QName[0] == "Result" {
		g.b.WriteString(", E: ")
		g.b.WriteString(tmp)
		g.b.WriteString(".E")
	}
	g.b.WriteString("}\n")
	g.indent--
	g.writeIndent()
	g.b.WriteString("}\n")
	return tmp, nil
}

// emitExpr's TryExpr arm — reachable only at unsupported
// expression positions (binary operand, call argument, etc.).
// Statement-position `try` is handled in emitStmt / emitLetOrVar
// without going through emitExpr.
func (g *gen) tryExprErr() error {
	return fmt.Errorf("codegen: `try` in expression position not yet supported — use it at let/var/return position; full expression-position lands with PR-Sema-2 (block expressions)")
}

// nextMatchTemp returns a fresh Go identifier reserved for the
// captured `match` subject. The `__tide_` prefix makes it
// vanishingly unlikely to collide with a user-written name even
// if the user takes the unusual step of writing
// underscore-prefixed identifiers. The runtime-prefix convention
// is shared with other codegen-internal temps.
func (g *gen) nextMatchTemp() string {
	g.matchTempCounter++
	return fmt.Sprintf("__tide_match_%d", g.matchTempCounter)
}

// emitMatchArmHeader writes either `case <expr>` or `default`.
func (g *gen) emitMatchArmHeader(p ast.Pattern) error {
	switch pat := p.(type) {
	case *ast.WildcardPat:
		g.b.WriteString("default")
		return nil
	case *ast.IntLitPat:
		g.b.WriteString("case ")
		g.b.WriteString(strconv.FormatInt(pat.Value, 10))
		return nil
	case *ast.StringLitPat:
		g.b.WriteString("case ")
		g.b.WriteString(strconv.Quote(pat.Value))
		return nil
	case *ast.BoolLitPat:
		g.b.WriteString("case ")
		if pat.Value {
			g.b.WriteString("true")
		} else {
			g.b.WriteString("false")
		}
		return nil
	case *ast.VariantPat:
		// Payload sub-patterns are valid in PR-F5+; bindings are
		// emitted separately by emitPayloadBindings between the
		// case header and the arm body.
		info, ok := g.variant[lastSeg(pat.QName)]
		if !ok {
			return fmt.Errorf("codegen: variant pattern %s does not match any declared sum-type variant", lastSeg(pat.QName))
		}
		g.b.WriteString("case ")
		g.b.WriteString(strconv.Itoa(info.tag))
		return nil
	case *ast.IdentPat:
		if info, ok := g.variant[pat.Name]; ok {
			g.b.WriteString("case ")
			g.b.WriteString(strconv.Itoa(info.tag))
			return nil
		}
		return fmt.Errorf("codegen: IdentPat %q in match arm is a fresh binding — only variant patterns supported in PR-F2", pat.Name)
	}
	return fmt.Errorf("codegen: unsupported pattern %T", p)
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

// emitMatchArmBody emits the arm body as a Go statement. The
// arm body in source is an Expr; we wrap it in a synthetic
// ExprStmt so the existing statement-lowering paths work. A
// ReturnExpr arm body lowers to a `return` statement as usual.
func (g *gen) emitMatchArmBody(body ast.Expr, _ ast.Span) error {
	return g.emitStmt(&ast.ExprStmt{Span: body.NodeSpan(), Expr: body})
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
	if err := g.emitExpr(value); err != nil {
		return err
	}
	g.b.WriteByte('\n')
	return nil
}

// ifShape is the position-independent view of an `if` chain that
// emitIf lowers. Both *ast.IfStmt and *ast.IfExpr collapse to it, so
// one walk serves statement and value position (lowering-go.md
// §Source maps / §If).
type ifShape struct {
	cond      ast.Expr
	thenBlock *ast.Block
	elseNode  ast.Node // nil | *ast.Block | nested *ast.IfStmt|*ast.IfExpr
	startLine int
}

func ifStmtShape(s *ast.IfStmt) ifShape {
	return ifShape{cond: s.Cond, thenBlock: s.ThenBlock, elseNode: s.Else, startLine: s.Span.StartLine}
}

func ifExprShape(e *ast.IfExpr) ifShape {
	return ifShape{cond: e.Cond, thenBlock: e.ThenBlock, elseNode: e.Else, startLine: e.Span.StartLine}
}

func asIfStmtShape(n ast.Node) (ifShape, bool) {
	if s, ok := n.(*ast.IfStmt); ok {
		return ifStmtShape(s), true
	}
	return ifShape{}, false
}

func asIfExprShape(n ast.Node) (ifShape, bool) {
	if e, ok := n.(*ast.IfExpr); ok {
		return ifExprShape(e), true
	}
	return ifShape{}, false
}

// emitIf lowers one `if`/`else` chain. The three axes that distinguish
// statement vs value position and IfStmt vs IfExpr are parameters:
//   - emitBody: how a branch block lowers — discard the trailing value
//     (emitBlockBody) or `return` it (emitBranchReturn).
//   - nestedShape: detects an else-if of the same node kind and adapts
//     it, so the recursion is kind-agnostic.
//   - requireElse: value position needs both arms; an else-less chain
//     is an error rather than a bare `}`.
//
// isElseIf is true when the caller has already written `} else ` — the
// `if` then continues inline without a fresh indent. A //line directive
// is emitted at every `if` boundary (§Source maps).
func (g *gen) emitIf(sh ifShape, isElseIf bool, emitBody func(*ast.Block) error,
	nestedShape func(ast.Node) (ifShape, bool), requireElse bool) error {
	g.line(sh.startLine)
	if !isElseIf {
		g.writeIndent()
	}
	g.b.WriteString("if ")
	if err := g.emitExpr(sh.cond); err != nil {
		return err
	}
	g.b.WriteString(" {\n")
	g.indent++
	if err := emitBody(sh.thenBlock); err != nil {
		return err
	}
	g.indent--
	switch {
	case sh.elseNode == nil:
		if requireElse {
			return fmt.Errorf("codegen: value-position `if` requires an `else` branch")
		}
		g.writeIndent()
		g.b.WriteString("}\n")
	default:
		if nested, ok := nestedShape(sh.elseNode); ok {
			g.writeIndent()
			g.b.WriteString("} else ")
			return g.emitIf(nested, true, emitBody, nestedShape, requireElse)
		}
		blk, ok := sh.elseNode.(*ast.Block)
		if !ok {
			return fmt.Errorf("codegen: unexpected else branch %T", sh.elseNode)
		}
		g.writeIndent()
		g.b.WriteString("} else {\n")
		g.indent++
		if err := emitBody(blk); err != nil {
			return err
		}
		g.indent--
		g.writeIndent()
		g.b.WriteString("}\n")
	}
	return nil
}

// emitIfStmt lowers a statement-position `if` (IfStmt) — branch values
// discarded.
func (g *gen) emitIfStmt(s *ast.IfStmt) error {
	return g.emitIf(ifStmtShape(s), false, g.emitBlockBody, asIfStmtShape, false)
}

// emitIfExprAsStmt lowers an IfExpr in statement position (e.g. a
// match-arm body in a statement-position match) — branch values
// discarded.
func (g *gen) emitIfExprAsStmt(e *ast.IfExpr) error {
	return g.emitIf(ifExprShape(e), false, g.emitBlockBody, asIfExprShape, false)
}

// emitWhileStmt lowers `while cond { body }` to Go's condition-only
// `for cond { body }` (lowering-go.md §Loops).
func (g *gen) emitWhileStmt(s *ast.WhileStmt) error {
	g.line(s.Span.StartLine)
	g.writeIndent()
	g.b.WriteString("for ")
	if err := g.emitExpr(s.Cond); err != nil {
		return err
	}
	g.b.WriteString(" {\n")
	g.indent++
	if err := g.emitBlockBody(s.Body); err != nil {
		return err
	}
	g.indent--
	g.writeIndent()
	g.b.WriteString("}\n")
	return nil
}

// patGoName renders a for-loop sub-pattern as a Go binding: the
// identifier name, or `_` for a wildcard. Other shapes are rejected.
func patGoName(p ast.Pattern) (string, error) {
	switch v := p.(type) {
	case *ast.IdentPat:
		return goIdent(v.Name), nil
	case *ast.WildcardPat:
		return "_", nil
	}
	return "", fmt.Errorf("codegen: for-loop tuple component must be a name or `_`, got %T", p)
}

// emitForTuple lowers `for (a, b) in coll { … }`. For a Map it walks
// the insertion-order key slice and indexes the value (deterministic,
// like the single-key form); for slices/other it uses Go's native
// `range` index/value pair.
func (g *gen) emitForTuple(s *ast.ForStmt, tp *ast.TuplePat) error {
	if len(tp.Sub) != 2 {
		return fmt.Errorf("codegen: tuple-pattern for-loop supports exactly 2 components, got %d", len(tp.Sub))
	}
	a, err := patGoName(tp.Sub[0])
	if err != nil {
		return err
	}
	b, err := patGoName(tp.Sub[1])
	if err != nil {
		return err
	}
	iterExpr, ok := s.Iterable.(ast.Expr)
	if !ok {
		return fmt.Errorf("codegen: tuple-pattern for-loop needs a collection iterable, got %T", s.Iterable)
	}
	g.line(s.Span.StartLine)
	g.writeIndent()
	if id, ok := iterExpr.(*ast.Ident); ok && g.varKindOf(id) == "Map" {
		// `for (k, v) in m` — deterministic over insertion order; the
		// value is fetched per key. A `_` key still needs a name to
		// index with when the value is bound.
		keyVar := a
		if keyVar == "_" && b != "_" {
			keyVar = g.nextMatchTemp()
		}
		g.b.WriteString("for _, ")
		g.b.WriteString(keyVar)
		g.b.WriteString(" := range ")
		if err := g.emitExpr(id); err != nil {
			return err
		}
		g.b.WriteString(".order {\n")
		g.indent++
		if b != "_" {
			g.writeIndent()
			g.b.WriteString(b)
			g.b.WriteString(" := ")
			if err := g.emitExpr(id); err != nil {
				return err
			}
			g.b.WriteString(".m[")
			g.b.WriteString(keyVar)
			g.b.WriteString("]\n")
		}
		if err := g.emitBlockBody(s.Body); err != nil {
			return err
		}
		g.indent--
		g.writeIndent()
		g.b.WriteString("}\n")
		return nil
	}
	// Slice / other collection — Go-native index/value range.
	g.b.WriteString("for ")
	g.b.WriteString(a)
	g.b.WriteString(", ")
	g.b.WriteString(b)
	g.b.WriteString(" := range ")
	if err := g.emitExpr(iterExpr); err != nil {
		return err
	}
	g.b.WriteString(" {\n")
	g.indent++
	if err := g.emitBlockBody(s.Body); err != nil {
		return err
	}
	g.indent--
	g.writeIndent()
	g.b.WriteString("}\n")
	return nil
}

func (g *gen) emitForStmt(s *ast.ForStmt) error {
	if tp, ok := s.Pattern.(*ast.TuplePat); ok {
		return g.emitForTuple(s, tp)
	}
	g.line(s.Span.StartLine)
	g.writeIndent()
	idPat, ok := s.Pattern.(*ast.IdentPat)
	if !ok {
		return fmt.Errorf("codegen: only IdentPat loop var in PR-C, got %T", s.Pattern)
	}
	switch iter := s.Iterable.(type) {
	case *ast.RangeExpr:
		g.b.WriteString("for ")
		g.b.WriteString(goIdent(idPat.Name))
		g.b.WriteString(" := ")
		if err := g.emitExpr(iter.Low); err != nil {
			return err
		}
		g.b.WriteString("; ")
		g.b.WriteString(goIdent(idPat.Name))
		if iter.Inclusive {
			g.b.WriteString(" <= ")
		} else {
			g.b.WriteString(" < ")
		}
		if err := g.emitExpr(iter.High); err != nil {
			return err
		}
		g.b.WriteString("; ")
		g.b.WriteString(goIdent(idPat.Name))
		g.b.WriteString("++ {\n")
	default:
		// Any other Iterable is a slice / map / set / channel
		// per builtins.md §IterElem.
		iterExpr, ok := iter.(ast.Expr)
		if !ok {
			return fmt.Errorf("codegen: unsupported iterable %T", iter)
		}
		// Map iteration — `for k in m` walks the wrapper's
		// insertion-order slice so iteration is deterministic
		// (Go's bare `range m.m` is randomised). For now we
		// expose keys only via this short form; tuple-form
		// `for (k, v) in m` and `m.entries()` come later.
		if id, ok := iterExpr.(*ast.Ident); ok && g.varKindOf(id) == "Map" {
			g.b.WriteString("for _, ")
			g.b.WriteString(goIdent(idPat.Name))
			g.b.WriteString(" := range ")
			if err := g.emitExpr(id); err != nil {
				return err
			}
			g.b.WriteString(".order {\n")
			break
		}
		// Set iteration — same idea against `s.order`.
		if id, ok := iterExpr.(*ast.Ident); ok && g.varKindOf(id) == "Set" {
			g.b.WriteString("for _, ")
			g.b.WriteString(goIdent(idPat.Name))
			g.b.WriteString(" := range ")
			if err := g.emitExpr(id); err != nil {
				return err
			}
			g.b.WriteString(".order {\n")
			break
		}
		g.b.WriteString("for _, ")
		g.b.WriteString(goIdent(idPat.Name))
		g.b.WriteString(" := range ")
		if err := g.emitExpr(iterExpr); err != nil {
			return err
		}
		g.b.WriteString(" {\n")
	}
	g.indent++
	if err := g.emitBlockBody(s.Body); err != nil {
		return err
	}
	g.indent--
	g.writeIndent()
	g.b.WriteString("}\n")
	return nil
}

// ---- expressions ----

func (g *gen) emitExpr(e ast.Expr) error {
	switch v := e.(type) {
	case *ast.IntLitExpr:
		g.b.WriteString(strconv.FormatInt(v.Value, 10))
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
			g.b.WriteString(goIdent(info.owner))
			g.b.WriteString(goIdent(v.Name))
			return nil
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
		} else {
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
		// `try` is only supported at statement-position sites
		// today (let / var / return value); the supporting paths
		// in emitStmt / emitLetOrVar intercept before reaching
		// emitExpr. Anything else (e.g., `try e + 1` inside an
		// arithmetic expression, or `f(try e)` as a call argument)
		// requires expression-position match-or-block lowering,
		// deferred until sema PR.
		return g.tryExprErr()
	}
	return fmt.Errorf("codegen: unhandled expression %T", e)
}

func (g *gen) emitField(f *ast.Field) error {
	if err := g.emitExpr(f.Receiver); err != nil {
		return err
	}
	g.b.WriteByte('.')
	g.b.WriteString(mapFieldName(f.Receiver, f.Name))
	return nil
}

func (g *gen) emitCall(c *ast.Call) error {
	// reflect.* dispatch — lower to inline tidert helpers per
	// `lang-spec/builtins.md` §reflect. Codegen owns reflect's
	// surface (it's runtime-supplied, not a Go-stdlib binding).
	if f, ok := c.Callee.(*ast.Field); ok {
		if recvID, ok := f.Receiver.(*ast.Ident); ok && recvID.Name == "reflect" {
			return g.emitReflectCall(f.Name, c.TypeArgs, c.Args)
		}
	}
	// fmt.scan<T>() — stdin binding. Lowers to the tideScan helper,
	// which wraps Go's pointer-mutation `fmt.Scan(&v)` into the
	// Result<T, error> return form (binding-surface.md §fmt).
	if isFmtScan(c.Callee) {
		g.b.WriteString("tideScan")
		if len(c.TypeArgs) > 0 {
			g.b.WriteByte('[')
			for i, ta := range c.TypeArgs {
				if i > 0 {
					g.b.WriteString(", ")
				}
				if err := g.emitTypeExpr(ta); err != nil {
					return err
				}
			}
			g.b.WriteByte(']')
		}
		g.b.WriteString("()")
		return nil
	}
	// strings.fromBytes(b) — the []byte → string round-trip binding
	// (binding-surface.md §strings). Lowers to Go's `string(b)`
	// conversion.
	if isFieldCall(c.Callee, "strings", "fromBytes") && len(c.Args) == 1 {
		g.b.WriteString("string(")
		if err := g.emitExpr(c.Args[0]); err != nil {
			return err
		}
		g.b.WriteByte(')')
		return nil
	}
	// makeSlice<T>(n, v) — predeclared generic builtin lowering
	// to the inline tideMakeSlice helper.
	if id, ok := c.Callee.(*ast.Ident); ok && id.Name == "makeSlice" {
		g.b.WriteString("tideMakeSlice")
		if len(c.TypeArgs) > 0 {
			g.b.WriteByte('[')
			for i, ta := range c.TypeArgs {
				if i > 0 {
					g.b.WriteString(", ")
				}
				if err := g.emitTypeExpr(ta); err != nil {
					return err
				}
			}
			g.b.WriteByte(']')
		}
		g.b.WriteByte('(')
		for i, a := range c.Args {
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
	// Class constructor shim — `ClassName(args)` in source lowers
	// to `&ClassName{args...}` (positional fields). Per
	// lowering-go.md §Implicit receiver, class instances are
	// pointer-typed; instantiation produces a *ClassName.
	if id, ok := c.Callee.(*ast.Ident); ok {
		if ci, isClass := g.class[id.Name]; isClass {
			if ci.generic && len(c.TypeArgs) == 0 {
				return fmt.Errorf("codegen: constructor call %s(...) on generic class needs explicit type arguments — write %s<T>(...)", id.Name, id.Name)
			}
			g.b.WriteByte('&')
			g.b.WriteString(goIdent(id.Name))
			if len(c.TypeArgs) > 0 {
				g.b.WriteByte('[')
				for i, ta := range c.TypeArgs {
					if i > 0 {
						g.b.WriteString(", ")
					}
					if err := g.emitTypeExpr(ta); err != nil {
						return err
					}
				}
				g.b.WriteByte(']')
			}
			g.b.WriteByte('{')
			for i, a := range c.Args {
				if i > 0 {
					g.b.WriteString(", ")
				}
				if err := g.emitExpr(a); err != nil {
					return err
				}
			}
			g.b.WriteByte('}')
			return nil
		}
	}
	// Static method call — `ClassName.method(args)` lowers to
	// package-level `<className>Method(args)` per
	// lowering-go.md §Generics.
	if f, ok := c.Callee.(*ast.Field); ok {
		if recvID, ok := f.Receiver.(*ast.Ident); ok {
			if ci, isClass := g.class[recvID.Name]; isClass && ci.statics[f.Name] {
				g.b.WriteString(staticMethodName(recvID.Name, f.Name))
				// Thread the call-site TypeArgs onto the generated
				// package-level Go function (per `lowering-go.md`
				// §Generics — `Box<int>.new(...)` → `boxNew[int](...)`).
				if len(c.TypeArgs) > 0 {
					g.b.WriteByte('[')
					for i, ta := range c.TypeArgs {
						if i > 0 {
							g.b.WriteString(", ")
						}
						if err := g.emitTypeExpr(ta); err != nil {
							return err
						}
					}
					g.b.WriteByte(']')
				}
				g.b.WriteByte('(')
				for i, a := range c.Args {
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
		}
	}
	// Slice method shortcuts per builtins.md §Slice methods:
	//   xs.push(e) → append(xs, e)
	//   xs.len()   → len(xs)
	// Triggered when the callee is a Field access whose receiver
	// is NOT a known stdlib namespace (e.g. fmt, os, strings).
	// Without sema this is a heuristic: any `.push`/`.len` on a
	// non-namespace receiver is a slice method. Sema'll tighten.
	if f, ok := c.Callee.(*ast.Field); ok && !isStdlibNamespace(f.Receiver) && !g.isContainerReceiver(f.Receiver) {
		switch f.Name {
		case "push":
			if len(c.Args) != 1 {
				return fmt.Errorf("codegen: .push expects exactly one argument, got %d", len(c.Args))
			}
			g.b.WriteString("append(")
			if err := g.emitExpr(f.Receiver); err != nil {
				return err
			}
			g.b.WriteString(", ")
			if err := g.emitExpr(c.Args[0]); err != nil {
				return err
			}
			g.b.WriteByte(')')
			return nil
		case "len":
			if len(c.Args) != 0 {
				return fmt.Errorf("codegen: .len takes no arguments, got %d", len(c.Args))
			}
			g.b.WriteString("len(")
			if err := g.emitExpr(f.Receiver); err != nil {
				return err
			}
			g.b.WriteByte(')')
			return nil
		case "bytes":
			// `s.bytes()` views a string as []byte — Go's
			// `[]byte(s)` conversion (binding-surface.md §strings).
			if len(c.Args) != 0 {
				return fmt.Errorf("codegen: .bytes takes no arguments, got %d", len(c.Args))
			}
			g.b.WriteString("[]byte(")
			if err := g.emitExpr(f.Receiver); err != nil {
				return err
			}
			g.b.WriteByte(')')
			return nil
		case "runes":
			// `s.runes()` views a string as []rune — Go's
			// `[]rune(s)` conversion (binding-surface.md §strings).
			if len(c.Args) != 0 {
				return fmt.Errorf("codegen: .runes takes no arguments, got %d", len(c.Args))
			}
			g.b.WriteString("[]rune(")
			if err := g.emitExpr(f.Receiver); err != nil {
				return err
			}
			g.b.WriteByte(')')
			return nil
		}
	}
	if err := g.emitExpr(c.Callee); err != nil {
		return err
	}
	if len(c.TypeArgs) > 0 {
		g.b.WriteByte('[')
		for i, ta := range c.TypeArgs {
			if i > 0 {
				g.b.WriteString(", ")
			}
			if err := g.emitTypeExpr(ta); err != nil {
				return err
			}
		}
		g.b.WriteByte(']')
	}
	g.b.WriteByte('(')
	for i, a := range c.Args {
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

// isContainerReceiver reports whether the receiver expression
// resolves to a predeclared-container instance (Map / Set /
// Stack). Used by emitCall to suppress the slice-method
// shortcut on container instances whose method names happen to
// collide with slice methods (`.len()`, `.push()`).
func (g *gen) isContainerReceiver(e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	if !ok {
		return false
	}
	return g.varKindOf(id) != ""
}

// varKindOf returns the predeclared-container kind ("Map" / "Set" /
// "Stack") of the binding referenced by id, read from the sema
// side-table (its inferred type), or "" when id is not a container
// instance. This replaces codegen's former shallow varKind tracker
// with sema as the single source of truth.
func (g *gen) varKindOf(id *ast.Ident) string {
	if g.info == nil {
		return ""
	}
	t := g.info.Type[id]
	if t == nil {
		if sym := g.info.Symbol[id]; sym != nil {
			t = sym.Type
		}
	}
	switch t.(type) {
	case *sema.Map:
		return "Map"
	case *sema.Set:
		return "Set"
	case *sema.Stack:
		return "Stack"
	}
	return ""
}

// isStdlibNamespace reports whether expr is an Ident whose name
// is in the hardcoded stdlib binding registry. Used by emitCall
// to keep `fmt.println` from being interpreted as a slice
// method call.
func isStdlibNamespace(e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	return ok && isStdlibNamespaceName(id.Name)
}

func isStdlibNamespaceName(name string) bool {
	switch name {
	case "fmt", "os", "strings", "strconv", "bufio", "context",
		"time", "sync", "io", "log", "net", "encoding", "math":
		return true
	}
	return false
}

// isConversionBinding reports whether the stdlib binding pkg.method
// lowers to a Go type conversion (not a pkg.* call), so it needs no
// import. Currently only `strings.fromBytes` → `string(b)`.
func isConversionBinding(pkg, method string) bool {
	return pkg == "strings" && method == "fromBytes"
}

// mapFieldName is the PR-C shortcut for binding calls. Tide
// `fmt.println` maps to Go `fmt.Println` etc. This bypasses the
// full bindgen pipeline; only the names hello/fizzbuzz use are
// hardcoded.
func mapFieldName(receiver ast.Expr, name string) string {
	id, ok := receiver.(*ast.Ident)
	if !ok {
		return goIdent(name)
	}
	switch id.Name {
	case "fmt":
		switch name {
		case "println":
			return "Println"
		case "print":
			return "Print"
		case "printf":
			return "Printf"
		case "sprintf":
			return "Sprintf"
		}
	case "os":
		switch name {
		case "exit":
			return "Exit"
		}
	case "math":
		switch name {
		case "floor":
			return "Floor"
		case "log10":
			return "Log10"
		}
	}
	return goIdent(name)
}

// isFmtScan reports whether e is the callee `fmt.scan` (the single-
// value stdin binding). Multi-value scan2/scan3 land later.
func isFmtScan(e ast.Expr) bool {
	return isFieldCall(e, "fmt", "scan")
}

// isFieldCall reports whether e is the field access `recv.name`
// where recv is the bare identifier `recvName` — used to recognise
// namespaced stdlib bindings (`strings.fromBytes`, `fmt.scan`).
func isFieldCall(e ast.Expr, recvName, name string) bool {
	f, ok := e.(*ast.Field)
	if !ok || f.Name != name {
		return false
	}
	recv, ok := f.Receiver.(*ast.Ident)
	return ok && recv.Name == recvName
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
