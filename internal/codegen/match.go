package codegen

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/heni/tide-lang/internal/ast"
	"github.com/heni/tide-lang/internal/sema"
)

// match.go — value- and statement-position lowering for `match`,
// plus the shallow arm/branch result-type inference shared with the
// value-position `if`/block IIFEs (control_flow.go). Carved out of
// codegen.go (health-pass F3(1)) ahead of the implicit-tail-return /
// value-position-typing work, per lang-spec/lowering-go.md §MatchIR.

// emitMatchSwitch lowers a MatchExpr to a Go `switch` statement,
// emitting each arm body through the supplied `emitArm` callback —
// the one axis that differs between statement position (discard the
// arm value) and tail position (`return` it). Per lowering-go.md
// §MatchIR, the case head varies by pattern shape:
//   - VariantPat / IdentPat-bound-to-variant → `case <tag-int>:`
//     of `switch subject.Tag`.
//   - Literal patterns → `case <literal>:` of `switch subject`.
//   - WildcardPat → `default:`.
//
// PR-F2 uses one of the two switch forms based on whether the
// arm set is variant-based or literal-based; mixing is not
// reached by the corpus and rejected.
func (g *gen) emitMatchSwitch(m *ast.MatchExpr, emitArm func(arm *ast.MatchArm) error) error {
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
		if err := emitArm(arm); err != nil {
			return err
		}
		g.indent--
	}
	g.writeIndent()
	g.b.WriteString("}\n")
	return nil
}

// emitMatchAsStmt lowers a MatchExpr at statement position — each arm
// body runs as a statement, its value discarded.
func (g *gen) emitMatchAsStmt(m *ast.MatchExpr) error {
	return g.emitMatchSwitch(m, func(arm *ast.MatchArm) error {
		return g.emitMatchArmBody(arm.Body, arm.Span)
	})
}

// emitMatchTail lowers a MatchExpr in tail position — the trailing
// expression of a value-returning body. It reuses the statement-form
// `switch` (so payload-binding arms, unsupported by the value-position
// IIFE per §"match in value position", lower correctly) but emits each
// arm body via emitTailReturn, so the implicit `return` is distributed
// into the arms. g.expectType (the function's declared return type) is
// re-established for each arm — the subject's own emission clears it —
// so leaf Result/Option constructors get explicit type args stamped.
func (g *gen) emitMatchTail(m *ast.MatchExpr) error {
	expect := g.expectType
	if err := g.emitMatchSwitch(m, func(arm *ast.MatchArm) error {
		g.expectType = expect
		return g.emitTailReturn(arm.Body)
	}); err != nil {
		return err
	}
	// A Go `switch` without a `default` is not a terminating statement
	// even when the Tide match is exhaustive (sema-guaranteed), so a
	// value-returning function would trip Go's "missing return". Emit an
	// unreachable guard after the switch (lowering-go.md §MatchIR
	// UnreachableIR) unless a wildcard arm already produced a `default`.
	if !hasWildcardArm(m) {
		g.writeIndent()
		g.b.WriteString("panic(\"unreachable: non-exhaustive match\")\n")
	}
	return nil
}

// hasWildcardArm reports whether a match has a `_` arm — which lowers
// to a Go `default:`, making the switch terminating on its own.
func hasWildcardArm(m *ast.MatchExpr) bool {
	for _, arm := range m.Arms {
		if _, ok := arm.Pattern.(*ast.WildcardPat); ok {
			return true
		}
	}
	return false
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
	case *ast.FloatLitExpr:
		return "float64", nil
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
// shapes a value-position block / if / match / closure result can
// take. Tide primitives map 1:1 (lowering-go.md §Primitive type
// lowering); classes are reference types (`*T`); `unit` has no Go
// spelling. Shapes outside this set return false.
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
	case *sema.Result:
		tt, okT := g.goTypeFromSema(v.T)
		et, okE := g.goTypeFromSema(v.E)
		if okT && okE {
			return "Result[" + tt + ", " + et + "]", true
		}
	case *sema.Option:
		if tt, ok := g.goTypeFromSema(v.T); ok {
			return "Option[" + tt + "]", true
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
	case *sema.Func:
		// `func(A) R` — used for a closure that returns a closure.
		var sb strings.Builder
		sb.WriteString("func(")
		for i, p := range v.Params {
			pt, ok := g.goTypeFromSema(p)
			if !ok {
				return "", false
			}
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(pt)
		}
		sb.WriteByte(')')
		if _, isUnit := v.Return.(*sema.Unit); v.Return != nil && !isUnit {
			rt, ok := g.goTypeFromSema(v.Return)
			if !ok {
				return "", false
			}
			sb.WriteByte(' ')
			sb.WriteString(rt)
		}
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

// emitMatchArmBody emits the arm body as a Go statement. The
// arm body in source is an Expr; we wrap it in a synthetic
// ExprStmt so the existing statement-lowering paths work. A
// ReturnExpr arm body lowers to a `return` statement as usual.
func (g *gen) emitMatchArmBody(body ast.Expr, _ ast.Span) error {
	return g.emitStmt(&ast.ExprStmt{Span: body.NodeSpan(), Expr: body})
}
