package codegen

// call.go — call-expression lowering: the emitCall dispatch and its
// stdlib-binding / container / channel / slice-method helpers, plus the
// shared emitTypeArgs / emitArgList primitives. The binding registries
// it consults live in bindings.go.

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
	// json.* binding — parse<T> / serialize / serializeIndent
	// (binding-surface.md §encoding/json). Gated on the receiver's
	// sema symbol so a user value named `json` isn't hijacked.
	if handled, err := g.emitJSONCall(c); handled {
		return err
	}
	// `error(msg)` free constructor (builtins.md §error) → Go's
	// errors.New(msg). Distinguished from the `.error()` interface
	// method (a Field callee) and from any error-conversion by the
	// bare `error` identifier callee with exactly one argument (the
	// `error(): string` method takes none).
	if g.isErrorCtorCall(c) {
		g.b.WriteString("errors.New(")
		if err := g.emitExpr(c.Args[0]); err != nil {
			return err
		}
		g.b.WriteByte(')')
		return nil
	}
	// fmt.scan<T>() — stdin binding. Lowers to the tideScan helper,
	// which wraps Go's pointer-mutation `fmt.Scan(&v)` into the
	// Result<T, error> return form (binding-surface.md §fmt).
	if isFmtScan(c.Callee) {
		g.b.WriteString("tideScan")
		if err := g.emitTypeArgs(c.TypeArgs); err != nil {
			return err
		}
		g.b.WriteString("()")
		return nil
	}
	// fmt.scan2<A,B>() / fmt.scan3<A,B,C>() — multi-value stdin bindings
	// returning Result<(A,B[,C]), error> (binding-surface.md §fmt). Lower
	// to the tideScan2 / tideScan3 helpers (one fmt.Scan of N pointers,
	// folded into a tuple Ok). The tuple Ok payload destructures through
	// the existing tuple-in-variant-payload match path.
	if n := fmtScanMultiArity(c.Callee); n > 0 && len(c.TypeArgs) == n {
		if n == 2 {
			g.usesScan2 = true
			g.b.WriteString("tideScan2")
		} else {
			g.usesScan3 = true
			g.b.WriteString("tideScan3")
		}
		g.usesResult = true
		if err := g.emitTypeArgs(c.TypeArgs); err != nil {
			return err
		}
		g.b.WriteString("()")
		return nil
	}
	// refEq(a, b) — class identity. Classes lower to pointer types, so
	// Go's `==` is reference identity (lowering-go.md §Defer / panic /
	// refEq); sema's T-RefEq has guaranteed both operands are the same
	// class. Gated on the callee resolving to the predeclared builtin
	// (SymBuiltinFunc) so a user decl that shadows the name is still
	// called normally rather than silently rewritten. Parenthesised for
	// safe nesting under a prefix `!` / surrounding operators.
	if id, ok := c.Callee.(*ast.Ident); ok && id.Name == "refEq" && len(c.Args) == 2 &&
		g.info != nil && g.info.Symbol[id] != nil && g.info.Symbol[id].Kind == sema.SymBuiltinFunc {
		g.b.WriteByte('(')
		if err := g.emitExpr(c.Args[0]); err != nil {
			return err
		}
		g.b.WriteString(" == ")
		if err := g.emitExpr(c.Args[1]); err != nil {
			return err
		}
		g.b.WriteByte(')')
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
				if err := g.emitArgList(c.Args); err != nil {
					return err
				}
				g.b.WriteByte(')')
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
	// sort.sorted(s, less) — comparator sort that returns a NEW slice
	// (binding-surface.md §sort). Lowers to the inline tideSorted helper
	// (copy + sort.SliceStable), preserving the input's immutability.
	// The comparator's omitted param types are stamped from sema's
	// inferred Func (emitClosure reads g.info.Type) — T-Closure. Gated on
	// the receiver resolving to the predeclared `sort` module (mirrors
	// the refEq / error-ctor gates) so a user value named `sort` with its
	// own `.sorted(a, b)` method is not hijacked.
	if f, ok := c.Callee.(*ast.Field); ok && len(c.Args) == 2 && f.Name == "sorted" {
		if recv, ok := f.Receiver.(*ast.Ident); ok && recv.Name == "sort" && g.isBuiltinModule(recv) {
			g.usesSortSorted = true
			g.b.WriteString("tideSorted")
			return g.emitArgList(c.Args)
		}
	}
	// Conversion binding — `pkg.method(arg)` that lowers to a Go type
	// conversion `target(arg)` (e.g. strings.fromBytes → string(b)),
	// not a package call (bindings.go §stdlibConversion).
	if f, ok := c.Callee.(*ast.Field); ok && len(c.Args) == 1 {
		if recv, ok := f.Receiver.(*ast.Ident); ok {
			if target, ok := stdlibConversionOf(recv.Name, f.Name); ok {
				g.b.WriteString(target)
				g.b.WriteByte('(')
				if err := g.emitExpr(c.Args[0]); err != nil {
					return err
				}
				g.b.WriteByte(')')
				return nil
			}
		}
	}
	// makeSlice<T>(n, v) — predeclared generic builtin lowering
	// to the inline tideMakeSlice helper.
	if id, ok := c.Callee.(*ast.Ident); ok && id.Name == "makeSlice" {
		g.b.WriteString("tideMakeSlice")
		if err := g.emitTypeArgs(c.TypeArgs); err != nil {
			return err
		}
		return g.emitArgList(c.Args)
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
			if err := g.emitTypeArgs(targs); err != nil {
				return err
			}
			return g.emitArgList(c.Args)
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
	// Foreign-binding call (ffi.md §ForeignCall): an extern function
	// `f(args)` → `pkg.Sym(args)`; an extern method `recv.m(args)` on
	// an opaque handle → `recv.GoName(args)`. Both wrap in tideResultOf
	// when their curated return is `Result<…>`.
	if id, ok := c.Callee.(*ast.Ident); ok {
		if efd, isExtern := g.externFunc[id.Name]; isExtern {
			return g.emitExternFuncCall(efd, c)
		}
	}
	if f, ok := c.Callee.(*ast.Field); ok {
		if m, isExtern := g.externMethodOf(f); isExtern {
			return g.emitExternMethodCall(m, f, c)
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
			if err := g.emitTypeArgs(c.TypeArgs); err != nil {
				return err
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
				if err := g.emitTypeArgs(c.TypeArgs); err != nil {
					return err
				}
				return g.emitArgList(c.Args)
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
		case "copy":
			// `s.copy()` returns a shallow clone with a fresh backing
			// array (builtins.md §Slice methods). `append(s[:0:0], s...)`
			// is the expression-position form: the zero-cap reslice
			// forces append to allocate, so the result never aliases s —
			// equivalent to `make`+`copy` without naming the element type
			// (lowering-go.md §Slice methods).
			if len(c.Args) != 0 {
				return fmt.Errorf("codegen: .copy takes no arguments, got %d", len(c.Args))
			}
			g.b.WriteString("append(")
			if err := g.emitExpr(f.Receiver); err != nil {
				return err
			}
			g.b.WriteString("[:0:0], ")
			if err := g.emitExpr(f.Receiver); err != nil {
				return err
			}
			g.b.WriteString("...)")
			return nil
		}
	}
	// Generic user-sum payload-variant constructor `Node(...)`. Go
	// infers the type args from the value arguments (`value: T` ← the
	// first arg), so the constructor emits bare — but a nested *nullary*
	// variant (`Node(1, Leaf, Leaf)`) has no argument for Go to infer
	// from, so thread the inferred instantiation down to the arguments
	// (§Generics) where the bare-variant emit consumes it.
	if id, ok := c.Callee.(*ast.Ident); ok {
		if info, isVar := g.variant[id.Name]; isVar && len(info.sumTypeParams) > 0 && len(info.fields) > 0 {
			g.b.WriteString(goIdent(info.owner))
			g.b.WriteString(goIdent(id.Name))
			prev := g.sumCtorArgs
			g.sumCtorArgs = g.genericSumCtorArgs(info, c.Args)
			err := g.emitArgList(c.Args)
			g.sumCtorArgs = prev
			return err
		}
	}
	// Bare instance-method call via the implicit receiver (`find(a)`
	// inside a sibling method → `t.find(a)`; name-resolution §Implicit
	// receiver). Sema resolves the bare name to the method symbol off
	// the member scope (so the call is typed); codegen prefixes the
	// receiver `t`, mirroring the bare-field case in emitExpr.
	if id, ok := c.Callee.(*ast.Ident); ok && g.isImplicitInstanceMethod(id) {
		g.b.WriteString("t.")
		g.b.WriteString(goIdent(id.Name))
		if err := g.emitTypeArgs(c.TypeArgs); err != nil {
			return err
		}
		return g.emitArgList(c.Args)
	}
	// A method-call selector `recv.method(...)` is spelled by
	// goMethodName (lowercase — methods stay unexported), NOT by the
	// field-value path (emitField → goFieldName), which exports its
	// name. Routing the callee through emitExpr here would wrongly
	// export the method. Non-Field callees (free functions, closures,
	// indexes) keep the generic emit.
	if fld, ok := c.Callee.(*ast.Field); ok {
		if err := g.emitExpr(fld.Receiver); err != nil {
			return err
		}
		g.b.WriteByte('.')
		// A func-typed *data field* called as `recv.fn(x)` takes the
		// exported field spelling; a genuine method stays lowercase.
		if g.isDataFieldSelector(fld.Receiver, fld.Name) {
			g.b.WriteString(g.goFieldName(fld.Receiver, fld.Name))
		} else {
			g.b.WriteString(g.goMethodName(fld.Receiver, fld.Name))
		}
	} else if err := g.emitExpr(c.Callee); err != nil {
		return err
	}
	if err := g.emitTypeArgs(c.TypeArgs); err != nil {
		return err
	}
	return g.emitArgList(c.Args)
}

// isImplicitInstanceMethod reports whether ident is a bare reference to
// an instance method of the enclosing class (sema resolved it to a
// non-static SymMethod via the member scope). Such a call needs the
// receiver `t.` prefix — the Go method lives on the receiver, not in
// package scope. A static method (called bare through the class scope)
// is excluded: it has no receiver.
func (g *gen) isImplicitInstanceMethod(id *ast.Ident) bool {
	if g.info == nil {
		return false
	}
	sym := g.info.Symbol[id]
	if sym == nil || sym.Kind != sema.SymMethod {
		return false
	}
	m, ok := sym.Decl.(*ast.Method)
	return ok && !m.IsStatic
}

// genericSumCtorArgs infers the Go type-argument strings for a generic
// sum variant constructor call, by reading the Go type of each value
// argument whose declared field type is a bare sum type-parameter
// (`value: T` pins `T`). Returns nil when not every parameter can be
// pinned — the caller then leaves nested variants un-stamped (Go
// inference covers the all-payload case; only nullary variants need
// the explicit args).
func (g *gen) genericSumCtorArgs(info variantInfo, args []ast.Expr) []string {
	subst := map[string]string{}
	for i, f := range info.fields {
		if i >= len(args) {
			break
		}
		nt, ok := f.DeclType.(*ast.NamedType)
		if !ok || len(nt.QName) != 1 || len(nt.Args) != 0 {
			continue
		}
		for _, tp := range info.sumTypeParams {
			if nt.QName[0] == tp {
				if s, err := g.inferArmResultType(args[i]); err == nil {
					subst[tp] = s
				}
			}
		}
	}
	out := make([]string, len(info.sumTypeParams))
	for i, tp := range info.sumTypeParams {
		s, ok := subst[tp]
		if !ok {
			return nil
		}
		out[i] = s
	}
	return out
}

// userSumCtorArgsFromExpect returns the type args to stamp on a bare
// nullary user-sum variant (`Leaf`) from an expected `Sum<…>` type — the
// generic analogue of predeclaredCtorTypeArgs for user sums (`return
// Leaf` in a `Tree<T>`-returning function).
func (g *gen) userSumCtorArgsFromExpect(info variantInfo, expect ast.TypeExpr) ([]ast.TypeExpr, bool) {
	nt, ok := expect.(*ast.NamedType)
	if !ok || len(nt.QName) != 1 || nt.QName[0] != info.owner {
		return nil, false
	}
	if len(nt.Args) != len(info.sumTypeParams) {
		return nil, false
	}
	return nt.Args, true
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

// isBuiltinModule reports whether ident resolves (via sema) to a
// predeclared stdlib module, not a user value that merely shares the
// name. Used to gate name-matched binding intercepts so a local
// shadowing the module is dispatched as an ordinary value.
func (g *gen) isBuiltinModule(id *ast.Ident) bool {
	if g.info == nil {
		return false
	}
	sym := g.info.Symbol[id]
	return sym != nil && sym.Kind == sema.SymBuiltinModule
}

func isStdlibNamespaceName(name string) bool {
	switch name {
	case "fmt", "os", "strings", "strconv", "bufio", "context",
		"time", "sync", "io", "log", "net", "encoding", "math", "sort",
		"json":
		return true
	}
	return false
}

// IsStdlibNamespace reports whether name is a recognized stdlib binding
// namespace (the v0.x hardcoded registry). Exported for the package
// resolver's import classification (RFC-0002 §Resolution, step 2).
func IsStdlibNamespace(name string) bool { return isStdlibNamespaceName(name) }

// isConversionBinding reports whether the stdlib binding pkg.method
// lowers to a Go type conversion (not a pkg.* call), so it needs no
// import. Derived from the stdlibConversion registry — the same source
// the lowering uses, so the two never diverge.
func isConversionBinding(pkg, method string) bool {
	_, ok := stdlibConversionOf(pkg, method)
	return ok
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
// value stdin binding).
func isFmtScan(e ast.Expr) bool {
	return isFieldCall(e, "fmt", "scan")
}

// fmtScanMultiArity returns the arity of a multi-value `fmt.scanN`
// stdin binding callee (2 for `fmt.scan2`, 3 for `fmt.scan3`), or 0
// when e is not one. The single-value `fmt.scan` is isFmtScan's domain.
func fmtScanMultiArity(e ast.Expr) int {
	switch {
	case isFieldCall(e, "fmt", "scan2"):
		return 2
	case isFieldCall(e, "fmt", "scan3"):
		return 3
	}
	return 0
}

// isErrorCtorCall reports whether c is the `error(msg)` free
// constructor (builtins.md §error): a bare `error` identifier callee
// with exactly one argument whose sema symbol is the predeclared
// `error` builtin type. Gating on the resolved symbol (not the bare
// name) means a user who shadows `error` with their own decl is not
// silently hijacked — same discipline as the refEq intercept. The
// `error(): string` interface method takes no arguments, and
// `.error()` calls are Field callees, so the one-argument form is
// otherwise unambiguous.
func (g *gen) isErrorCtorCall(c *ast.Call) bool {
	id, ok := c.Callee.(*ast.Ident)
	if !ok || id.Name != "error" || len(c.Args) != 1 {
		return false
	}
	if g.info == nil {
		return false
	}
	sym := g.info.Symbol[id]
	return sym != nil && sym.Kind == sema.SymBuiltinType
}

// emitTypeArgs writes a Go type-argument list `[A, B, …]` for the
// given type expressions, or nothing when the slice is empty. The
// single emit point for every `[…]` instantiation across codegen.
func (g *gen) emitTypeArgs(targs []ast.TypeExpr) error {
	if len(targs) == 0 {
		return nil
	}
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
	return nil
}

// emitArgList writes a Go positional argument list `(a, b, …)`,
// emitting each argument expression in order.
func (g *gen) emitArgList(args []ast.Expr) error {
	g.b.WriteByte('(')
	for i, a := range args {
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
