// Package codegen lowers a Tide AST to Go source.
//
// Contract: lang-spec/lowering-go.md. PR-C scope: the same
// hello.td / fizzbuzz.td subset the parser handles. Generated Go
// is gofmt-stable (round-trips through gofmt -s to itself) per
// test-contract.md §GO.
//
// Binding shortcut for PR-C: `fmt.println` lowers directly to a
// call into Go's stdlib `fmt.Println` (with a hardcoded
// Tide→Go method-name map for the small set hello/fizzbuzz use).
// The full bindgen pipeline lands in a later PR.
package codegen
