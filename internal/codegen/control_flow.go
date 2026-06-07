package codegen

import (
	"fmt"

	"github.com/heni/tide-lang/internal/ast"
	"github.com/heni/tide-lang/internal/sema"
)

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

// emitClosure lowers a closure literal to a Go func literal
// `func(p T, …) R { … }`. Go captures the surrounding scope
// automatically. Parameters must be typed (the short form's omitted
// types need call-site context, which v1 codegen lacks). The return
// type comes from the annotation, else sema's inferred Func.Return.
func (g *gen) emitClosure(cl *ast.ClosureLit) error {
	g.b.WriteString("func(")
	for i, prm := range cl.Params {
		if i > 0 {
			g.b.WriteString(", ")
		}
		if prm.DeclType == nil {
			return fmt.Errorf("codegen: untyped closure parameter %q — annotate it (`(%s: T) => …`)", prm.Name, prm.Name)
		}
		g.b.WriteString(goIdent(prm.Name))
		g.b.WriteByte(' ')
		if err := g.emitTypeExpr(prm.DeclType); err != nil {
			return err
		}
	}
	g.b.WriteByte(')')
	// Return type: explicit annotation (emitted inline); else read it
	// from sema's inferred Func. Three outcomes: a rendered Go type
	// (emit `return val`), a unit return (no type, body value is a
	// discarded statement), or unknown (only an error for the short
	// form, which always yields a value).
	hasReturn, isUnit := false, false
	if cl.ReturnType != nil {
		g.b.WriteByte(' ')
		if err := g.emitTypeExpr(cl.ReturnType); err != nil {
			return err
		}
		hasReturn = true
	} else if g.info != nil {
		if fn, ok := g.info.Type[cl].(*sema.Func); ok && fn.Return != nil {
			if _, u := fn.Return.(*sema.Unit); u {
				isUnit = true
			} else if s, ok := g.goTypeFromSema(fn.Return); ok {
				g.b.WriteByte(' ')
				g.b.WriteString(s)
				hasReturn = true
			}
		}
	}
	if cl.Short && !hasReturn && !isUnit {
		return fmt.Errorf("codegen: cannot infer closure result type — annotate the return (`(…): R => …`)")
	}
	g.b.WriteString(" {\n")
	g.indent++
	if cl.Short {
		// Short form: the trailing value is the result — `return` it
		// unless the closure is unit-typed (then it's a bare stmt).
		if cl.Body.Trailing != nil {
			g.writeIndent()
			if !isUnit {
				g.b.WriteString("return ")
			}
			if err := g.emitExpr(cl.Body.Trailing); err != nil {
				return err
			}
			g.b.WriteByte('\n')
		}
	} else if err := g.emitBlockBody(cl.Body); err != nil {
		return err
	}
	g.indent--
	g.writeIndent()
	g.b.WriteString("}")
	return nil
}
