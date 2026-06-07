package codegen

import "testing"

// TestStdlibRenameOf locks the rename registry's gating: a stdlib
// namespace + known method renames to the Go identifier; a non-stdlib
// receiver or an unknown method does not. It also guards the boundary
// with the conversion bindings — `strings.fromBytes` must NOT be a
// rename (it lowers to a Go conversion, handled separately), or the
// import-tracking and call lowering would diverge.
func TestStdlibRenameOf(t *testing.T) {
	cases := []struct {
		recv, name string
		want       string
		ok         bool
	}{
		{"strings", "split", "Split", true},
		{"strconv", "itoa", "Itoa", true},
		{"os", "args", "Args", true},
		{"fmt", "println", "Println", true},
		// Not renames: result-wrapping bindings go through emitCall.
		{"strconv", "atoi", "", false},
		{"os", "readFile", "", false},
		// Not a rename: conversion binding, lowered to `string(b)`.
		{"strings", "fromBytes", "", false},
		// Not a stdlib namespace.
		{"myObj", "split", "", false},
		// Unknown method on a real namespace.
		{"strings", "noSuchMethod", "", false},
	}
	for _, c := range cases {
		got, ok := stdlibRenameOf(c.recv, c.name)
		if got != c.want || ok != c.ok {
			t.Errorf("stdlibRenameOf(%q, %q) = (%q, %v); want (%q, %v)",
				c.recv, c.name, got, ok, c.want, c.ok)
		}
	}
}

// TestStdlibResultWrapOf locks the (T, error) → Result registry.
func TestStdlibResultWrapOf(t *testing.T) {
	if got, ok := stdlibResultWrapOf("strconv", "atoi"); !ok || got != "Atoi" {
		t.Errorf(`stdlibResultWrapOf("strconv","atoi") = (%q,%v); want ("Atoi",true)`, got, ok)
	}
	if got, ok := stdlibResultWrapOf("os", "readFile"); !ok || got != "ReadFile" {
		t.Errorf(`stdlibResultWrapOf("os","readFile") = (%q,%v); want ("ReadFile",true)`, got, ok)
	}
	if got, ok := stdlibResultWrapOf("strconv", "parseFloat"); !ok || got != "ParseFloat" {
		t.Errorf(`stdlibResultWrapOf("strconv","parseFloat") = (%q,%v); want ("ParseFloat",true)`, got, ok)
	}
	// A rename binding is not a result-wrap binding.
	if _, ok := stdlibResultWrapOf("strconv", "itoa"); ok {
		t.Errorf(`stdlibResultWrapOf("strconv","itoa") should be false`)
	}
}
