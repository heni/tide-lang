package sema

import (
	"strconv"

	"github.com/heni/tide-lang/internal/ast"
)

// checkBodies — Barrier C. Walks every function / method body
// after Barrier B has frozen the external surface, inferring a
// type for every expression (recorded in Info.Type) and emitting
// the typing diagnostics whose premises are local to a body.
//
// PR-Sema-C1 covers the scalar core: literal types, identifier /
// receiver types, arithmetic / logical operators, let / var / and
// assignment type agreement (E0201), return-type agreement
// (E0203), and call arity (E0202). Collection, conversion,
// Dynamic, exhaustiveness and context rules land in later PRs;
// every shape this PR cannot yet type degrades to *Unknown, so an
// unfinished checker never reports a false positive.
// See docs/internals/sema.md §4.
func (c *checker) checkBodies(f *ast.File) {
	// Type module-level constants first so any function/class body
	// that references one reads back a resolved type — top-level
	// bindings are visible everywhere (name-resolution.md §File scope).
	for _, d := range f.Decls {
		if tl, ok := d.(*ast.TopLevelLet); ok {
			c.checkTopLevelLet(tl)
		}
	}
	for _, d := range f.Decls {
		switch v := d.(type) {
		case *ast.FuncDecl:
			if v.Body != nil {
				c.curReturn = c.typeFromExpr(v.ReturnType)
				c.curThis = nil
				c.curTryForbidden = c.definitelyNotTryable(v.ReturnType)
				c.checkBlock(v.Body)
			}
		case *ast.ClassDecl:
			for _, m := range v.Methods {
				if m.Body == nil {
					continue
				}
				c.curReturn = c.typeFromExpr(m.ReturnType)
				c.curTryForbidden = c.definitelyNotTryable(m.ReturnType)
				if m.IsStatic {
					c.curThis = nil
				} else {
					c.curThis = &Named{N: v.Name, Decl: v}
				}
				c.checkBlock(m.Body)
			}
		}
	}
}

// checkTopLevelLet infers a module-level constant's initialiser and
// records the resolved type on its file-scope symbol (keyed in
// Info.Def at index time). `try` is illegal here — there is no
// enclosing Result/Option-returning frame (E0402 via curTryForbidden).
func (c *checker) checkTopLevelLet(tl *ast.TopLevelLet) {
	c.curReturn = nil
	c.curThis = nil
	c.curTryForbidden = true
	c.checkBinding(tl, nil, tl.DeclType, tl.Value)
}

func (c *checker) checkBlock(b *ast.Block) {
	c.inferBlock(b)
}

func (c *checker) checkStmt(s ast.Stmt) {
	switch v := s.(type) {
	case *ast.ExprStmt:
		c.inferExpr(v.Expr)
	case *ast.LetStmt:
		c.checkBinding(v, v.Pattern, v.DeclType, v.Value)
	case *ast.VarStmt:
		c.checkBinding(v, nil, v.DeclType, v.Value)
	case *ast.AssignStmt:
		lt := c.inferExpr(v.LValue)
		vt := c.inferExpr(v.Value)
		if !c.fits(lt, v.Value, vt) {
			c.report("E0201", "Type mismatch — cannot assign "+vt.String()+" to "+lt.String(), v.Span)
		}
	case *ast.IfStmt:
		c.inferExpr(v.Cond)
		if v.ThenBlock != nil {
			c.checkBlock(v.ThenBlock)
		}
		switch e := v.Else.(type) {
		case *ast.IfStmt:
			c.checkStmt(e)
		case *ast.Block:
			c.checkBlock(e)
		}
	case *ast.ForStmt:
		// Infer the iterable first so checkForBinding can read its
		// element type(s) to type the loop variable(s).
		var iterT Type
		switch it := v.Iterable.(type) {
		case *ast.RangeExpr:
			c.inferExpr(it.Low)
			c.inferExpr(it.High)
		case ast.Expr:
			iterT = c.inferExpr(it)
		}
		c.checkForBinding(v, iterT)
		if v.Body != nil {
			c.loopDepth++
			c.checkBlock(v.Body)
			c.loopDepth--
		}
	case *ast.WhileStmt:
		c.inferExpr(v.Cond)
		if v.Body != nil {
			c.loopDepth++
			c.checkBlock(v.Body)
			c.loopDepth--
		}
	case *ast.DeferStmt:
		// T-Defer: the deferred expression's AST shape must be a
		// Call (a closure-wrapped block is spelled `(() => …)()`,
		// still a Call). E0406 otherwise.
		if _, ok := v.Call.(*ast.Call); !ok {
			c.report("E0406", "`defer` argument must be a call", v.Call.NodeSpan())
		}
		c.inferExpr(v.Call)
	case *ast.SelectStmt:
		// T-Select: every case body is unit; a recv binding gets the
		// channel's element type.
		for _, sc := range v.Cases {
			switch cse := sc.(type) {
			case *ast.SelectRecv:
				ct := c.inferExpr(cse.Channel)
				if cse.Bind != "" && cse.Bind != "_" {
					if sym := c.info.Def[cse]; sym != nil {
						sym.Type = channelElem(ct)
					}
				}
				c.checkBlock(cse.Body)
			case *ast.SelectSend:
				c.inferExpr(cse.Channel)
				c.inferExpr(cse.Value)
				c.checkBlock(cse.Body)
			case *ast.SelectDefault:
				c.checkBlock(cse.Body)
			}
		}
	}
}

// channelElem returns the element type of a channel kind (Channel /
// RecvChan), or Unknown when t is not a receivable channel.
func channelElem(t Type) Type {
	switch c := t.(type) {
	case *Channel:
		return c.Elem
	case *RecvChan:
		return c.Elem
	}
	return &Unknown{}
}

// checkBinding handles let / var. bindNode is the AST node keyed
// in Info.Def for the introduced binding (the LetStmt's IdentPat
// or the VarStmt itself); a destructuring let with no single
// IdentPat passes a nil pattern and only type-checks the value.
func (c *checker) checkBinding(bindNode ast.Node, pat ast.Pattern, ann ast.TypeExpr, value ast.Expr) {
	var vt Type = &Unknown{}
	if value != nil {
		vt = c.inferExpr(value)
	}
	var declared Type
	if ann != nil {
		declared = c.typeFromExpr(ann)
		// Only compare against an actual initialiser; a bare
		// `var x: T` (no value) is a separate concern, not a
		// type mismatch. fits() applies integer-literal and
		// slice-literal narrowing and the E0204 range check.
		if value != nil && !c.fits(declared, value, vt) {
			c.report("E0201", "Type mismatch — annotation is "+declared.String()+" but value is "+vt.String(), value.NodeSpan())
		}
	}
	// The binding's static type is the annotation when present,
	// else the inferred value type. Hang it on the shared Symbol so
	// use sites read it back through Info.Symbol.
	bound := declared
	if bound == nil {
		bound = vt
	}
	c.setBindingType(bindNode, pat, bound)
}

// setBindingType records the resolved type on the binding's
// Symbol via Info.Def (LetStmt → its IdentPat; VarStmt → itself).
// A destructuring `let (a, b) = e` distributes the value's tuple
// components across the pattern (setPatternType).
func (c *checker) setBindingType(bindNode ast.Node, pat ast.Pattern, t Type) {
	if t == nil {
		return
	}
	if pat != nil {
		c.setPatternType(pat, t)
		return
	}
	if sym := c.info.Def[bindNode]; sym != nil {
		sym.Type = t
	}
}

// setPatternType types the binders of an irrefutable let/var pattern
// from t. An IdentPat takes t directly; a TuplePat distributes t's
// components when t is a Tuple of matching arity (T-Let-Destructure),
// reporting E0201 on an arity / non-tuple mismatch. A concretely-bad
// shape is diagnosed; an Unknown value type leaves binders Unknown.
func (c *checker) setPatternType(pat ast.Pattern, t Type) {
	switch p := pat.(type) {
	case *ast.IdentPat:
		if sym := c.info.Def[p]; sym != nil {
			sym.Type = t
		}
	case *ast.WildcardPat:
		// `_` binds nothing.
	case *ast.TuplePat:
		tup, ok := t.(*Tuple)
		if !ok {
			if concrete(t) {
				c.report("E0201", "Cannot destructure — value is "+t.String()+", not a tuple", p.Span)
			}
			return
		}
		if len(tup.Comps) != len(p.Sub) {
			c.report("E0201", "Tuple destructuring arity mismatch — pattern binds "+
				strconv.Itoa(len(p.Sub))+" but value has "+strconv.Itoa(len(tup.Comps)), p.Span)
			return
		}
		for i, sub := range p.Sub {
			c.setPatternType(sub, tup.Comps[i])
		}
	default:
		// Literal / variant components are refutable — a let/var
		// binding must be irrefutable (only names, `_`, nested tuples).
		c.report("E0201", "Refutable pattern in binding — `let`/`var` bind irrefutably; use `match` to test a value", pat.NodeSpan())
	}
}

// checkForBinding types a for-loop's variable(s) from the iterable's
// element type(s) (builtins.md §IterElem). A numeric range binds the
// single variable to int. A single variable over a collection takes
// its element (a Map yields its key). A two-variable tuple pattern
// takes the index/value pair: a slice yields `(int, Elem)` — the only
// indexed IterElem clause in the spec — and a Map yields `(Key, Val)`.
// iterT is the iterable's inferred type (nil for a range — handled by
// the AST check). Unmodelled shapes (Stack — not iterable in v1; a
// 2-tuple over a Set/Channel — no indexed clause) leave it Unknown.
func (c *checker) checkForBinding(f *ast.ForStmt, iterT Type) {
	if _, isRange := f.Iterable.(*ast.RangeExpr); isRange {
		if ip, ok := f.Pattern.(*ast.IdentPat); ok {
			c.setForVar(ip, &Builtin{N: "int"})
		}
		return
	}
	switch p := f.Pattern.(type) {
	case *ast.IdentPat:
		c.setForVar(p, iterSingleElem(iterT))
	case *ast.TuplePat:
		if len(p.Sub) != 2 {
			return
		}
		k, v := iterPairElems(iterT)
		if ip, ok := p.Sub[0].(*ast.IdentPat); ok {
			c.setForVar(ip, k)
		}
		if ip, ok := p.Sub[1].(*ast.IdentPat); ok {
			c.setForVar(ip, v)
		}
	}
}

// setForVar assigns t to the symbol an IdentPat introduced, when both
// the symbol and a non-nil type are available.
func (c *checker) setForVar(ip *ast.IdentPat, t Type) {
	if t == nil {
		return
	}
	if sym := c.info.Def[ip]; sym != nil {
		sym.Type = t
	}
}

// iterSingleElem returns the element a single loop variable binds to
// when iterating the given collection type — a Map yields its key
// (the short `for k in m` form). nil for an unmodelled iterable.
func iterSingleElem(t Type) Type {
	switch v := t.(type) {
	case *Slice:
		return v.Elem
	case *Set:
		return v.Elem
	case *Channel:
		return v.Elem
	case *RecvChan:
		return v.Elem
	case *Map:
		return v.Key
	}
	return nil
}

// iterPairElems returns the (index/key, value) pair a two-variable
// tuple pattern binds when iterating the given collection: a slice /
// set / stack indexes with int; a Map yields its (Key, Val). Returns
// (nil, nil) for an unmodelled iterable.
func iterPairElems(t Type) (Type, Type) {
	switch v := t.(type) {
	case *Slice:
		return &Builtin{N: "int"}, v.Elem
	case *Map:
		return v.Key, v.Val
	}
	return nil, nil
}
