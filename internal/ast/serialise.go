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
	case *FuncDecl:
		b.WriteByte(' ')
		writeQuoted(b, v.Name)
		writeSpan(b, v.Span)
		b.WriteByte('\n')
		writeIndent(b, depth+1)
		b.WriteString("(params)")
		b.WriteByte('\n')
		write(b, v.Body, depth+1)
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
	case *ForStmt:
		writeSpan(b, v.Span)
		b.WriteByte('\n')
		write(b, v.Pattern, depth+1)
		b.WriteByte('\n')
		write(b, v.Iterable, depth+1)
		b.WriteByte('\n')
		write(b, v.Body, depth+1)
	case *IdentPat:
		b.WriteByte(' ')
		writeQuoted(b, v.Name)
		writeSpan(b, v.Span)
	case *IntLitExpr:
		b.WriteByte(' ')
		b.WriteString(strconv.FormatInt(v.Value, 10))
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
	case *Ident:
		b.WriteByte(' ')
		writeQuoted(b, v.Name)
		writeSpan(b, v.Span)
	case *Call:
		writeSpan(b, v.Span)
		b.WriteByte('\n')
		write(b, v.Callee, depth+1)
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
