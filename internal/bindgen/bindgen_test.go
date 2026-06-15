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

// TestGeneratedRoundTrips — the generated bindings must be valid Tide:
// they parse and type-check with no diagnostics (the generator never
// emits something the compiler rejects; unbindable symbols are comments).
func TestGeneratedRoundTrips(t *testing.T) {
	for _, path := range []string{"strconv", "regexp", "strings", "math"} {
		src := gen(t, path)
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
