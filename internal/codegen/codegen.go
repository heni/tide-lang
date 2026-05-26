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
	g := &gen{file: file, variant: map[string]variantInfo{}}
	// First pass — register sum-type variants so later
	// expression / pattern lowering can qualify Variant idents
	// to their Go-side constants and tag numbers.
	for _, d := range f.Decls {
		if td, ok := d.(*ast.TypeDecl); ok {
			if sb, ok := td.Body.(*ast.SumTypeBody); ok {
				for i, v := range sb.Variants {
					g.variant[v.Name] = variantInfo{owner: td.Name, tag: i}
				}
			}
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
}

type variantInfo struct {
	owner string // owning sum-type name (e.g. "Color")
	tag   int    // declaration order, used for the Tag field
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
		// Verify nullary-only — payload variants are PR-F3.
		for _, v := range body.Variants {
			if len(v.Fields) > 0 {
				return fmt.Errorf("codegen: variant %s.%s has payload — payload variants land in a later PR",
					td.Name, v.Name)
			}
		}
		// Lower to a tagged struct, matching the Option / Result
		// shape from lowering-go.md §Container types. Nullary
		// variants are constants of the struct type; their tag
		// is the declaration order (§Variant-tag numbering).
		g.line(td.Span.StartLine)
		g.b.WriteString("type ")
		g.b.WriteString(goIdent(td.Name))
		g.b.WriteString(" struct {\n\tTag uint8\n}\n")
		g.b.WriteString("var (\n")
		for i, v := range body.Variants {
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
		return nil
	}
	return fmt.Errorf("codegen: unhandled TypeBody %T", td.Body)
}

func (g *gen) emitFuncDecl(fn *ast.FuncDecl) error {
	g.line(fn.Span.StartLine)
	g.b.WriteString("func ")
	g.b.WriteString(goIdent(fn.Name))
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
	case *ast.NamedType:
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
	hasVariant, hasLiteral := false, false
	for _, arm := range m.Arms {
		switch p := arm.Pattern.(type) {
		case *ast.VariantPat:
			hasVariant = true
			_ = p
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
	g.writeIndent()
	g.b.WriteString("switch ")
	if err := g.emitExpr(m.Subject); err != nil {
		return err
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
		if err := g.emitMatchArmBody(arm.Body, arm.Span); err != nil {
			return err
		}
		g.indent--
	}
	g.writeIndent()
	g.b.WriteString("}\n")
	return nil
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
		// PR-F2 only handles nullary variants; payload
		// sub-patterns land with PR-F3.
		if len(pat.Sub) > 0 {
			return fmt.Errorf("codegen: payload variant pattern %s(...) not yet supported", lastSeg(pat.QName))
		}
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
		return fmt.Errorf("codegen: only RangeExpr iterables in PR-C, got %T", s.Iterable)
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
	if err := g.emitExpr(c.Callee); err != nil {
		return err
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
