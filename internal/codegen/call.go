package codegen

// call.go — call-expression lowering: the emitCall dispatch and its
// stdlib-binding / container / channel / slice-method helpers. Split
// out of codegen.go (god-file health pass) so the stdlib-bindings
// epoch finds all call dispatch in one place. Pure same-package move
// — generated Go is byte-identical.

import (
	"fmt"

	"github.com/heni/tide-lang/internal/ast"
	"github.com/heni/tide-lang/internal/sema"
)

func (g *gen) emitCall(c *ast.Call) error {
	// Capture any expected-type context (set at return / typed-binding
	// position) and clear it immediately so nested argument expressions
	// don't inherit it — only this call's own callee may consume it.
	expect := g.expectType
	g.expectType = nil
	// reflect.* dispatch — lower to inline tidert helpers per
	// `lang-spec/builtins.md` §reflect. Codegen owns reflect's
	// surface (it's runtime-supplied, not a Go-stdlib binding).
	if f, ok := c.Callee.(*ast.Field); ok {
		if recvID, ok := f.Receiver.(*ast.Ident); ok && recvID.Name == "reflect" {
			return g.emitReflectCall(f.Name, c.TypeArgs, c.Args)
		}
	}
	// fmt.scan<T>() — stdin binding. Lowers to the tideScan helper,
	// which wraps Go's pointer-mutation `fmt.Scan(&v)` into the
	// Result<T, error> return form (binding-surface.md §fmt).
	if isFmtScan(c.Callee) {
		g.b.WriteString("tideScan")
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
		g.b.WriteString("()")
		return nil
	}
	// Result-wrapping stdlib binding — `pkg.method(args)` whose Go
	// referent returns `(T, error)`, lowered to
	// `tideResultOf(pkg.GoName(args))` (bindings.go). Go spreads the
	// referent's two-value return across the helper's two params and
	// infers T, folding it into the predeclared Result<T, error>.
	if f, ok := c.Callee.(*ast.Field); ok {
		if recv, ok := f.Receiver.(*ast.Ident); ok {
			if goName, ok := stdlibResultWrapOf(recv.Name, f.Name); ok {
				g.usesResultOf = true
				g.usesResult = true
				g.b.WriteString("tideResultOf(")
				g.b.WriteString(recv.Name)
				g.b.WriteByte('.')
				g.b.WriteString(goName)
				g.b.WriteByte('(')
				for i, a := range c.Args {
					if i > 0 {
						g.b.WriteString(", ")
					}
					if err := g.emitExpr(a); err != nil {
						return err
					}
				}
				g.b.WriteString("))")
				return nil
			}
		}
	}
	// time.milliseconds(n) / time.seconds(n) — Duration constructors
	// (bindings.go). Lower to `time.Duration(n) * time.<Unit>`.
	if f, ok := c.Callee.(*ast.Field); ok {
		if recv, ok := f.Receiver.(*ast.Ident); ok && recv.Name == "time" {
			if unit, ok := timeDurationUnit(f.Name); ok {
				if len(c.Args) != 1 {
					return fmt.Errorf("codegen: time.%s expects exactly one argument, got %d", f.Name, len(c.Args))
				}
				g.b.WriteString("time.Duration(")
				if err := g.emitExpr(c.Args[0]); err != nil {
					return err
				}
				g.b.WriteString(") * time.")
				g.b.WriteString(unit)
				return nil
			}
		}
	}
	// strings.fromBytes(b) — the []byte → string round-trip binding
	// (binding-surface.md §strings). Lowers to Go's `string(b)`
	// conversion.
	if isFieldCall(c.Callee, "strings", "fromBytes") && len(c.Args) == 1 {
		g.b.WriteString("string(")
		if err := g.emitExpr(c.Args[0]); err != nil {
			return err
		}
		g.b.WriteByte(')')
		return nil
	}
	// makeSlice<T>(n, v) — predeclared generic builtin lowering
	// to the inline tideMakeSlice helper.
	if id, ok := c.Callee.(*ast.Ident); ok && id.Name == "makeSlice" {
		g.b.WriteString("tideMakeSlice")
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
	// makeChannel<T>(cap?) — predeclared channel constructor
	// (lowering-go.md §Channel lowering): `make(chan T)` unbuffered,
	// `make(chan T, cap)` when a capacity is given.
	if id, ok := c.Callee.(*ast.Ident); ok && id.Name == "makeChannel" {
		g.b.WriteString("make(chan ")
		if len(c.TypeArgs) == 1 {
			if err := g.emitTypeExpr(c.TypeArgs[0]); err != nil {
				return err
			}
		}
		if len(c.Args) == 1 {
			g.b.WriteString(", ")
			if err := g.emitExpr(c.Args[0]); err != nil {
				return err
			}
		}
		g.b.WriteByte(')')
		return nil
	}
	// Predeclared Result/Option constructor (`Ok`/`Err`/`Some`/`None`)
	// in an expected-type position — emit explicit Go type args from
	// the context so the type parameter the argument leaves open
	// (`Ok(v)` → E, `Err(e)` → T) is supplied rather than left for
	// Go's inference to fail on (lowering-go.md §Container types).
	if id, ok := c.Callee.(*ast.Ident); ok {
		if targs, info, ok := g.predeclaredCtorTypeArgs(id.Name, expect); ok {
			g.b.WriteString(goIdent(info.owner))
			g.b.WriteString(goIdent(id.Name))
			g.b.WriteByte('[')
			for i, ta := range targs {
				if i > 0 {
					g.b.WriteString(", ")
				}
				if err := g.emitTypeExpr(ta); err != nil {
					return err
				}
			}
			g.b.WriteByte(']')
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
	// Channel instance methods (lowering-go.md §Channel lowering):
	//   ch.send(v)  → ch <- v
	//   ch.recv()   → <-ch
	//   ch.tryRecv()→ tideTryRecv(ch)   (non-blocking select helper)
	//   ch.close()  → close(ch)
	if f, ok := c.Callee.(*ast.Field); ok && g.isChannelReceiver(f.Receiver) {
		switch f.Name {
		case "send":
			if len(c.Args) != 1 {
				return fmt.Errorf("codegen: .send expects exactly one argument, got %d", len(c.Args))
			}
			if err := g.emitExpr(f.Receiver); err != nil {
				return err
			}
			g.b.WriteString(" <- ")
			return g.emitExpr(c.Args[0])
		case "recv":
			g.b.WriteString("<-")
			return g.emitExpr(f.Receiver)
		case "tryRecv":
			g.usesTryRecv = true
			g.usesOption = true
			g.b.WriteString("tideTryRecv(")
			if err := g.emitExpr(f.Receiver); err != nil {
				return err
			}
			g.b.WriteByte(')')
			return nil
		case "close":
			g.b.WriteString("close(")
			if err := g.emitExpr(f.Receiver); err != nil {
				return err
			}
			g.b.WriteByte(')')
			return nil
		}
	}
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
	if f, ok := c.Callee.(*ast.Field); ok && !isStdlibNamespace(f.Receiver) && !g.isContainerReceiver(f.Receiver) {
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
		case "bytes":
			// `s.bytes()` views a string as []byte — Go's
			// `[]byte(s)` conversion (binding-surface.md §strings).
			if len(c.Args) != 0 {
				return fmt.Errorf("codegen: .bytes takes no arguments, got %d", len(c.Args))
			}
			g.b.WriteString("[]byte(")
			if err := g.emitExpr(f.Receiver); err != nil {
				return err
			}
			g.b.WriteByte(')')
			return nil
		case "runes":
			// `s.runes()` views a string as []rune — Go's
			// `[]rune(s)` conversion (binding-surface.md §strings).
			if len(c.Args) != 0 {
				return fmt.Errorf("codegen: .runes takes no arguments, got %d", len(c.Args))
			}
			g.b.WriteString("[]rune(")
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

// isContainerReceiver reports whether the receiver expression
// resolves to a predeclared-container instance (Map / Set /
// Stack). Used by emitCall to suppress the slice-method
// shortcut on container instances whose method names happen to
// collide with slice methods (`.len()`, `.push()`).
func (g *gen) isContainerReceiver(e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	if !ok {
		return false
	}
	return g.varKindOf(id) != ""
}

// isChannelReceiver reports whether e is a channel-typed binding
// (Channel / SendChan / RecvChan), so emitCall can lower its method
// calls (`.send` / `.recv` / `.tryRecv` / `.close`) to Go channel
// operators rather than method dispatch.
func (g *gen) isChannelReceiver(e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	if !ok {
		return false
	}
	switch g.varKindOf(id) {
	case "Channel", "SendChan", "RecvChan":
		return true
	}
	return false
}

// varKindOf returns the predeclared-container kind ("Map" / "Set" /
// "Stack") of the binding referenced by id, read from the sema
// side-table (its inferred type), or "" when id is not a container
// instance. This replaces codegen's former shallow varKind tracker
// with sema as the single source of truth.
func (g *gen) varKindOf(id *ast.Ident) string {
	if g.info == nil {
		return ""
	}
	t := g.info.Type[id]
	if t == nil {
		if sym := g.info.Symbol[id]; sym != nil {
			t = sym.Type
		}
	}
	switch t.(type) {
	case *sema.Map:
		return "Map"
	case *sema.Set:
		return "Set"
	case *sema.Stack:
		return "Stack"
	case *sema.Channel:
		return "Channel"
	case *sema.SendChan:
		return "SendChan"
	case *sema.RecvChan:
		return "RecvChan"
	}
	return ""
}

// isStdlibNamespace reports whether expr is an Ident whose name
// is in the hardcoded stdlib binding registry. Used by emitCall
// to keep `fmt.println` from being interpreted as a slice
// method call.
func isStdlibNamespace(e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	return ok && isStdlibNamespaceName(id.Name)
}

func isStdlibNamespaceName(name string) bool {
	switch name {
	case "fmt", "os", "strings", "strconv", "bufio", "context",
		"time", "sync", "io", "log", "net", "encoding", "math":
		return true
	}
	return false
}

// isConversionBinding reports whether the stdlib binding pkg.method
// lowers to a Go type conversion (not a pkg.* call), so it needs no
// import. Currently only `strings.fromBytes` → `string(b)`.
func isConversionBinding(pkg, method string) bool {
	return pkg == "strings" && method == "fromBytes"
}

// mapFieldName lowers a `pkg.method` / `pkg.value` reference to its
// Go identifier. For a value-returning stdlib binding it consults the
// rename registry (bindings.go); otherwise (a struct/class field, or
// an unmodelled name) it falls back to goIdent. Result-wrapping
// bindings never reach here — emitCall intercepts them.
func mapFieldName(receiver ast.Expr, name string) string {
	id, ok := receiver.(*ast.Ident)
	if !ok {
		return goIdent(name)
	}
	if g, ok := stdlibRenameOf(id.Name, name); ok {
		return g
	}
	return goIdent(name)
}

// predeclaredCtorTypeArgs returns the type arguments to stamp onto a
// predeclared Result/Option constructor call `name(...)` given the
// expected type at the call site, plus whether the stamp applies.
// It applies only when name is `Ok`/`Err`/`Some`/`None` and expect is
// the matching `Result<T, E>` / `Option<T>` NamedType carrying the
// right arity — user sum types are non-generic in v1, so Go infers
// their type params and they need no stamp.
func (g *gen) predeclaredCtorTypeArgs(name string, expect ast.TypeExpr) ([]ast.TypeExpr, variantInfo, bool) {
	info, ok := g.variant[name]
	if !ok || (info.owner != "Option" && info.owner != "Result") {
		return nil, variantInfo{}, false
	}
	nt, ok := expect.(*ast.NamedType)
	if !ok || len(nt.QName) != 1 || nt.QName[0] != info.owner {
		return nil, variantInfo{}, false
	}
	want := 1
	if info.owner == "Result" {
		want = 2
	}
	if len(nt.Args) != want {
		return nil, variantInfo{}, false
	}
	return nt.Args, info, true
}

// isFmtScan reports whether e is the callee `fmt.scan` (the single-
// value stdin binding). Multi-value scan2/scan3 land later.
func isFmtScan(e ast.Expr) bool {
	return isFieldCall(e, "fmt", "scan")
}

// isFieldCall reports whether e is the field access `recv.name`
// where recv is the bare identifier `recvName` — used to recognise
// namespaced stdlib bindings (`strings.fromBytes`, `fmt.scan`).
func isFieldCall(e ast.Expr, recvName, name string) bool {
	f, ok := e.(*ast.Field)
	if !ok || f.Name != name {
		return false
	}
	recv, ok := f.Receiver.(*ast.Ident)
	return ok && recv.Name == recvName
}
