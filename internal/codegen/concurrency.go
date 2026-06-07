package codegen

import (
	"fmt"

	"github.com/heni/tide-lang/internal/ast"
)

// emitScopeExpr lowers a `scope<T, E>(parent?) { body }` expression
// to an immediately-invoked func returning `Result[T, error]`
// (lowering-go.md §ScopeIR). A fresh group is derived from the
// parent context (or context.Background()); the body runs, each
// `spawn` registering on the group; the group is joined; the first
// spawned error becomes Err, otherwise the body's trailing value
// (or unit) is wrapped Ok.
//
// v1 fixes E = error (sema E0407), so the Err arm needs no type
// assertion — the group's `error` flows straight through.
func (g *gen) emitScopeExpr(s *ast.ScopeExpr) error {
	var tArg ast.TypeExpr // success type T; nil ⇒ unit
	if len(s.TypeArgs) >= 1 {
		tArg = s.TypeArgs[0]
	}

	depth := len(g.groupVars)
	gv := fmt.Sprintf("_tideEg%d", depth)
	g.groupVars = append(g.groupVars, gv)
	defer func() { g.groupVars = g.groupVars[:depth] }()

	// A scope is its own return frame: a `return` inside the scope's
	// IIFE is not a spawn return, even when the scope is lexically
	// nested inside a spawn body. Drop the spawn-return conversion
	// for the duration of the scope body.
	savedSpawn := g.inSpawnBody
	g.inSpawnBody = false
	defer func() { g.inSpawnBody = savedSpawn }()

	g.b.WriteString("func() Result[")
	if err := g.emitScopeTType(tArg); err != nil {
		return err
	}
	g.b.WriteString(", error] {\n")
	g.indent++

	// Group + derived context. The context binding is discarded
	// until ScopeRef (`scope.context`) lands; the group itself is
	// used by every spawn and by Wait below.
	g.writeIndent()
	g.b.WriteString(gv)
	g.b.WriteString(", _ := tideNewGroup(")
	if s.Parent != nil {
		if err := g.emitExpr(s.Parent); err != nil {
			return err
		}
	} else {
		g.b.WriteString("context.Background()")
	}
	g.b.WriteString(")\n")

	for _, st := range s.Body.Stmts {
		if err := g.emitStmt(st); err != nil {
			return err
		}
	}

	// Join: the first spawned error (if any) becomes Err.
	g.writeIndent()
	g.b.WriteString("if _err := ")
	g.b.WriteString(gv)
	g.b.WriteString(".Wait(); _err != nil {\n")
	g.indent++
	g.writeIndent()
	g.b.WriteString("return ResultErr[")
	if err := g.emitScopeTType(tArg); err != nil {
		return err
	}
	g.b.WriteString(", error](_err)\n")
	g.indent--
	g.writeIndent()
	g.b.WriteString("}\n")

	// Success: wrap the trailing value (or unit) Ok.
	g.writeIndent()
	g.b.WriteString("return ResultOk[")
	if err := g.emitScopeTType(tArg); err != nil {
		return err
	}
	g.b.WriteString(", error](")
	if s.Body.Trailing != nil {
		if err := g.emitExpr(s.Body.Trailing); err != nil {
			return err
		}
	} else {
		g.b.WriteString("struct{}{}")
	}
	g.b.WriteString(")\n")

	g.indent--
	g.writeIndent()
	g.b.WriteString("}()")
	return nil
}

// emitScopeTType renders the scope's success type T, defaulting to
// Go's zero-byte `struct{}` when absent (T = unit).
func (g *gen) emitScopeTType(t ast.TypeExpr) error {
	if t == nil {
		g.b.WriteString("struct{}")
		return nil
	}
	return g.emitTypeExpr(t)
}

// emitSpawnStmt lowers `spawn { body }` to `<group>.Go(func() error
// { ... })` (lowering-go.md §SpawnIR). The body's `return Ok(())` /
// `return Err(e)` convert to the func's `error` return via
// emitSpawnReturn (driven by g.inSpawnBody); a body that falls
// through with no return gets a `return nil`.
func (g *gen) emitSpawnStmt(s *ast.SpawnExpr) error {
	gv := "_tideEg0"
	if n := len(g.groupVars); n > 0 {
		gv = g.groupVars[n-1]
	}
	g.line(s.Span.StartLine)
	g.writeIndent()
	g.b.WriteString(gv)
	g.b.WriteString(".Go(func() error {\n")
	g.indent++

	saved := g.inSpawnBody
	g.inSpawnBody = true
	for _, st := range s.Body.Stmts {
		if err := g.emitStmt(st); err != nil {
			g.inSpawnBody = saved
			return err
		}
	}
	if !spawnBodyEndsInReturn(s.Body) {
		g.writeIndent()
		g.b.WriteString("return nil\n")
	}
	g.inSpawnBody = saved

	g.indent--
	g.writeIndent()
	g.b.WriteString("})\n")
	return nil
}

// emitSpawnReturn lowers a `return <Result<unit, E>>` inside a spawn
// body to the func's `error` return: `return Ok(_)` → `return nil`,
// `return Err(e)` → `return <e>`, any other Result expression →
// tag-branch on its `.E`.
func (g *gen) emitSpawnReturn(r *ast.ReturnExpr) error {
	g.line(r.Span.StartLine)
	g.writeIndent()
	if r.Value == nil {
		g.b.WriteString("return nil\n")
		return nil
	}
	if call, ok := r.Value.(*ast.Call); ok {
		if id, ok := call.Callee.(*ast.Ident); ok {
			if vi, isVar := g.variant[id.Name]; isVar && vi.owner == "Result" {
				if vi.tag == 0 { // Ok(_)
					g.b.WriteString("return nil\n")
					return nil
				}
				// Err(e) → return e
				g.b.WriteString("return ")
				if len(call.Args) == 1 {
					if err := g.emitExpr(call.Args[0]); err != nil {
						return err
					}
				} else {
					g.b.WriteString("nil")
				}
				g.b.WriteByte('\n')
				return nil
			}
		}
	}
	// General Result expression: branch on its tag.
	g.b.WriteString("if __sr := ")
	if err := g.emitExpr(r.Value); err != nil {
		return err
	}
	g.b.WriteString("; __sr.Tag == 1 {\n")
	g.indent++
	g.writeIndent()
	g.b.WriteString("return __sr.E\n")
	g.indent--
	g.writeIndent()
	g.b.WriteString("}\n")
	g.writeIndent()
	g.b.WriteString("return nil\n")
	return nil
}

// spawnBodyEndsInReturn reports whether the spawn body's last
// statement is a `return`, so emitSpawnStmt can skip the synthetic
// trailing `return nil`.
func spawnBodyEndsInReturn(b *ast.Block) bool {
	if b.Trailing != nil || len(b.Stmts) == 0 {
		return false
	}
	es, ok := b.Stmts[len(b.Stmts)-1].(*ast.ExprStmt)
	if !ok {
		return false
	}
	_, ok = es.Expr.(*ast.ReturnExpr)
	return ok
}
