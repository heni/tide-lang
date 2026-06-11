package ast

import (
	"fmt"
	"strconv"
	"strings"
)

// Canonical returns the test-contract.md §AST S-expression
// serialization of the node tree rooted at n.
//
// The form for each node:
//
//	(NodeKind [attrs ...] [children ...] @span)
//
// where attrs are quoted string literals or int constants in the
// order documented per node, children are nested forms, and span
// is "line:col-line:col" attached at the tail of the parens.
//
// Each child appears on its own line, indented by two spaces per
// nesting level. The form is deterministic — diffing two runs of
// the same input gives identical text.
func Canonical(n Node) string {
	var b strings.Builder
	write(&b, n, 0)
	return b.String()
}

func write(b *strings.Builder, n Node, depth int) {
	if n == nil {
		writeIndent(b, depth)
		b.WriteString("(nil)")
		return
	}
	writeIndent(b, depth)
	b.WriteByte('(')
	b.WriteString(n.NodeKind())

	switch v := n.(type) {
	case *File:
		writeSpan(b, v.Span)
		for _, im := range v.Imports {
			b.WriteByte('\n')
			write(b, im, depth+1)
		}
		for _, d := range v.Decls {
			b.WriteByte('\n')
			write(b, d, depth+1)
		}
	case *Import:
		b.WriteByte(' ')
		writeQuoted(b, v.Path)
		writeSpan(b, v.Span)
	case *ClassDecl:
		b.WriteByte(' ')
		writeQuoted(b, v.Name)
		writeSpan(b, v.Span)
		b.WriteByte('\n')
		writeIndent(b, depth+1)
		if len(v.TypeParams) == 0 {
			b.WriteString("(type-params)")
		} else {
			b.WriteString("(type-params")
			for _, tp := range v.TypeParams {
				b.WriteByte(' ')
				writeQuoted(b, tp)
			}
			b.WriteByte(')')
		}
		b.WriteByte('\n')
		writeIndent(b, depth+1)
		if len(v.Implements) == 0 {
			b.WriteString("(implements)")
		} else {
			b.WriteString("(implements")
			for _, it := range v.Implements {
				b.WriteByte('\n')
				write(b, it, depth+2)
			}
			b.WriteByte(')')
		}
		if len(v.Fields) == 0 {
			b.WriteByte('\n')
			writeIndent(b, depth+1)
			b.WriteString("(fields)")
		} else {
			for _, f := range v.Fields {
				b.WriteByte('\n')
				write(b, f, depth+1)
			}
		}
		if len(v.Methods) == 0 {
			b.WriteByte('\n')
			writeIndent(b, depth+1)
			b.WriteString("(methods)")
		} else {
			for _, m := range v.Methods {
				b.WriteByte('\n')
				write(b, m, depth+1)
			}
		}
	case *ClassField:
		b.WriteByte(' ')
		writeQuoted(b, v.Name)
		b.WriteByte(' ')
		b.WriteString(v.Mutability)
		writeSpan(b, v.Span)
		b.WriteByte('\n')
		write(b, v.DeclType, depth+1)
	case *InterfaceDecl:
		b.WriteByte(' ')
		writeQuoted(b, v.Name)
		writeSpan(b, v.Span)
		for _, e := range v.Extends {
			b.WriteByte('\n')
			writeIndent(b, depth+1)
			b.WriteString("(extends\n")
			write(b, e, depth+2)
			b.WriteByte(')')
		}
		for _, m := range v.Methods {
			b.WriteByte('\n')
			write(b, m, depth+1)
		}
	case *InterfaceMethodSig:
		b.WriteByte(' ')
		writeQuoted(b, v.Name)
		writeSpan(b, v.Span)
		for _, prm := range v.Params {
			b.WriteByte('\n')
			write(b, prm, depth+1)
		}
		if v.ReturnType != nil {
			b.WriteByte('\n')
			writeIndent(b, depth+1)
			b.WriteString("(return\n")
			write(b, v.ReturnType, depth+2)
			b.WriteByte(')')
		}
	case *Method:
		b.WriteByte(' ')
		writeQuoted(b, v.Name)
		if v.IsStatic {
			b.WriteString(" static")
		} else {
			b.WriteString(" instance")
		}
		writeSpan(b, v.Span)
		b.WriteByte('\n')
		writeIndent(b, depth+1)
		if len(v.Params) == 0 {
			b.WriteString("(params)")
		} else {
			b.WriteString("(params")
			for _, p := range v.Params {
				b.WriteByte('\n')
				write(b, p, depth+2)
			}
			b.WriteByte(')')
		}
		if v.ReturnType != nil {
			b.WriteByte('\n')
			writeIndent(b, depth+1)
			b.WriteString("(return\n")
			write(b, v.ReturnType, depth+2)
			b.WriteByte(')')
		}
		b.WriteByte('\n')
		write(b, v.Body, depth+1)
	case *FuncDecl:
		b.WriteByte(' ')
		writeQuoted(b, v.Name)
		writeSpan(b, v.Span)
		b.WriteByte('\n')
		writeIndent(b, depth+1)
		if len(v.TypeParams) == 0 {
			b.WriteString("(type-params)")
		} else {
			b.WriteString("(type-params")
			for _, tp := range v.TypeParams {
				b.WriteByte(' ')
				writeQuoted(b, tp)
			}
			b.WriteByte(')')
		}
		b.WriteByte('\n')
		writeIndent(b, depth+1)
		if len(v.Params) == 0 {
			b.WriteString("(params)")
		} else {
			b.WriteString("(params")
			for _, p := range v.Params {
				b.WriteByte('\n')
				write(b, p, depth+2)
			}
			b.WriteByte(')')
		}
		if v.ReturnType != nil {
			b.WriteByte('\n')
			writeIndent(b, depth+1)
			b.WriteString("(return\n")
			write(b, v.ReturnType, depth+2)
			b.WriteByte(')')
		}
		b.WriteByte('\n')
		write(b, v.Body, depth+1)
	case *Param:
		b.WriteByte(' ')
		writeQuoted(b, v.Name)
		writeSpan(b, v.Span)
		b.WriteByte('\n')
		write(b, v.DeclType, depth+1)
	case *NamedType:
		b.WriteByte(' ')
		writeQuoted(b, strings.Join(v.QName, "."))
		writeSpan(b, v.Span)
		for _, a := range v.Args {
			b.WriteByte('\n')
			write(b, a, depth+1)
		}
	case *LetStmt:
		writeSpan(b, v.Span)
		b.WriteByte('\n')
		write(b, v.Pattern, depth+1)
		if v.DeclType != nil {
			b.WriteByte('\n')
			writeIndent(b, depth+1)
			b.WriteString("(type\n")
			write(b, v.DeclType, depth+2)
			b.WriteByte(')')
		}
		b.WriteByte('\n')
		write(b, v.Value, depth+1)
	case *VarStmt:
		b.WriteByte(' ')
		writeQuoted(b, v.Name)
		writeSpan(b, v.Span)
		if v.DeclType != nil {
			b.WriteByte('\n')
			writeIndent(b, depth+1)
			b.WriteString("(type\n")
			write(b, v.DeclType, depth+2)
			b.WriteByte(')')
		}
		if v.Value != nil {
			b.WriteByte('\n')
			write(b, v.Value, depth+1)
		}
	case *TopLevelLet:
		b.WriteByte(' ')
		writeQuoted(b, v.Name)
		writeSpan(b, v.Span)
		if v.DeclType != nil {
			b.WriteByte('\n')
			writeIndent(b, depth+1)
			b.WriteString("(type\n")
			write(b, v.DeclType, depth+2)
			b.WriteByte(')')
		}
		b.WriteByte('\n')
		write(b, v.Value, depth+1)
	case *AssignStmt:
		writeSpan(b, v.Span)
		b.WriteByte('\n')
		write(b, v.LValue, depth+1)
		b.WriteByte('\n')
		write(b, v.Value, depth+1)
	case *ReturnExpr:
		writeSpan(b, v.Span)
		if v.Value != nil {
			b.WriteByte('\n')
			write(b, v.Value, depth+1)
		}
	case *PrimitiveType:
		b.WriteByte(' ')
		writeQuoted(b, v.Name)
		writeSpan(b, v.Span)
	case *SliceType:
		writeSpan(b, v.Span)
		b.WriteByte('\n')
		write(b, v.Elem, depth+1)
	case *TupleType:
		writeSpan(b, v.Span)
		for _, c := range v.Components {
			b.WriteByte('\n')
			write(b, c, depth+1)
		}
	case *FuncType:
		writeSpan(b, v.Span)
		for _, p := range v.Params {
			b.WriteByte('\n')
			write(b, p, depth+1)
		}
		if v.ReturnType != nil {
			b.WriteByte('\n')
			writeIndent(b, depth+1)
			b.WriteString("(return\n")
			write(b, v.ReturnType, depth+2)
			b.WriteByte(')')
		}
	case *ClosureLit:
		if v.Short {
			b.WriteString(" short")
		}
		writeSpan(b, v.Span)
		for _, p := range v.Params {
			b.WriteByte('\n')
			write(b, p, depth+1)
		}
		if v.ReturnType != nil {
			b.WriteByte('\n')
			writeIndent(b, depth+1)
			b.WriteString("(return\n")
			write(b, v.ReturnType, depth+2)
			b.WriteByte(')')
		}
		b.WriteByte('\n')
		write(b, v.Body, depth+1)
	case *TupleLit:
		writeSpan(b, v.Span)
		for _, c := range v.Components {
			b.WriteByte('\n')
			write(b, c, depth+1)
		}
	case *BraceLit:
		b.WriteByte(' ')
		b.WriteString(string(v.Kind))
		writeSpan(b, v.Span)
		b.WriteByte('\n')
		write(b, v.TypeName, depth+1)
		for _, e := range v.Entries {
			b.WriteByte('\n')
			write(b, e, depth+1)
		}
	case *RecordEntry:
		b.WriteByte(' ')
		writeQuoted(b, v.Name)
		writeSpan(b, v.Span)
		b.WriteByte('\n')
		write(b, v.Value, depth+1)
	case *MapEntry:
		writeSpan(b, v.Span)
		b.WriteByte('\n')
		write(b, v.Key, depth+1)
		b.WriteByte('\n')
		write(b, v.Value, depth+1)
	case *SetEntry:
		writeSpan(b, v.Span)
		b.WriteByte('\n')
		write(b, v.Value, depth+1)
	case *TupleField:
		b.WriteByte(' ')
		b.WriteString(strconv.Itoa(v.Position))
		writeSpan(b, v.Span)
		b.WriteByte('\n')
		write(b, v.Receiver, depth+1)
	case *SliceLit:
		writeSpan(b, v.Span)
		if v.ElemType != nil {
			b.WriteByte('\n')
			writeIndent(b, depth+1)
			b.WriteString("(elem-type\n")
			write(b, v.ElemType, depth+2)
			b.WriteByte(')')
		}
		for _, it := range v.Items {
			b.WriteByte('\n')
			write(b, it, depth+1)
		}
	case *Index:
		writeSpan(b, v.Span)
		b.WriteByte('\n')
		write(b, v.Receiver, depth+1)
		b.WriteByte('\n')
		write(b, v.Idx, depth+1)
	case *Slice:
		writeSpan(b, v.Span)
		b.WriteByte('\n')
		write(b, v.Receiver, depth+1)
		b.WriteByte('\n')
		writeIndent(b, depth+1)
		if v.Low == nil {
			b.WriteString("(low nil)")
		} else {
			b.WriteString("(low\n")
			write(b, v.Low, depth+2)
			b.WriteByte(')')
		}
		b.WriteByte('\n')
		writeIndent(b, depth+1)
		if v.High == nil {
			b.WriteString("(high nil)")
		} else {
			b.WriteString("(high\n")
			write(b, v.High, depth+2)
			b.WriteByte(')')
		}
	case *TypeDecl:
		b.WriteByte(' ')
		writeQuoted(b, v.Name)
		writeSpan(b, v.Span)
		b.WriteByte('\n')
		write(b, v.Body, depth+1)
	case *AliasBody:
		writeSpan(b, v.Span)
		b.WriteByte('\n')
		write(b, v.Aliased, depth+1)
	case *SumTypeBody:
		writeSpan(b, v.Span)
		for _, vr := range v.Variants {
			b.WriteByte('\n')
			write(b, vr, depth+1)
		}
	case *Variant:
		b.WriteByte(' ')
		writeQuoted(b, v.Name)
		writeSpan(b, v.Span)
		for _, f := range v.Fields {
			b.WriteByte('\n')
			write(b, f, depth+1)
		}
	case *RecordTypeBody:
		writeSpan(b, v.Span)
		for _, fd := range v.Fields {
			b.WriteByte('\n')
			write(b, fd, depth+1)
		}
	case *FieldDecl:
		b.WriteByte(' ')
		writeQuoted(b, v.Name)
		writeSpan(b, v.Span)
		b.WriteByte('\n')
		write(b, v.DeclType, depth+1)
	case *MatchExpr:
		writeSpan(b, v.Span)
		b.WriteByte('\n')
		write(b, v.Subject, depth+1)
		for _, arm := range v.Arms {
			b.WriteByte('\n')
			write(b, arm, depth+1)
		}
	case *MatchArm:
		writeSpan(b, v.Span)
		b.WriteByte('\n')
		write(b, v.Pattern, depth+1)
		b.WriteByte('\n')
		write(b, v.Body, depth+1)
	case *TuplePat:
		writeSpan(b, v.Span)
		for _, s := range v.Sub {
			b.WriteByte('\n')
			write(b, s, depth+1)
		}
	case *WildcardPat:
		writeSpan(b, v.Span)
	case *IntLitPat:
		b.WriteByte(' ')
		b.WriteString(strconv.FormatInt(v.Value, 10))
		writeSpan(b, v.Span)
	case *FloatLitPat:
		b.WriteByte(' ')
		b.WriteString(v.RawText)
		writeSpan(b, v.Span)
	case *StringLitPat:
		b.WriteByte(' ')
		writeQuoted(b, v.Value)
		writeSpan(b, v.Span)
	case *BoolLitPat:
		b.WriteByte(' ')
		if v.Value {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
		writeSpan(b, v.Span)
	case *RuneLitPat:
		b.WriteByte(' ')
		b.WriteString(v.RawText)
		writeSpan(b, v.Span)
	case *VariantPat:
		b.WriteByte(' ')
		writeQuoted(b, strings.Join(v.QName, "."))
		writeSpan(b, v.Span)
		for _, s := range v.Sub {
			b.WriteByte('\n')
			write(b, s, depth+1)
		}
	case *AltPat:
		writeSpan(b, v.Span)
		for _, a := range v.Atoms {
			b.WriteByte('\n')
			write(b, a, depth+1)
		}
	case *Block:
		writeSpan(b, v.Span)
		for _, s := range v.Stmts {
			b.WriteByte('\n')
			write(b, s, depth+1)
		}
		if v.Trailing != nil {
			b.WriteByte('\n')
			writeIndent(b, depth+1)
			b.WriteString("(trailing\n")
			write(b, v.Trailing, depth+2)
			b.WriteByte(')')
		}
	case *ExprStmt:
		writeSpan(b, v.Span)
		b.WriteByte('\n')
		write(b, v.Expr, depth+1)
	case *IfStmt:
		writeSpan(b, v.Span)
		b.WriteByte('\n')
		write(b, v.Cond, depth+1)
		b.WriteByte('\n')
		write(b, v.ThenBlock, depth+1)
		if v.Else != nil {
			b.WriteByte('\n')
			writeIndent(b, depth+1)
			b.WriteString("(else\n")
			write(b, v.Else, depth+2)
			b.WriteByte(')')
		}
	case *IfExpr:
		writeSpan(b, v.Span)
		b.WriteByte('\n')
		write(b, v.Cond, depth+1)
		b.WriteByte('\n')
		write(b, v.ThenBlock, depth+1)
		if v.Else != nil {
			b.WriteByte('\n')
			writeIndent(b, depth+1)
			b.WriteString("(else\n")
			write(b, v.Else, depth+2)
			b.WriteByte(')')
		}
	case *ForStmt:
		writeSpan(b, v.Span)
		b.WriteByte('\n')
		write(b, v.Pattern, depth+1)
		b.WriteByte('\n')
		write(b, v.Iterable, depth+1)
		b.WriteByte('\n')
		write(b, v.Body, depth+1)
	case *WhileStmt:
		writeSpan(b, v.Span)
		b.WriteByte('\n')
		write(b, v.Cond, depth+1)
		b.WriteByte('\n')
		write(b, v.Body, depth+1)
	case *DeferStmt:
		writeSpan(b, v.Span)
		b.WriteByte('\n')
		write(b, v.Call, depth+1)
	case *SelectStmt:
		writeSpan(b, v.Span)
		for _, sc := range v.Cases {
			b.WriteByte('\n')
			write(b, sc, depth+1)
		}
	case *SelectRecv:
		if v.Bind != "" {
			b.WriteByte(' ')
			writeQuoted(b, v.Bind)
		}
		writeSpan(b, v.Span)
		b.WriteByte('\n')
		write(b, v.Channel, depth+1)
		b.WriteByte('\n')
		write(b, v.Body, depth+1)
	case *SelectSend:
		writeSpan(b, v.Span)
		b.WriteByte('\n')
		write(b, v.Channel, depth+1)
		b.WriteByte('\n')
		write(b, v.Value, depth+1)
		b.WriteByte('\n')
		write(b, v.Body, depth+1)
	case *SelectDefault:
		writeSpan(b, v.Span)
		b.WriteByte('\n')
		write(b, v.Body, depth+1)
	case *BreakExpr:
		writeSpan(b, v.Span)
	case *ContinueExpr:
		writeSpan(b, v.Span)
	case *UnitLit:
		writeSpan(b, v.Span)
	case *ParenExpr:
		writeSpan(b, v.Span)
		b.WriteByte('\n')
		write(b, v.Inner, depth+1)
	case *IdentPat:
		b.WriteByte(' ')
		writeQuoted(b, v.Name)
		writeSpan(b, v.Span)
	case *IntLitExpr:
		b.WriteByte(' ')
		b.WriteString(strconv.FormatInt(v.Value, 10))
		writeSpan(b, v.Span)
	case *FloatLitExpr:
		b.WriteByte(' ')
		b.WriteString(v.RawText)
		writeSpan(b, v.Span)
	case *StringLitExpr:
		b.WriteByte(' ')
		writeQuoted(b, v.Value)
		writeSpan(b, v.Span)
	case *BoolLitExpr:
		b.WriteByte(' ')
		if v.Value {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
		writeSpan(b, v.Span)
	case *ThisExpr:
		writeSpan(b, v.Span)
	case *Ident:
		b.WriteByte(' ')
		writeQuoted(b, v.Name)
		writeSpan(b, v.Span)
	case *Call:
		writeSpan(b, v.Span)
		b.WriteByte('\n')
		write(b, v.Callee, depth+1)
		if len(v.TypeArgs) > 0 {
			b.WriteByte('\n')
			writeIndent(b, depth+1)
			b.WriteString("(type-args")
			for _, ta := range v.TypeArgs {
				b.WriteByte('\n')
				write(b, ta, depth+2)
			}
			b.WriteByte(')')
		}
		for _, a := range v.Args {
			b.WriteByte('\n')
			write(b, a, depth+1)
		}
	case *Field:
		b.WriteByte(' ')
		writeQuoted(b, v.Name)
		writeSpan(b, v.Span)
		b.WriteByte('\n')
		write(b, v.Receiver, depth+1)
	case *Binary:
		b.WriteByte(' ')
		writeQuoted(b, v.Op)
		writeSpan(b, v.Span)
		b.WriteByte('\n')
		write(b, v.Left, depth+1)
		b.WriteByte('\n')
		write(b, v.Right, depth+1)
	case *Unary:
		b.WriteByte(' ')
		writeQuoted(b, v.Op)
		writeSpan(b, v.Span)
		b.WriteByte('\n')
		write(b, v.Operand, depth+1)
	case *TryExpr:
		writeSpan(b, v.Span)
		b.WriteByte('\n')
		write(b, v.Inner, depth+1)
	case *ScopeExpr:
		writeSpan(b, v.Span)
		for _, ta := range v.TypeArgs {
			b.WriteByte('\n')
			write(b, ta, depth+1)
		}
		if v.Parent != nil {
			b.WriteByte('\n')
			writeIndent(b, depth+1)
			b.WriteString("(parent\n")
			write(b, v.Parent, depth+2)
			b.WriteByte(')')
		}
		b.WriteByte('\n')
		write(b, v.Body, depth+1)
	case *SpawnExpr:
		writeSpan(b, v.Span)
		b.WriteByte('\n')
		write(b, v.Body, depth+1)
	case *RangeExpr:
		if v.Inclusive {
			b.WriteString(" inclusive")
		} else {
			b.WriteString(" exclusive")
		}
		writeSpan(b, v.Span)
		b.WriteByte('\n')
		write(b, v.Low, depth+1)
		b.WriteByte('\n')
		write(b, v.High, depth+1)
	default:
		b.WriteString(" ; <unhandled-node-kind>")
	}

	b.WriteByte(')')
}

func writeIndent(b *strings.Builder, depth int) {
	for i := 0; i < depth; i++ {
		b.WriteString("  ")
	}
}

func writeSpan(b *strings.Builder, s Span) {
	fmt.Fprintf(b, " @%d:%d-%d:%d", s.StartLine, s.StartCol, s.EndLine, s.EndCol)
}

func writeQuoted(b *strings.Builder, s string) {
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		case '\r':
			b.WriteString(`\r`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
}
