package bindgen

import (
	"go/types"
	"strings"
	"testing"

	"github.com/heni/tide-lang/internal/lexer"
	"github.com/heni/tide-lang/internal/parser"
	"github.com/heni/tide-lang/internal/sema"
)

// gen runs Generate, skipping when the Go source importer is unavailable
// (no GOROOT source tree — e.g. a minimal CI image).
func gen(t *testing.T, path string) string {
	t.Helper()
	src, err := Generate(path)
	if err != nil {
		t.Skipf("bindgen %s unavailable in this environment: %v", path, err)
	}
	return src
}

// TestGenerateStrconv checks the canonical bindings and the boundary
// lifts / bail markers a hand-eye review expects from `strconv`.
func TestGenerateStrconv(t *testing.T) {
	src := gen(t, "strconv")
	wantContains := []string{
		`extern func atoi(s: string): Result<int, error> @go("strconv.Atoi")`,
		`extern func itoa(i: int): string @go("strconv.Itoa")`,
		`extern func formatBool(b: bool): string @go("strconv.FormatBool")`,
		"// UNBINDABLE formatComplex:", // complex param
		"// UNBINDABLE unquoteChar:",   // arity-≥3 results
	}
	for _, w := range wantContains {
		if !strings.Contains(src, w) {
			t.Errorf("strconv bindings missing %q\n--- got ---\n%s", w, src)
		}
	}
}

// TestGenerateDeterministic — re-running produces byte-identical output
// (the stable-diff-over-curation property).
func TestGenerateDeterministic(t *testing.T) {
	a := gen(t, "strconv")
	b, err := Generate("strconv")
	if err != nil {
		t.Skipf("unavailable: %v", err)
	}
	if a != b {
		t.Error("Generate(strconv) is not deterministic")
	}
}

// TestGenerateOpaqueHandle — a named type becomes an opaque `extern type`
// plus an `extern impl` of its exported methods, with the keyword-collision
// rename (`Match` → `match_`) preserving the Go symbol via @go.
func TestGenerateRegexpHandle(t *testing.T) {
	src := gen(t, "regexp")
	wantContains := []string{
		`extern type Regexp @go("regexp.Regexp")`,
		`extern func mustCompile(str: string): Regexp @go("regexp.MustCompile")`,
		`matchString(s: string): bool @go("MatchString")`,
		`match_(b: []byte): bool @go("Match")`, // `Match` → keyword-escaped
	}
	for _, w := range wantContains {
		if !strings.Contains(src, w) {
			t.Errorf("regexp bindings missing %q\n--- got ---\n%s", w, src)
		}
	}
}

// TestGenerateVariadic — a variadic Go signature (`exec.Command(name
// string, arg ...string)`) renders its final parameter as Tide's `...T`
// instead of bailing (the D23 unblocker; ffi.md §Variadic).
func TestGenerateVariadic(t *testing.T) {
	src := gen(t, "os/exec")
	want := `extern func command(name: string, arg: ...string): Cmd @go("os/exec.Command")`
	if !strings.Contains(src, want) {
		t.Errorf("os/exec bindings missing variadic %q\n--- got ---\n%s", want, src)
	}
	if strings.Contains(src, "Tide has no variadic") {
		t.Errorf("os/exec still emits the pre-D23 variadic bail marker\n--- got ---\n%s", src)
	}
}

// TestGeneratedRoundTrips — the generated bindings must be valid Tide:
// they parse and type-check with no diagnostics (the generator never
// emits something the compiler rejects; unbindable symbols are comments).
// Precondition: a package with ≥1 binding (an all-bail package yields a
// comment-only report, covered separately below).
func TestGeneratedRoundTrips(t *testing.T) {
	for _, path := range []string{"strconv", "regexp", "strings", "math"} {
		src := gen(t, path)
		if !HasBindings(src) {
			t.Errorf("%s: expected ≥1 binding to round-trip", path)
			continue
		}
		toks, lerr := lexer.LexFile(src, path+".td")
		if lerr != nil {
			t.Errorf("%s: generated bindings fail to lex: %v", path, lerr)
			continue
		}
		f, perr := parser.ParseFile(toks, path+".td")
		if perr != nil {
			t.Errorf("%s: generated bindings fail to parse: %v", path, perr)
			continue
		}
		if _, diags := sema.Check(f, path+".td"); len(diags) != 0 {
			t.Errorf("%s: generated bindings produced %d sema diagnostics: %v",
				path, len(diags), diags)
		}
	}
}

// TestAllBailPackage — a package whose every export bails yields a
// comment-only report carrying the "no bindable symbols" note, not a
// silently-uncompilable file. `crypto/sha256` is all-bail today (its
// digests are fixed-size arrays, its constructor returns the cross-
// package `hash.Hash` interface); the assertion is tolerant in case a
// future Go release adds a bindable symbol.
func TestAllBailPackage(t *testing.T) {
	src := gen(t, "crypto/sha256")
	if HasBindings(src) {
		t.Skip("crypto/sha256 gained a bindable symbol in this Go version")
	}
	if !strings.Contains(src, "No bindable symbols") {
		t.Errorf("all-bail package missing the no-bindable-symbols note\n%s", src)
	}
}

func TestTideName(t *testing.T) {
	cases := map[string]string{
		"Atoi":        "atoi",
		"MustCompile": "mustCompile",
		"Match":       "match_", // keyword collision
		"Type":        "type_",
		"URL":         "uRL", // first letter only, by convention
	}
	for in, want := range cases {
		if got := tideName(in); got != want {
			t.Errorf("tideName(%q) = %q; want %q", in, got, want)
		}
	}
}

func TestTranslateBasic(t *testing.T) {
	ok := map[types.BasicKind]string{
		types.Bool: "bool", types.Int: "int", types.Uint8: "byte",
		types.Int32: "int32", types.Float64: "float64", types.String: "string",
	}
	for kind, want := range ok {
		got, isOK, reason := translateBasic(types.Typ[kind])
		if !isOK || got != want {
			t.Errorf("translateBasic(%v) = (%q, %v, %q); want %q", kind, got, isOK, reason, want)
		}
	}
	for _, bad := range []types.BasicKind{types.Uintptr, types.Complex128, types.UnsafePointer} {
		if _, isOK, _ := translateBasic(types.Typ[bad]); isOK {
			t.Errorf("translateBasic(%v) should bail", bad)
		}
	}
}
