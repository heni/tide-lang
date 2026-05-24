// Package codegen lowers the type-checked Tide AST to Go source.
//
// Go is an intermediate representation (D1): generated Go need not be
// readable. Codegen emits //line directives so panics, stack traces, delve
// and pprof map back to .td source (D8).
//
// Status: not implemented. See docs/architecture.md.
package codegen
