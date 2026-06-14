package codegen

import (
	"fmt"
	"strconv"

	"github.com/heni/tide-lang/internal/ast"
)

// try.go — `try` lowering. A statement-position `try e` becomes an
// unwrap temp + early-return-on-bail preamble (emitTryPreamble); a
// `try` nested in an expression is pre-emitted as such a preamble and
// substituted with `<tmp>.V` (hoistExprTries), but only when lifting it
// preserves evaluation order (hoistableTries / hoistTriesIfSafe).
// Otherwise it falls to tryExprErr — a graceful limitation, never a
// miscompile. See lang-spec/desugaring.md §T-Try and lowering-go.md
// §try lowering. Extracted from codegen.go (behaviour-preserving).

// hoistableTries reports whether every `try` reachable by hoistExprTries
// in exprs (evaluated left-to-right) can be lifted to a statement
// preamble *without reordering side effects*. A try is unsafe to hoist
// when an impure non-try expression is evaluated before it in the same
// frame: hoisting moves the try's early-return ahead of that expression,
// so the effect would be deferred past the try — or skipped entirely
// when the try bails. (Two adjacent tries are fine: both move out, in
// order.) When unsafe the caller leaves the try to tryExprErr — a
// graceful limitation, never a miscompile. Conservative by construction:
// any expression form not known-pure counts as impure — including
// panic-points (index out-of-range, division by zero), since a panic is
// an observable effect whose order must be preserved (lowering-go.md
// §try lowering).
func hoistableTries(exprs ...ast.Expr) bool {
	impure := false
	ok := true
	var walk func(e ast.Expr)
	walk = func(e ast.Expr) {
		if !ok {
			return
		}
		switch v := e.(type) {
		case *ast.Ident, *ast.IntLitExpr, *ast.FloatLitExpr,
			*ast.StringLitExpr, *ast.BoolLitExpr, *ast.RuneLitExpr,
			*ast.UnitLit:
			// pure leaves — no effect, no nested try
		case *ast.TryExpr:
			if impure {
				ok = false
				return
			}
			// The inner is its own hoisted unit: its impures precede the
			// preamble, so they don't reorder against outer siblings.
			// Analyse it in a fresh impure scope, then restore.
			saved := impure
			impure = false
			walk(v.Inner)
			impure = saved
		case *ast.Call:
			walk(v.Callee)
			for _, a := range v.Args {
				walk(a)
			}
			impure = true // the call itself is a side effect, in place
		case *ast.Field:
			walk(v.Receiver)
		case *ast.TupleField:
			walk(v.Receiver)
		case *ast.Binary:
			walk(v.Left)
			if v.Op != "&&" && v.Op != "||" {
				walk(v.Right)
			}
			if v.Op == "/" || v.Op == "%" {
				impure = true // division by zero panics — an ordered effect
			}
		case *ast.Unary:
			walk(v.Operand)
		case *ast.Index:
			walk(v.Receiver)
			walk(v.Idx)
			impure = true // out-of-range index panics — an ordered effect
		case *ast.ParenExpr:
			walk(v.Inner)
		case *ast.TupleLit:
			for _, c := range v.Components {
				walk(c)
			}
		case *ast.SliceLit:
			for _, it := range v.Items {
				walk(it)
			}
		default:
			// Unknown / frame-introducing / effectful container
			// (brace literal, value match/if, closure, scope/spawn, …):
			// treat as impure so a following try is not reordered past it.
			impure = true
		}
	}
	for _, e := range exprs {
		walk(e)
	}
	return ok
}

// hoistTriesIfSafe hoists the tries in exprs (in order) only when doing
// so preserves evaluation order (hoistableTries); otherwise it leaves
// them in place to reach tryExprErr. The exprs are the statement's
// sub-expressions in evaluation order (e.g. an assignment's LValue then
// Value) so the order check spans them as one frame.
func (g *gen) hoistTriesIfSafe(exprs ...ast.Expr) error {
	if !hoistableTries(exprs...) {
		return nil
	}
	for _, e := range exprs {
		if err := g.hoistExprTries(e); err != nil {
			return err
		}
	}
	return nil
}

// hoistExprTries pre-emits, as statement preambles, every `try`
// nested inside an expression that is not itself a statement-position
// `try` (those are handled directly by emitStmt / emitLetOrVar). Each
// hoisted try's temp name is recorded in g.tryHoist; emitExpr then
// substitutes `<tmp>.V` at the node's original position. This realises
// `try` in expression position — `f(try g())`, `a + try b()` — via the
// block-expr lowering (desugaring.md §T-Try): the early-return preamble
// runs before the surrounding expression, in source order.
//
// The walk stops at any construct that introduces a *new return frame*
// (closures, value-position match/if/block, scope/spawn) — a `try`
// there belongs to that frame and is emitted when its body is, so
// descending would attach the early-return to the wrong function. The
// right operand of `&&` / `||` is also not descended: it evaluates
// conditionally, so hoisting its preamble unconditionally would change
// short-circuit semantics (a `try` there still reaches tryExprErr).
func (g *gen) hoistExprTries(e ast.Expr) error {
	switch v := e.(type) {
	case *ast.TryExpr:
		// emitTryPreamble hoists this try's own inner tries (innermost
		// first), so nested `try f(try g())` works for every caller.
		tmp, err := g.emitTryPreamble(v)
		if err != nil {
			return err
		}
		if g.tryHoist == nil {
			g.tryHoist = map[*ast.TryExpr]string{}
		}
		g.tryHoist[v] = tmp
	case *ast.Call:
		if err := g.hoistExprTries(v.Callee); err != nil {
			return err
		}
		for _, a := range v.Args {
			if err := g.hoistExprTries(a); err != nil {
				return err
			}
		}
	case *ast.Field:
		return g.hoistExprTries(v.Receiver)
	case *ast.TupleField:
		return g.hoistExprTries(v.Receiver)
	case *ast.Binary:
		if err := g.hoistExprTries(v.Left); err != nil {
			return err
		}
		if v.Op != "&&" && v.Op != "||" {
			return g.hoistExprTries(v.Right)
		}
	case *ast.Unary:
		return g.hoistExprTries(v.Operand)
	case *ast.Index:
		if err := g.hoistExprTries(v.Receiver); err != nil {
			return err
		}
		return g.hoistExprTries(v.Idx)
	case *ast.ParenExpr:
		return g.hoistExprTries(v.Inner)
	case *ast.TupleLit:
		for _, c := range v.Components {
			if err := g.hoistExprTries(c); err != nil {
				return err
			}
		}
	case *ast.SliceLit:
		for _, it := range v.Items {
			if err := g.hoistExprTries(it); err != nil {
				return err
			}
		}
	}
	return nil
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
	// A `try` nested in this try's inner expr (`try f(try g())`) is
	// hoisted first, so its early-return preamble precedes the
	// `tmp := <inner>` line below — when reordering is safe.
	if err := g.hoistTriesIfSafe(t.Inner); err != nil {
		return "", err
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
	return fmt.Errorf("codegen: `try` in this expression position is not supported — it sits in a conditionally-evaluated operand (`&&`/`||` right side), a separate return frame (closure / value-position match/if / scope / spawn), or after another side-effecting expression in the same statement; lift it to a `let`/`var`/`return` binding")
}
