package codegen

import (
	"strconv"

	"github.com/heni/tide-lang/internal/ast"
)

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
