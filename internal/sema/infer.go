package sema

import (
	"strconv"

	"github.com/heni/tide-lang/internal/ast"
)

// inferExpr returns the type of e, recording it in Info.Type and
// emitting any local typing diagnostic. A shape PR-C1 cannot type
// yet returns *Unknown. See docs/internals/sema.md §4 / §6.
func (c *checker) inferExpr(e ast.Expr) Type {
	if e == nil {
		return &Unit{}
	}
	var t Type
	switch v := e.(type) {
	case *ast.IntLitExpr:
		t = &Builtin{N: "int"}
	case *ast.StringLitExpr:
		t = &Builtin{N: "string"}
	case *ast.BoolLitExpr:
		t = &Builtin{N: "bool"}
	case *ast.RuneLitExpr:
		t = &Builtin{N: "rune"}
	case *ast.ThisExpr:
		if c.curThis != nil {
			t = c.curThis
		} else {
			t = &Unknown{}
		}
	case *ast.Ident:
		t = c.inferIdent(v)
	case *ast.Call:
		t = c.inferCall(v)
	case *ast.Field:
		t = c.inferField(v)
	case *ast.Binary:
		t = c.inferBinary(v)
	case *ast.Unary:
		t = c.inferUnary(v)
	case *ast.MatchExpr:
		t = c.inferMatch(v)
	case *ast.ReturnExpr:
		c.checkReturn(v)
		t = &Never{}
	case *ast.TryExpr:
		c.inferExpr(v.Inner)
		t = &Unknown{} // try-unwrap typing lands with the collection / Result PR
	case *ast.SliceLit:
		t = c.inferSliceLit(v)
	case *ast.Index:
		t = c.inferIndex(v)
	case *ast.Slice:
		recv := c.inferExpr(v.Receiver)
		c.expectInt(v.Low)
		c.expectInt(v.High)
		if _, ok := recv.(*Slice); ok {
			t = recv // s[lo:hi] : []T
		} else {
			t = &Unknown{}
		}
	default:
		t = &Unknown{}
	}
	c.info.Type[e] = t
	return t
}

func (c *checker) inferIdent(id *ast.Ident) Type {
	if id.Name == "_" {
		return &Unknown{}
	}
	if sym := c.info.Symbol[id]; sym != nil {
		return symValueType(sym)
	}
	return &Unknown{}
}

// inferCall types a call and checks argument types against the
// callee's parameters (E0201) on top of the arity check (E0202).
func (c *checker) inferCall(call *ast.Call) Type {
	c.inferExpr(call.Callee)
	args := make([]Type, len(call.Args))
	for i, a := range call.Args {
		args[i] = c.inferExpr(a)
	}
	c.checkCallArity(call)

	ret := Type(&Unknown{})
	if id, ok := call.Callee.(*ast.Ident); ok {
		if sym := c.info.Symbol[id]; sym != nil {
			switch sym.Kind {
			case SymFunc, SymMethod:
				if fn, ok := sym.Type.(*Func); ok {
					c.checkArgTypes(fn.Params, args, call.Args, sym.Name)
					ret = fn.Return
				}
			case SymClass:
				ret = c.checkConstructor(sym, args, call.Args)
			case SymUserVariant:
				// Payload-variant constructor: its value is the sum.
				ret = sym.Type
			case SymBuiltinType:
				ret = c.inferBuiltinTypeCall(id.Name, call, args)
			case SymBuiltinFunc:
				ret = c.inferBuiltinFuncCall(id.Name, call, args)
			}
		}
	} else if f, ok := call.Callee.(*ast.Field); ok {
		// Method call `recv.m(args)`. inferField already typed the
		// callee as the method's Func when the receiver is a class.
		if fn, ok := c.info.Type[f].(*Func); ok {
			c.checkArgTypes(fn.Params, args, call.Args, f.Name)
			ret = fn.Return
		}
	}
	return ret
}

// checkConstructor types a `ClassName(args)` constructor call,
// checking each argument against the corresponding field type.
func (c *checker) checkConstructor(sym *Symbol, args []Type, argNodes []ast.Expr) Type {
	cd, ok := sym.Decl.(*ast.ClassDecl)
	if !ok {
		return &Unknown{}
	}
	params := make([]Type, len(cd.Fields))
	for i, f := range cd.Fields {
		params[i] = c.typeFromExpr(f.DeclType)
	}
	c.checkArgTypes(params, args, argNodes, cd.Name)
	return &Named{N: cd.Name, Decl: cd}
}

// checkArgTypes fires E0201 per positional argument whose type
// disagrees with the parameter. Length mismatches are E0202's
// job; this only compares the overlapping prefix.
func (c *checker) checkArgTypes(params, args []Type, nodes []ast.Expr, callee string) {
	n := len(params)
	if len(args) < n {
		n = len(args)
	}
	for i := 0; i < n; i++ {
		if !c.fits(params[i], nodes[i], args[i]) {
			c.report("E0201",
				"Type mismatch — argument "+strconv.Itoa(i+1)+" to "+callee+
					" expects "+params[i].String()+", got "+args[i].String(),
				nodes[i].NodeSpan())
		}
	}
}

// inferField types member access `recv.name`: a class field gives
// its declared type, a class method gives its Func type. Module
// access and everything else stays Unknown for PR-C1.
func (c *checker) inferField(f *ast.Field) Type {
	recv := c.inferExpr(f.Receiver)
	named, ok := recv.(*Named)
	if !ok {
		return &Unknown{}
	}
	cd, ok := named.Decl.(*ast.ClassDecl)
	if !ok {
		return &Unknown{}
	}
	for _, fld := range cd.Fields {
		if fld.Name == f.Name {
			return c.typeFromExpr(fld.DeclType)
		}
	}
	for _, m := range cd.Methods {
		if m.Name == f.Name {
			return c.methodSigType(m)
		}
	}
	return &Unknown{}
}

func (c *checker) inferBinary(b *ast.Binary) Type {
	lt := c.inferExpr(b.Left)
	rt := c.inferExpr(b.Right)
	lt, rt = c.adaptIntLiteralOperands(b, lt, rt)
	switch b.Op {
	case "+":
		// `+` is numeric addition or string concatenation.
		if isString(lt) || isString(rt) {
			c.expectSame(lt, rt, b, "string concatenation")
			return &Builtin{N: "string"}
		}
		return c.numericResult(lt, rt, b)
	case "-", "*", "/", "%":
		return c.numericResult(lt, rt, b)
	case "==", "!=":
		// Equality demands a comparable type (T-Cmp); class
		// operands route to refEq, collections / funcs are not
		// comparable at all.
		if (concrete(lt) && !comparable(lt)) || (concrete(rt) && !comparable(rt)) {
			c.report("E0401", "`"+b.Op+"` on non-comparable type — compare field-wise, or use `refEq` for class identity", b.Span)
		} else {
			c.expectSame(lt, rt, b, "comparison")
		}
		return &Builtin{N: "bool"}
	case "<", "<=", ">", ">=":
		// Ordering-domain enforcement (Ord) lands with a later PR;
		// here we only require the two operands to agree.
		c.expectSame(lt, rt, b, "comparison")
		return &Builtin{N: "bool"}
	case "&&", "||":
		// Dynamic / Any operands are a boundary concern, not a
		// bool-operand mismatch — consistent with the numeric /
		// comparison paths.
		if concrete(lt) && !isBool(lt) && !isDynamic(lt) && !isAny(lt) {
			c.report("E0201", "Type mismatch — `"+b.Op+"` expects bool, got "+lt.String(), b.Left.NodeSpan())
		}
		if concrete(rt) && !isBool(rt) && !isDynamic(rt) && !isAny(rt) {
			c.report("E0201", "Type mismatch — `"+b.Op+"` expects bool, got "+rt.String(), b.Right.NodeSpan())
		}
		return &Builtin{N: "bool"}
	default:
		return &Unknown{}
	}
}

// adaptIntLiteralOperands narrows an integer-literal operand to the
// other operand's concrete integer type (type-system.md §Literals):
// `b == 0` on a `byte`, `r + 1` on a `rune`. Returns the adjusted
// operand types and records the narrowed type / range-checks the
// literal. When both operands are literals nothing changes (both
// stay `int`).
func (c *checker) adaptIntLiteralOperands(b *ast.Binary, lt, rt Type) (Type, Type) {
	if _, ok := b.Right.(*ast.IntLitExpr); ok && isIntegerType(lt) {
		c.info.Type[b.Right] = lt
		c.checkIntLitRange(lt, b.Right)
		rt = lt
	}
	if _, ok := b.Left.(*ast.IntLitExpr); ok && isIntegerType(rt) {
		c.info.Type[b.Left] = rt
		c.checkIntLitRange(rt, b.Left)
		lt = rt
	}
	return lt, rt
}

// numericResult checks both operands are the same numeric type and
// returns it; mismatches fire E0201.
func (c *checker) numericResult(lt, rt Type, b *ast.Binary) Type {
	// Dynamic / Any operands are governed by the boundary rules,
	// not by arithmetic typing — don't mislabel them here.
	if isDynamic(lt) || isDynamic(rt) || isAny(lt) || isAny(rt) {
		return &Unknown{}
	}
	if concrete(lt) && !isNumeric(lt) {
		c.report("E0201", "Type mismatch — `"+b.Op+"` expects a numeric type, got "+lt.String(), b.Left.NodeSpan())
		return &Unknown{}
	}
	if concrete(rt) && !isNumeric(rt) {
		c.report("E0201", "Type mismatch — `"+b.Op+"` expects a numeric type, got "+rt.String(), b.Right.NodeSpan())
		return &Unknown{}
	}
	c.expectSame(lt, rt, b, "operands of `"+b.Op+"`")
	if concrete(lt) {
		return lt
	}
	return rt
}

// expectSame fires E0201 when both operand types are concrete and
// unequal. The diagnostic points at the right operand.
//
// Named operands (class / sum types) are left alone: `==`/`!=` on
// class types routes to E0206 (`refEq`) and comparability of
// nominal types is the comparability PR's job, so reporting a
// generic E0201 here would mislabel the eventual diagnostic.
func (c *checker) expectSame(lt, rt Type, b *ast.Binary, what string) {
	if _, ok := lt.(*Named); ok {
		return
	}
	if _, ok := rt.(*Named); ok {
		return
	}
	// Dynamic / Any operand agreement is a boundary concern, not a
	// generic equality mismatch.
	if isDynamic(lt) || isDynamic(rt) || isAny(lt) || isAny(rt) {
		return
	}
	if concrete(lt) && concrete(rt) && !equal(lt, rt) {
		c.report("E0201", "Type mismatch — "+what+" require equal types, got "+lt.String()+" and "+rt.String(), b.Right.NodeSpan())
	}
}

func (c *checker) inferUnary(u *ast.Unary) Type {
	ot := c.inferExpr(u.Operand)
	switch u.Op {
	case "!":
		if concrete(ot) && !isBool(ot) {
			c.report("E0201", "Type mismatch — `!` expects bool, got "+ot.String(), u.Operand.NodeSpan())
		}
		return &Builtin{N: "bool"}
	case "-":
		if concrete(ot) && !isNumeric(ot) {
			c.report("E0201", "Type mismatch — unary `-` expects a numeric type, got "+ot.String(), u.Operand.NodeSpan())
			return &Unknown{}
		}
		return ot
	default:
		return &Unknown{}
	}
}

// inferMatch types a match expression. Arm bodies are inferred;
// the whole expression's type is the first concrete arm type
// (arms are required to agree, but arm-agreement diagnostics and
// exhaustiveness are Barrier D — here we only need a result type).
func (c *checker) inferMatch(m *ast.MatchExpr) Type {
	c.inferExpr(m.Subject)
	var result Type = &Unknown{}
	for _, arm := range m.Arms {
		at := c.inferExpr(arm.Body)
		if isUnknown(result) && concrete(at) {
			if _, never := at.(*Never); !never {
				result = at
			}
		}
	}
	return result
}

// checkReturn fires E0203 when a `return e` value disagrees with
// the enclosing function's declared return type.
func (c *checker) checkReturn(r *ast.ReturnExpr) {
	var got Type = &Unit{}
	if r.Value != nil {
		got = c.inferExpr(r.Value)
	}
	want := c.curReturn
	if want == nil {
		want = &Unit{}
	}
	// A bare `return` yields unit; only narrow / range-check when
	// there is an actual value expression.
	if r.Value == nil {
		if concrete(want) && !assignable(want, got) {
			c.report("E0203", "Wrong return type — function returns "+want.String()+", got "+got.String(), r.Span)
		}
		return
	}
	if !c.fits(want, r.Value, got) {
		c.report("E0203", "Wrong return type — function returns "+want.String()+", got "+got.String(), r.Span)
	}
}

// checkCallArity — E0202. Compares the call's positional argument
// count against the callee's declared parameter count when the
// callee is a user-declared func or class constructor reachable
// through Info. Methods, variadic and stdlib-binding calls are
// skipped (their arities are not modelled yet).
func (c *checker) checkCallArity(call *ast.Call) {
	id, ok := call.Callee.(*ast.Ident)
	if !ok {
		return
	}
	sym, ok := c.info.Symbol[id]
	if !ok || sym == nil {
		return
	}
	switch sym.Kind {
	case SymFunc:
		fn, ok := sym.Decl.(*ast.FuncDecl)
		if !ok {
			return
		}
		want := len(fn.Params)
		got := len(call.Args)
		if want != got {
			c.report("E0202",
				"Wrong arity in call to "+fn.Name+
					": expects "+strconv.Itoa(want)+" "+pluralArgs(want)+
					", got "+strconv.Itoa(got),
				call.Span)
		}
	case SymClass:
		cd, ok := sym.Decl.(*ast.ClassDecl)
		if !ok {
			return
		}
		want := len(cd.Fields)
		got := len(call.Args)
		if want != got {
			c.report("E0202",
				"Wrong arity in constructor "+cd.Name+
					": expects "+strconv.Itoa(want)+" field "+pluralArgs(want)+
					", got "+strconv.Itoa(got),
				call.Span)
		}
	}
}
