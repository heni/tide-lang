// Package parser turns a Tide token stream into an AST.
//
// Contract: lang-spec/grammar.ebnf (syntactic part) and lang-spec/ast.md.
// PR-B scope: only the productions needed by examples/core-language/hello/hello.td and
// examples/core-language/fizzbuzz/fizzbuzz.td (imports, FuncDecl with empty
// param list, Block of ExprStmt / IfStmt / ForStmt, ranges,
// binary ops, calls, field access, ident/int/string/bool
// literals).
package parser
