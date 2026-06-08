package codegen

import (
	"fmt"

	"github.com/heni/tide-lang/internal/ast"
)

// tail.go — implicit tail-expression return. A function/method/closure
// body whose declared result is a value yields that value through its
// trailing expression (the block-as-expression value rule); codegen
// lowers the trailing expression in *tail position*, distributing the
// implicit `return` into the leaves of a match/if/block rather than
// discarding it. See lang-spec/lowering-go.md §"Implicit tail return".
//
// Tail position is preferred over the value-position IIFE
// (emitMatchAsExpr / emitIfExprAsValue) for a body trailing because:
//   - a payload-binding match (`Ok(n) => …`) is unsupported by the
//     IIFE form but lowers cleanly as a statement `switch`;
//   - it avoids wrapping the whole body in `func() T { … }()`;
//   - g.expectType (the declared return type) stays in scope down to
//     each leaf, so a leaf Result/Option constructor gets explicit Go
//     type args stamped (§"Constructor type-argument stamping").

// emitTailReturn lowers e as the value of an implicit tail return.
// g.expectType is the enclosing function's declared return type on
// entry. A diverging trailing (return / break / continue / os.exit)
// already terminates control, so it is emitted as a bare statement
// with no `return` wrapper.
func (g *gen) emitTailReturn(e ast.Expr) error {
	switch v := e.(type) {
	case *ast.ParenExpr:
		return g.emitTailReturn(v.Inner)
	case *ast.MatchExpr:
		return g.emitMatchTail(v)
	case *ast.IfExpr:
		return g.emitIfExprTail(v)
	case *ast.Block:
		return g.emitBlockTail(v)
	}
	if isDivergingExpr(e) {
		return g.emitStmt(&ast.ExprStmt{Span: e.NodeSpan(), Expr: e})
	}
	g.line(e.NodeSpan().StartLine)
	g.writeIndent()
	g.b.WriteString("return ")
	if err := g.emitExpr(e); err != nil {
		return err
	}
	g.b.WriteByte('\n')
	return nil
}

// emitBlockTail emits a block in tail position: its statements, then
// its trailing expression in tail position. expectType is captured
// before the statements (which may clear it) and re-established for the
// trailing leaf. A trailing-less block in a value-returning position is
// a sema error; codegen guards it defensively.
func (g *gen) emitBlockTail(b *ast.Block) error {
	expect := g.expectType
	for _, s := range b.Stmts {
		if err := g.emitStmt(s); err != nil {
			return err
		}
	}
	if b.Trailing == nil {
		return fmt.Errorf("codegen: value-returning block needs a trailing expression")
	}
	g.expectType = expect
	return g.emitTailReturn(b.Trailing)
}

// emitIfExprTail lowers an IfExpr in tail position — each branch block
// emits its trailing in tail position (`return`-distributed). Both
// arms are required (requireElse): an else-less value `if` would fall
// through with no return. expectType is threaded per branch.
func (g *gen) emitIfExprTail(e *ast.IfExpr) error {
	expect := g.expectType
	return g.emitIf(ifExprShape(e), false, func(b *ast.Block) error {
		g.expectType = expect
		return g.emitBlockTail(b)
	}, asIfExprShape, true)
}
