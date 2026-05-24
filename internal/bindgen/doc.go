// Package bindgen generates Tide bindings by introspecting Go packages.
//
// Binding signatures are derived mechanically from go/packages type
// information, never hand-written (D6). Tide binds, it does not port (D3).
// See docs/architecture.md.
//
// Status: not implemented.
package bindgen
