package codegen

import (
	"strings"
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

// externResultKindOf drives the boundary lift (lowering-go.md
// §ForeignCall): a Go `(T, error)` referent (Tide `Result<T, error>`)
// lifts via tideResultOf; a bare-`error` referent (Tide
// `Result<unit, error>`) via tideResultUnit; anything else lowers bare.
func TestExternResultKindOf(t *testing.T) {
	result := func(args ...ast.TypeExpr) *ast.NamedType {
		return &ast.NamedType{QName: []string{"Result"}, Args: args}
	}
	prim := func(n string) *ast.PrimitiveType { return &ast.PrimitiveType{Name: n} }
	cases := []struct {
		name string
		rt   ast.TypeExpr
		want externResultKind
	}{
		{"value", result(prim("int"), prim("error")), resultValue},
		{"unit", result(prim("unit"), prim("error")), resultUnit},
		{"bare-result", result(), resultValue}, // no args → value (defensive)
		{"non-result-named", &ast.NamedType{QName: []string{"Option"}}, resultNone},
		{"primitive", prim("bool"), resultNone},
	}
	for _, c := range cases {
		if got := externResultKindOf(c.rt); got != c.want {
			t.Errorf("%s: externResultKindOf = %d; want %d", c.name, got, c.want)
		}
	}
}

// The method site of the unit lift (emitExternMethodCall) shares the
// classifier with the func site but is a distinct code path; assert a
// handle method returning Result<unit, error> lowers via tideResultUnit.
func TestExternMethodUnitLift(t *testing.T) {
	src := `import fmt

extern type Buffer @go("bytes")

extern func newBuffer(s: string): Buffer @go("bytes.NewBufferString")

extern impl Buffer {
  writeByte(c: byte): Result<unit, error> @go("WriteByte")
}

func main() {
  let b = newBuffer("")
  match b.writeByte(65) {
    Ok(_)  => fmt.println("ok"),
    Err(_) => fmt.println("err"),
  }
}
`
	got := emitString(t, src)
	if !strings.Contains(got, "tideResultUnit(b.WriteByte(") {
		t.Errorf("method unit lift not lowered via tideResultUnit:\n%s", got)
	}
}
