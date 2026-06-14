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
	// A tuple-subject match (`match (s, e) { (Idle, InsertCoin(n)) => … }`)
	// can't switch on a single tag — it lowers to a boolean decision tree
	// (`switch { case t0.Tag==X && t1.Tag==Y: … }`). Both statement- and
	// tail-position share this via emitArm. See §MatchIR (tuple
	// decision-tree).
	if tup, ok := m.Subject.(*ast.TupleLit); ok {
		return g.emitTupleMatchSwitch(m, tup, emitArm)
	}
	hasVariant, hasLiteral, hasPayloadBinding := false, false, false
	for _, arm := range m.Arms {
		v, l := g.classifyPattern(arm.Pattern)
		hasVariant = hasVariant || v
		hasLiteral = hasLiteral || l
		if vp, ok := arm.Pattern.(*ast.VariantPat); ok && len(vp.Sub) > 0 {
			hasPayloadBinding = true
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

// emitTupleMatchSwitch lowers a tuple-subject `match` to a Go
// `switch { case <conjunction>: … }` decision tree (lowering-go.md
// §MatchIR). Each tuple component is captured in a temp; each arm's
// tuple pattern contributes a per-component conjunct (variant tag /
// literal equality; a wildcard or fresh-ident binding contributes
// none) plus its bindings, then the arm body via emitArm — so
// statement- and tail-position reuse the one path. An arm whose
// components are all wildcard/fresh-ident has an empty conjunction and
// lowers to `default:`. Arm order is preserved, so Go's first-match
// semantics match Tide's.
func (g *gen) emitTupleMatchSwitch(m *ast.MatchExpr, tup *ast.TupleLit, emitArm func(arm *ast.MatchArm) error) error {
	g.line(m.Span.StartLine)
	compTypes := g.tupleCompTypes(tup)
	temps := make([]string, len(tup.Components))
	for j, comp := range tup.Components {
		tmp := g.nextMatchTemp()
		g.writeIndent()
		g.b.WriteString(tmp)
		g.b.WriteString(" := ")
		if err := g.emitExpr(comp); err != nil {
			return err
		}
		g.b.WriteByte('\n')
		temps[j] = tmp
	}
	g.writeIndent()
	g.b.WriteString("switch {\n")
	for _, arm := range m.Arms {
		tp, ok := arm.Pattern.(*ast.TuplePat)
		if !ok {
			return fmt.Errorf("codegen: tuple-subject match arm pattern is %T, expected a tuple pattern", arm.Pattern)
		}
		if len(tp.Sub) != len(temps) {
			return fmt.Errorf("codegen: tuple pattern has %d components, subject has %d", len(tp.Sub), len(temps))
		}
		conds, err := g.tupleArmConds(tp, temps, compTypes)
		if err != nil {
			return err
		}
		g.writeIndent()
		if len(conds) == 0 {
			g.b.WriteString("default:\n")
		} else {
			g.b.WriteString("case ")
			g.b.WriteString(strings.Join(conds, " && "))
			g.b.WriteString(":\n")
		}
		g.indent++
		if err := g.emitTupleArmBindings(tp, temps, compTypes); err != nil {
			return err
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

// tupleCompTypes returns the sum-type name of each tuple component
// (from sema's inferred type), or "" for a component whose type is not
// a named sum or is untyped. A component ident is a variant *reference*
// only when it names a variant of that component's own sum (owner ==
// compType) — without this scoping a fresh-binding ident that happens to
// collide with an unrelated sum's variant would be mis-lowered as a tag
// test (the recurring global-vs-scoped name footgun; AI.md §3.10/§3.13).
func (g *gen) tupleCompTypes(tup *ast.TupleLit) []string {
	names := make([]string, len(tup.Components))
	if g.info == nil {
		return names
	}
	for j, comp := range tup.Components {
		if n, ok := g.info.Type[comp].(*sema.Named); ok {
			names[j] = n.N
		}
	}
	return names
}

// identComponentVariant resolves a bare component ident to a variant of
// the component's own sum type (compType). When the component type is
// unknown (compType == "", e.g. sema info absent in a golden run) it
// falls back to the global variant table — preserving prior behaviour
// where no collision can be detected anyway.
func (g *gen) identComponentVariant(name, compType string) (variantInfo, bool) {
	info, ok := g.variant[name]
	if ok && (compType == "" || info.owner == compType) {
		return info, true
	}
	return variantInfo{}, false
}

// tupleArmConds builds the Go boolean conjuncts that select a tuple
// arm — one per component that discriminates (wildcard / fresh-ident
// components add none).
func (g *gen) tupleArmConds(tp *ast.TuplePat, temps, compTypes []string) ([]string, error) {
	var conds []string
	for j, sub := range tp.Sub {
		cond, err := g.compCond(sub, temps[j], compTypes[j])
		if err != nil {
			return nil, err
		}
		if cond != "" {
			conds = append(conds, cond)
		}
	}
	return conds, nil
}

// compCond renders the Go test a single tuple-component pattern imposes
// on its captured temp `t`: a variant tag check, a literal equality, or
// the empty string for a wildcard / fresh-ident binding (no test). A
// component shape that would need conditional sub-matching (a nested
// variant/literal *inside* a payload) is not reached here — payloads
// are bound, not tested — and an unrecognised shape errors.
func (g *gen) compCond(p ast.Pattern, t, compType string) (string, error) {
	switch pat := p.(type) {
	case *ast.WildcardPat:
		return "", nil
	case *ast.IdentPat:
		if info, ok := g.identComponentVariant(pat.Name, compType); ok {
			return fmt.Sprintf("%s.Tag == %d", t, info.tag), nil
		}
		return "", nil // fresh binding — no test
	case *ast.VariantPat:
		name := lastSeg(pat.QName)
		info, ok := g.variant[name]
		if !ok {
			return "", fmt.Errorf("codegen: variant pattern %s does not match any declared sum-type variant", name)
		}
		// A constructor must belong to the component's own sum, else
		// its tag would be tested against an unrelated type's value (a
		// silent miscompile). compType == "" (sema info absent) skips
		// the check. The proper home is a sema mismatched-constructor
		// diagnostic; until then codegen fails loudly rather than wrong.
		if compType != "" && info.owner != compType {
			return "", fmt.Errorf("codegen: constructor %s belongs to %s, not the matched component type %s — mismatched constructor in tuple pattern", name, info.owner, compType)
		}
		return fmt.Sprintf("%s.Tag == %d", t, info.tag), nil
	case *ast.IntLitPat:
		return fmt.Sprintf("%s == %d", t, pat.Value), nil
	case *ast.StringLitPat:
		return fmt.Sprintf("%s == %s", t, strconv.Quote(pat.Value)), nil
	case *ast.BoolLitPat:
		if pat.Value {
			return t, nil
		}
		return "!" + t, nil
	case *ast.RuneLitPat:
		return fmt.Sprintf("%s == %s", t, pat.RawText), nil
	}
	return "", fmt.Errorf("codegen: unsupported tuple-match component pattern %T", p)
}

// emitTupleArmBindings emits the binding statements a tuple arm
// introduces: a VariantPat component's payload (via emitPayloadBindings,
// shared with single-subject matches) and a fresh-ident component bound
// to its whole captured temp. Wildcards, nullary-variant idents, and
// literals bind nothing.
func (g *gen) emitTupleArmBindings(tp *ast.TuplePat, temps, compTypes []string) error {
	for j, sub := range tp.Sub {
		if err := g.bindCompPattern(sub, temps[j], compTypes[j]); err != nil {
			return err
		}
	}
	return nil
}

func (g *gen) bindCompPattern(p ast.Pattern, t, compType string) error {
	switch pat := p.(type) {
	case *ast.VariantPat:
		if len(pat.Sub) > 0 {
			return g.emitPayloadBindings(pat, t)
		}
		return nil
	case *ast.IdentPat:
		if _, ok := g.identComponentVariant(pat.Name, compType); ok {
			return nil // nullary-variant reference — binds nothing
		}
		return g.bindSubPattern(pat, t) // fresh binding: `name := t`
	}
	return nil // wildcard / literal — nothing to bind
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
	// UnreachableIR) unless an arm already produced a `default` — which
	// would make the guard unreachable code (a `go vet` failure).
	if !g.matchEmitsDefault(m) {
		g.writeIndent()
		g.b.WriteString("panic(\"unreachable: non-exhaustive match\")\n")
	}
	return nil
}

// matchEmitsDefault reports whether a match lowers a `default:` clause —
// a `_` arm, or (for a tuple match) an arm whose components are all
// wildcard / fresh-ident, so its conjunction is empty. A `default`
// makes the Go switch terminating on its own, so no trailing
// unreachable guard is emitted.
func (g *gen) matchEmitsDefault(m *ast.MatchExpr) bool {
	var compTypes []string
	if tup, ok := m.Subject.(*ast.TupleLit); ok {
		compTypes = g.tupleCompTypes(tup)
	}
	for _, arm := range m.Arms {
		switch p := arm.Pattern.(type) {
		case *ast.WildcardPat:
			return true
		case *ast.TuplePat:
			if g.tupleArmIsCatchAll(p, compTypes) {
				return true
			}
		}
	}
	return false
}

// tupleArmIsCatchAll reports whether every component of a tuple arm is
// a wildcard or a fresh-ident binding — i.e. it imposes no test and
// lowers to `default:`. A variant-named ident (scoped to the component's
// own sum) or any refining pattern makes it conditional.
func (g *gen) tupleArmIsCatchAll(tp *ast.TuplePat, compTypes []string) bool {
	for j, sub := range tp.Sub {
		compType := ""
		if j < len(compTypes) {
			compType = compTypes[j]
		}
		switch s := sub.(type) {
		case *ast.WildcardPat:
			// imposes no test
		case *ast.IdentPat:
			if _, ok := g.identComponentVariant(s.Name, compType); ok {
				return false // nullary-variant ref → a tag test
			}
		default:
			return false
		}
	}
	return true
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
		v, l := g.classifyPattern(arm.Pattern)
		hasVariant = hasVariant || v
		hasLiteral = hasLiteral || l
		if vp, ok := arm.Pattern.(*ast.VariantPat); ok && len(vp.Sub) > 0 {
			hasPayloadBinding = true
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

// emitPayloadBindings binds each sub-pattern of a VariantPat against
// its payload field `<subject>.<PayloadField>`, delegating the
// per-sub-pattern shape (Ident / Wildcard / nested Tuple) to
// bindSubPattern.
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
		// A self-referential payload field is stored as a pointer
		// (§Recursive sum types); deref it so the binding has the
		// sum's value type, not `*T`.
		deref := ""
		if isSelfRefField(info.fields[i], info.owner) {
			deref = "*"
		}
		// Predeclared sums use spec-fixed field names (V / E)
		// per `lang-spec/lowering-go.md` §Container types;
		// user-declared variants follow the PR-F5a
		// `<Variant><FieldName>` convention.
		var fieldName string
		if pf := predeclaredPayloadField(name); pf != "" {
			fieldName = pf
		} else {
			fieldName = payloadFieldName(name, info.fields[i].Name)
		}
		fieldExpr := deref + subjectExpr + "." + fieldName
		if err := g.bindSubPattern(sub, fieldExpr); err != nil {
			return err
		}
	}
	return nil
}

// bindSubPattern writes the binding lines destructuring `valueExpr`
// (the Go expression yielding the matched value) according to sub.
// IdentPat binds the whole value; WildcardPat binds nothing; a
// TuplePat destructures positionally (`valueExpr._0`, `._1`, …),
// recursing on each component — this is how a tuple nested in a
// variant payload (`Some((i, j))`) binds its parts. A component that
// is itself a literal/variant pattern would need conditional matching
// and is unsupported in payload position in v1.
func (g *gen) bindSubPattern(sub ast.Pattern, valueExpr string) error {
	switch sp := sub.(type) {
	case *ast.IdentPat:
		g.writeIndent()
		g.b.WriteString(goIdent(sp.Name))
		g.b.WriteString(" := ")
		g.b.WriteString(valueExpr)
		g.b.WriteByte('\n')
	case *ast.WildcardPat:
		// Nothing to bind.
	case *ast.TuplePat:
		for i, comp := range sp.Sub {
			if err := g.bindSubPattern(comp, fmt.Sprintf("%s._%d", valueExpr, i)); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("codegen: nested sub-pattern %T in variant payload not supported in v1", sub)
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

// classifyPattern reports whether a pattern (or any AltPat atom)
// switches on the variant tag or on the subject value — the choice
// of the two Go `switch` forms (lowering-go.md §MatchIR). A pattern
// may be neither (e.g. WildcardPat → `default:`, valid in both).
func (g *gen) classifyPattern(p ast.Pattern) (variant, literal bool) {
	switch pat := p.(type) {
	case *ast.VariantPat:
		variant = true
	case *ast.IdentPat:
		if _, ok := g.variant[pat.Name]; ok {
			variant = true
		}
	case *ast.IntLitPat, *ast.StringLitPat, *ast.BoolLitPat, *ast.RuneLitPat:
		literal = true
	case *ast.AltPat:
		for _, a := range pat.Atoms {
			v, l := g.classifyPattern(a)
			variant = variant || v
			literal = literal || l
		}
	}
	return
}

// emitMatchArmHeader writes the case clause: `default` for a
// wildcard, else `case v` (or `case v1, v2, …` for an AltPat —
// Go's comma-listed case values are exactly alternation).
func (g *gen) emitMatchArmHeader(p ast.Pattern) error {
	if _, ok := p.(*ast.WildcardPat); ok {
		g.b.WriteString("default")
		return nil
	}
	g.b.WriteString("case ")
	if alt, ok := p.(*ast.AltPat); ok {
		for i, atom := range alt.Atoms {
			if i > 0 {
				g.b.WriteString(", ")
			}
			v, err := g.caseValue(atom)
			if err != nil {
				return err
			}
			g.b.WriteString(v)
		}
		return nil
	}
	v, err := g.caseValue(p)
	if err != nil {
		return err
	}
	g.b.WriteString(v)
	return nil
}

// caseValue renders the Go `case`-value text for a single (non-alt,
// non-wildcard) pattern: a literal in its Go spelling, or a variant's
// integer tag. Payload sub-patterns on a VariantPat are bound
// separately (emitPayloadBindings), so only the tag matters here.
func (g *gen) caseValue(p ast.Pattern) (string, error) {
	switch pat := p.(type) {
	case *ast.IntLitPat:
		return strconv.FormatInt(pat.Value, 10), nil
	case *ast.StringLitPat:
		return strconv.Quote(pat.Value), nil
	case *ast.BoolLitPat:
		if pat.Value {
			return "true", nil
		}
		return "false", nil
	case *ast.RuneLitPat:
		// Re-emit the source spelling; Go accepts `'a'` for its rune
		// (int32) type, matching RuneLitExpr lowering.
		return pat.RawText, nil
	case *ast.VariantPat:
		info, ok := g.variant[lastSeg(pat.QName)]
		if !ok {
			return "", fmt.Errorf("codegen: variant pattern %s does not match any declared sum-type variant", lastSeg(pat.QName))
		}
		return strconv.Itoa(info.tag), nil
	case *ast.IdentPat:
		if info, ok := g.variant[pat.Name]; ok {
			return strconv.Itoa(info.tag), nil
		}
		return "", fmt.Errorf("codegen: IdentPat %q in match arm is a fresh binding — only variant patterns supported in PR-F2", pat.Name)
	}
	return "", fmt.Errorf("codegen: unsupported pattern %T", p)
}

// emitMatchArmBody emits the arm body as a Go statement. The
// arm body in source is an Expr; we wrap it in a synthetic
// ExprStmt so the existing statement-lowering paths work. A
// ReturnExpr arm body lowers to a `return` statement as usual.
func (g *gen) emitMatchArmBody(body ast.Expr, _ ast.Span) error {
	return g.emitStmt(&ast.ExprStmt{Span: body.NodeSpan(), Expr: body})
}
