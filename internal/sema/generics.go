package sema

import (
	"strconv"

	"github.com/heni/tide-lang/internal/ast"
)

// Generic instantiation at a call site (type-system.md T-Call-Generic
// + the unify skeleton). A generic function's signature is expressed
// over Generic{name} parameters; a call either supplies explicit
// type arguments (`f<τ>(args)`) or leaves them to be inferred from
// the argument types. Either way the bound substitution is applied
// to the parameter and return types, the arguments are checked
// against the substituted parameters (E0201, via checkArgTypes at
// the call site), and a type parameter that resolves to Dynamic is
// rejected (E0211).

// instantiate returns the monomorphic Func obtained by binding fn's
// type parameters at this call. It emits E0211 for a Dynamic
// binding. fn.TypeParams is assumed non-empty.
func (c *checker) instantiate(fn *Func, call *ast.Call, args []Type) *Func {
	subst := map[string]Type{}

	switch {
	case len(call.TypeArgs) == len(fn.TypeParams):
		// Explicit type arguments.
		for i, name := range fn.TypeParams {
			subst[name] = c.typeFromExpr(call.TypeArgs[i])
		}
	case len(call.TypeArgs) > 0:
		// A non-empty but wrong number of explicit type arguments
		// is E0207; fall through to inference so the rest of the
		// call still type-checks against what can be bound.
		c.report("E0207",
			"Wrong type arity on generic instantiation: expects "+
				strconv.Itoa(len(fn.TypeParams))+" type "+pluralArgs(len(fn.TypeParams))+
				", got "+strconv.Itoa(len(call.TypeArgs)),
			call.Span)
		fallthrough
	default:
		// Infer from the argument types (Algorithm-W skeleton).
		for i := range fn.Params {
			if i < len(args) {
				unifyArg(fn.Params[i], args[i], subst)
			}
		}
	}

	// A type parameter that no argument pins down stays *Generic in
	// fn.Return (a residual wildcard), so a return-only `T` is
	// intentionally left unchecked rather than guessed.

	for _, name := range fn.TypeParams {
		if ty, ok := subst[name]; ok && isDynamic(ty) {
			c.report("E0211", "`Dynamic` in inferred type-parameter position — pass the value through reflect.box / reflect.unbox explicitly", call.Span)
		}
	}

	params := make([]Type, len(fn.Params))
	for i, p := range fn.Params {
		params[i] = substitute(p, subst)
	}
	return &Func{Params: params, Return: substitute(fn.Return, subst), Variadic: fn.Variadic}
}

// unifyArg binds the generic parameters appearing in `param`
// against the concrete argument type `arg`, accumulating into subst.
// A parameter already bound keeps its first binding (left-to-right),
// so a later conflicting argument surfaces as an E0201 mismatch
// against the substituted parameter rather than silently rebinding.
func unifyArg(param, arg Type, subst map[string]Type) {
	switch p := param.(type) {
	case *Generic:
		if _, bound := subst[p.Name]; !bound && concrete(arg) {
			subst[p.Name] = arg
		}
	case *Slice:
		if a, ok := arg.(*Slice); ok {
			unifyArg(p.Elem, a.Elem, subst)
		}
	case *Map:
		if a, ok := arg.(*Map); ok {
			unifyArg(p.Key, a.Key, subst)
			unifyArg(p.Val, a.Val, subst)
		}
	case *Set:
		if a, ok := arg.(*Set); ok {
			unifyArg(p.Elem, a.Elem, subst)
		}
	case *Stack:
		if a, ok := arg.(*Stack); ok {
			unifyArg(p.Elem, a.Elem, subst)
		}
	}
}

// substitute replaces every Generic{name} in t with subst[name]
// (when bound), recursing through the structured types.
func substitute(t Type, subst map[string]Type) Type {
	switch x := t.(type) {
	case *Generic:
		if r, ok := subst[x.Name]; ok {
			return r
		}
		return t
	case *Slice:
		return &Slice{Elem: substitute(x.Elem, subst)}
	case *Map:
		return &Map{Key: substitute(x.Key, subst), Val: substitute(x.Val, subst)}
	case *Set:
		return &Set{Elem: substitute(x.Elem, subst)}
	case *Stack:
		return &Stack{Elem: substitute(x.Elem, subst)}
	case *Func:
		ps := make([]Type, len(x.Params))
		for i, p := range x.Params {
			ps[i] = substitute(p, subst)
		}
		return &Func{Params: ps, Return: substitute(x.Return, subst), TypeParams: x.TypeParams, Variadic: x.Variadic}
	default:
		return t
	}
}
