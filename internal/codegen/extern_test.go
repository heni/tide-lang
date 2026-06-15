package codegen

import (
	"testing"

	"github.com/heni/tide-lang/internal/ast"
)

// The `@go` split is the one piece of non-trivial logic the FFI lowering
// owns (ffi.md §GoRef): import path vs symbol on the last `.` after the
// last `/`, with a case-converted default symbol.

func TestGoRefPkgSym(t *testing.T) {
	cases := []struct {
		raw, tideName  string
		wantPkg, wantS string
	}{
		{"os/exec.Command", "command", "os/exec", "Command"},
		{"regexp.MustCompile", "mustCompile", "regexp", "MustCompile"},
		{"path/filepath.Join", "join", "path/filepath", "Join"},
		{"regexp", "Regexp", "regexp", "Regexp"}, // bare path → default symbol
		{"os/exec", "Cmd", "os/exec", "Cmd"},     // bare nested path → default symbol
		{"encoding/json.Marshal", "m", "encoding/json", "Marshal"},
	}
	for _, c := range cases {
		ref := &ast.GoRef{Raw: c.raw}
		pkg, sym := goRefPkgSym(ref, c.tideName)
		if pkg != c.wantPkg || sym != c.wantS {
			t.Errorf("goRefPkgSym(%q, %q) = (%q, %q); want (%q, %q)",
				c.raw, c.tideName, pkg, sym, c.wantPkg, c.wantS)
		}
	}
	// Absent attribute → empty package (an error at the use site) and a
	// case-converted default symbol.
	if pkg, sym := goRefPkgSym(nil, "command"); pkg != "" || sym != "Command" {
		t.Errorf("goRefPkgSym(nil, command) = (%q, %q); want (\"\", Command)", pkg, sym)
	}
}

func TestGoPkgRef(t *testing.T) {
	cases := map[string]string{
		"regexp":        "regexp",
		"os/exec":       "exec",
		"path/filepath": "filepath",
		"encoding/json": "json",
	}
	for in, want := range cases {
		if got := goPkgRef(in); got != want {
			t.Errorf("goPkgRef(%q) = %q; want %q", in, got, want)
		}
	}
}

func TestGoRefMember(t *testing.T) {
	if got := goRefMember(&ast.GoRef{Raw: "MatchString"}, "matchString"); got != "MatchString" {
		t.Errorf("explicit member = %q; want MatchString", got)
	}
	if got := goRefMember(nil, "matchString"); got != "MatchString" {
		t.Errorf("default member = %q; want MatchString", got)
	}
}
