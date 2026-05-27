package codegen

import (
	"fmt"
	"go/format"
	"strconv"
	"strings"

	"github.com/heni/tide-lang/internal/ast"
)

// Emit lowers the given Tide AST to a Go source string. The
// returned text is gofmt-stable (round-trips through gofmt -s).
// file is the source path embedded into //line directives;
// pass "" to suppress them.
func Emit(f *ast.File, file string) (string, error) {
	g := &gen{
		file:    file,
		variant: map[string]variantInfo{},
		class:   map[string]classInfo{},
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
}

type variantInfo struct {
	owner  string             // owning sum-type name (e.g. "Color")
	tag    int                // declaration order, used for the Tag field
	fields []*ast.FieldDecl   // payload fields, nil/empty for nullary variants
}

type classInfo struct {
	statics map[string]bool // names of `static` methods
	generic bool            // true iff the class has type parameters
}

func (g *gen) writeHeader(f *ast.File) {
	g.b.WriteString("package main\n\n")
	// PR-C bindings shortcut: every Tide import resolves to the
	// matching Go stdlib package by the same name. fmt → "fmt".
	// strconv → "strconv". etc. Sorted for determinism.
	if len(f.Imports) > 0 {
		paths := make([]string, len(f.Imports))
		for i, im := range f.Imports {
			paths[i] = im.Path
		}
		// Simple insertion sort (n is tiny).
		for i := 1; i < len(paths); i++ {
			for j := i; j > 0 && paths[j-1] > paths[j]; j-- {
				paths[j-1], paths[j] = paths[j], paths[j-1]
			}
		}
		if len(paths) == 1 {
			g.b.WriteString("import \"")
			g.b.WriteString(paths[0])
			g.b.WriteString("\"\n\n")
		} else {
			g.b.WriteString("import (\n")
			for _, p := range paths {
				g.b.WriteString("\t\"")
				g.b.WriteString(p)
				g.b.WriteString("\"\n")
			}
			g.b.WriteString(")\n\n")
		}
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
	if err := g.emitBlockBody(m.Body); err != nil {
		return err
	}
	g.indent--
	g.b.WriteString("}\n")
	return nil
}

// emitTypeParamBrackets writes a Go-style type-parameter
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
	if err := g.emitBlockBody(fn.Body); err != nil {
		return err
	}
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
		// PR-C: trailing-expression block (used by IfExpr / ScopeExpr)
		// isn't reached for hello/fizzbuzz. Reserve.
		return fmt.Errorf("codegen: trailing-expression block not supported in PR-C")
	}
	return nil
}

func (g *gen) emitStmt(s ast.Stmt) error {
	switch v := s.(type) {
	case *ast.ExprStmt:
		// ReturnExpr (DivergingExpr): lower to Go `return` stmt.
		if r, ok := v.Expr.(*ast.ReturnExpr); ok {
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
		// MatchExpr: lower to Go `switch` statement.
		if m, ok := v.Expr.(*ast.MatchExpr); ok {
			return g.emitMatchAsStmt(m)
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
			g.b.WriteString(payloadFieldName(name, info.fields[i].Name))
			g.b.WriteByte('\n')
		case *ast.WildcardPat:
			// Nothing to bind.
		default:
			return fmt.Errorf("codegen: nested sub-pattern %T in variant payload not supported in v1", sub)
		}
	}
	return nil
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

func (g *gen) emitIfStmt(s *ast.IfStmt) error {
	g.line(s.Span.StartLine)
	g.writeIndent()
	g.b.WriteString("if ")
	if err := g.emitExpr(s.Cond); err != nil {
		return err
	}
	g.b.WriteString(" {\n")
	g.indent++
	if err := g.emitBlockBody(s.ThenBlock); err != nil {
		return err
	}
	g.indent--
	switch e := s.Else.(type) {
	case nil:
		g.writeIndent()
		g.b.WriteString("}\n")
	case *ast.IfStmt:
		g.writeIndent()
		g.b.WriteString("} else ")
		// emit the nested IfStmt without re-indenting the `if`.
		if err := g.emitElseIf(e); err != nil {
			return err
		}
	case *ast.Block:
		g.writeIndent()
		g.b.WriteString("} else {\n")
		g.indent++
		if err := g.emitBlockBody(e); err != nil {
			return err
		}
		g.indent--
		g.writeIndent()
		g.b.WriteString("}\n")
	default:
		return fmt.Errorf("codegen: unexpected else branch %T", s.Else)
	}
	return nil
}

// emitElseIf emits an IfStmt as the continuation of `} else `.
// It does NOT write a leading newline or indent — the caller has
// already emitted those.
func (g *gen) emitElseIf(s *ast.IfStmt) error {
	// //line directive maps the nested if's condition back to the
	// source position the developer typed `else if` on, not the
	// outer if's line. lowering-go.md §Source maps requires the
	// directive at every statement boundary.
	g.line(s.Span.StartLine)
	g.b.WriteString("if ")
	if err := g.emitExpr(s.Cond); err != nil {
		return err
	}
	g.b.WriteString(" {\n")
	g.indent++
	if err := g.emitBlockBody(s.ThenBlock); err != nil {
		return err
	}
	g.indent--
	switch e := s.Else.(type) {
	case nil:
		g.writeIndent()
		g.b.WriteString("}\n")
	case *ast.IfStmt:
		g.writeIndent()
		g.b.WriteString("} else ")
		return g.emitElseIf(e)
	case *ast.Block:
		g.writeIndent()
		g.b.WriteString("} else {\n")
		g.indent++
		if err := g.emitBlockBody(e); err != nil {
			return err
		}
		g.indent--
		g.writeIndent()
		g.b.WriteString("}\n")
	}
	return nil
}

func (g *gen) emitForStmt(s *ast.ForStmt) error {
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
		// per builtins.md §IterElem. PR-F3 supports slice
		// iteration (`for x in xs` over `[]T`). Maps / sets /
		// channels land in later PRs.
		iterExpr, ok := iter.(ast.Expr)
		if !ok {
			return fmt.Errorf("codegen: unsupported iterable %T", iter)
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
		// PR-F2 only supports match in statement position; the
		// statement emitter for ExprStmt handles the wrap and
		// arm-body emission. Reaching MatchExpr in pure
		// expression position is not supported yet.
		return fmt.Errorf("codegen: match expression in value position not yet supported")
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
	if f, ok := c.Callee.(*ast.Field); ok && !isStdlibNamespace(f.Receiver) {
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

// isStdlibNamespace reports whether expr is an Ident whose name
// is in the hardcoded stdlib binding registry. Used by emitCall
// to keep `fmt.println` from being interpreted as a slice
// method call.
func isStdlibNamespace(e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	if !ok {
		return false
	}
	switch id.Name {
	case "fmt", "os", "strings", "strconv", "bufio", "context",
		"time", "sync", "io", "log", "net", "encoding", "math":
		return true
	}
	return false
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
	}
	return goIdent(name)
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
