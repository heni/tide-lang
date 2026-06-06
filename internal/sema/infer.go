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
			c.report("E0501", "`this` outside an instance-method body", v.Span)
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
	case *ast.ParenExpr:
		t = c.inferExpr(v.Inner)
	case *ast.TupleLit:
		comps := make([]Type, len(v.Components))
		for i, ce := range v.Components {
			comps[i] = c.inferExpr(ce)
		}
		t = &Tuple{Comps: comps}
	case *ast.TupleField:
		rt := c.inferExpr(v.Receiver)
		if tup, ok := rt.(*Tuple); ok && v.Position >= 0 && v.Position < len(tup.Comps) {
			t = tup.Comps[v.Position]
		} else {
			t = &Unknown{}
		}
	case *ast.BraceLit:
		t = c.inferBraceLit(v)
	case *ast.Block:
		t = c.inferBlock(v)
	case *ast.IfExpr:
		t = c.inferIfExpr(v)
	case *ast.MatchExpr:
		t = c.inferMatch(v)
	case *ast.ReturnExpr:
		c.checkReturn(v)
		t = &Never{}
	case *ast.BreakExpr:
		if c.loopDepth == 0 {
			c.report("E0404", "`break` outside a loop", v.Span)
		}
		t = &Never{}
	case *ast.ContinueExpr:
		if c.loopDepth == 0 {
			c.report("E0404", "`continue` outside a loop", v.Span)
		}
		t = &Never{}
	case *ast.TryExpr:
		c.inferExpr(v.Inner)
		if c.curTryForbidden {
			c.report("E0402", "`try` outside a Result/Option-returning function", v.Span)
		}
		t = &Unknown{} // try-unwrap result typing lands with the Result/Option PR
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

// inferBraceLit types a brace literal. Record literals resolve to
// their nominal type and check each field value against the declared
// field type (E0201). Map / Set / Stack literals stay Unknown until
// their own modelling lands — their entry values are still inferred.
func (c *checker) inferBraceLit(b *ast.BraceLit) Type {
	rt := c.typeFromExpr(b.TypeName)
	for _, e := range b.Entries {
		switch en := e.(type) {
		case *ast.RecordEntry:
			vt := c.inferExpr(en.Value)
			if ft := c.recordFieldType(rt, en.Name); ft != nil {
				if !c.fits(ft, en.Value, vt) {
					c.report("E0201", "Type mismatch — field "+en.Name+" expects "+ft.String()+", got "+vt.String(), en.Value.NodeSpan())
				}
			} else if isRecordType(rt) {
				// Unknown field on a known record — catch it here in
				// .td coordinates rather than leaking a go/types error.
				c.report("E0201", "Record "+rt.String()+" has no field "+en.Name, en.Span)
			}
		case *ast.MapEntry:
			c.inferExpr(en.Key)
			c.inferExpr(en.Value)
		case *ast.SetEntry:
			c.inferExpr(en.Value)
		}
	}
	if b.Kind == ast.BraceRecord {
		return rt
	}
	return &Unknown{}
}

// isRecordType reports whether t is a nominal record (a Named whose
// decl is a RecordTypeBody).
func isRecordType(t Type) bool {
	named, ok := t.(*Named)
	if !ok {
		return false
	}
	td, ok := named.Decl.(*ast.TypeDecl)
	if !ok {
		return false
	}
	_, ok = td.Body.(*ast.RecordTypeBody)
	return ok
}

// recordFieldType returns the declared type of field `name` on a
// record type, or nil when t is not a record / has no such field.
func (c *checker) recordFieldType(t Type, name string) Type {
	named, ok := t.(*Named)
	if !ok {
		return nil
	}
	td, ok := named.Decl.(*ast.TypeDecl)
	if !ok {
		return nil
	}
	rb, ok := td.Body.(*ast.RecordTypeBody)
	if !ok {
		return nil
	}
	for _, f := range rb.Fields {
		if f.Name == name {
			return c.typeFromExpr(f.DeclType)
		}
	}
	return nil
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
					if len(fn.TypeParams) > 0 {
						fn = c.instantiate(fn, call, args)
					}
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
		// Static container constructor `Map<K,V>.new()` /
		// `Set<T>.new()` / `Set<T>.from(..)` / `Stack<T>.new()`:
		// the type arguments bind to the container, carried on the
		// Call. Produces the structured container type.
		if ct := c.staticContainerCtor(f, call); ct != nil {
			ret = ct
		} else if fn, ok := c.info.Type[f].(*Func); ok {
			// Method call `recv.m(args)`. inferField already typed
			// the callee as the method's Func when the receiver is
			// a class.
			c.checkArgTypes(fn.Params, args, call.Args, f.Name)
			ret = fn.Return
		}
	}
	return ret
}

// staticContainerCtor recognises a predeclared-container static
// constructor (`Map<K,V>.new()`, `Set<T>.new()/from()`,
// `Stack<T>.new()`) and returns the structured container type, or
// nil when f is not such a call.
func (c *checker) staticContainerCtor(f *ast.Field, call *ast.Call) Type {
	recv, ok := f.Receiver.(*ast.Ident)
	if !ok {
		return nil
	}
	if sym := c.info.Symbol[recv]; sym == nil || sym.Kind != SymBuiltinType {
		return nil
	}
	switch recv.Name {
	case "Map":
		if len(call.TypeArgs) == 2 {
			return &Map{Key: c.typeFromExpr(call.TypeArgs[0]), Val: c.typeFromExpr(call.TypeArgs[1])}
		}
	case "Set":
		if len(call.TypeArgs) == 1 {
			return &Set{Elem: c.typeFromExpr(call.TypeArgs[0])}
		}
	case "Stack":
		if len(call.TypeArgs) == 1 {
			return &Stack{Elem: c.typeFromExpr(call.TypeArgs[0])}
		}
	}
	return nil
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
	// Record field access — `p.x` on a record type.
	if ft := c.recordFieldType(named, f.Name); ft != nil {
		return ft
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
	if _, ok := unparen(b.Right).(*ast.IntLitExpr); ok && isIntegerType(lt) {
		c.info.Type[b.Right] = lt
		c.checkIntLitRange(lt, b.Right)
		rt = lt
	}
	if _, ok := unparen(b.Left).(*ast.IntLitExpr); ok && isIntegerType(rt) {
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
	subjectType := c.inferExpr(m.Subject)
	c.checkExhaustive(m, subjectType)
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

// inferBlock checks a block's statements and yields its value: the
// trailing expression's type, or unit when there is none. It is the
// single implementation behind both block-as-expression typing and
// statement-position block checking (checkBlock).
func (c *checker) inferBlock(b *ast.Block) Type {
	for _, s := range b.Stmts {
		c.checkStmt(s)
	}
	if b.Trailing != nil {
		return c.inferExpr(b.Trailing)
	}
	return &Unit{}
}

// inferIfExpr types an `if`-expression. The result is the branch
// type when concrete; an `if` with no `else`, or with disagreeing /
// non-concrete branches, stays conservative-Unknown so no false
// positive fires (branch-agreement is a later Barrier-D concern).
func (c *checker) inferIfExpr(e *ast.IfExpr) Type {
	c.inferExpr(e.Cond)
	thenT := c.inferBlock(e.ThenBlock)
	switch x := e.Else.(type) {
	case *ast.IfExpr:
		c.inferIfExpr(x)
	case *ast.Block:
		c.inferBlock(x)
	}
	if concrete(thenT) {
		return thenT
	}
	return &Unknown{}
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
