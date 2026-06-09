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
		case *ast.InterfaceDecl:
			if err := g.emitInterfaceDecl(v); err != nil {
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
	usesResultOf  bool
	usesTryRecv   bool
	usesScope     bool
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
	// tryTempCounter generates unique temp names for `try`
	// emission. Same hygiene as matchTempCounter.
	tryTempCounter int
	// loopTempCounter generates unique throwaway counter names for
	// `for _ in low..high` (a wildcard loop var over a numeric range,
	// where Go's `i++` form needs a named — not `_` — counter).
	loopTempCounter int
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
	if g.usesScope {
		// Structured-concurrency scopes lower onto an inline group
		// helper built from the standard library (no errgroup dep —
		// generated modules are stdlib-only).
		add("context")
		add("sync")
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
	g.writePredeclaredResultOf()
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
			if f, ok := v.Callee.(*ast.Field); ok && f.Name == "tryRecv" {
				g.usesTryRecv = true
				g.usesOption = true
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
		// A nominal record lowers to a named Go struct. All generated
		// code is package `main`, so Tide field names map directly
		// (unexported Go fields are visible within the package) — no
		// capitalisation needed (lowering-go.md §Record lowering).
		g.line(td.Span.StartLine)
		g.b.WriteString("type ")
		g.b.WriteString(goIdent(td.Name))
		g.b.WriteString(" struct {\n")
		g.indent++
		for _, f := range body.Fields {
			g.writeIndent()
			g.b.WriteString(goIdent(f.Name))
			g.b.WriteByte(' ')
			if err := g.emitTypeExpr(f.DeclType); err != nil {
				return err
			}
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
			g.line(v.Span.StartLine)
			g.writeIndent()
			if r.Value == nil {
				g.b.WriteString("return\n")
				return nil
			}
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
		g.b.WriteString(goIdent(re.Name))
		g.b.WriteString(": ")
		if err := g.emitExpr(re.Value); err != nil {
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
				g.b.WriteString(goIdent(v.Name))
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
	g.b.WriteString(g.goFieldName(f.Receiver, f.Name))
	return nil
}

// goFieldName maps a Tide field / method name to its Go spelling.
// Beyond the stdlib-namespace renames (mapFieldName), it applies the
// D6 binding-name rewrite for the one Go-interface method reachable in
// v1: `.error()` on a value typed as the predeclared `error` builtin is
// Go's `error.Error()` (the PascalCase↔lowerCamel boundary convention;
// D14 footnote). A user class that *implements* `error` is typed
// nominally — never as the builtin — so its own `error()` method is
// left untouched.
func (g *gen) goFieldName(receiver ast.Expr, name string) string {
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
