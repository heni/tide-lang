// Package ast defines the Tide abstract syntax tree.
//
// Contract: lang-spec/ast.md. Every node carries a Span. The
// canonical serialization for fixtures is the S-expression form
// defined in lang-spec/test-contract.md §AST.
//
// v1 PR-B scope: only the subset needed by examples/core-language/hello/hello.td and
// examples/core-language/fizzbuzz/fizzbuzz.td. The full schema (sum types,
// classes, scope/spawn, …) lands in later PRs as the parser /
// sema / codegen grow.
package ast
