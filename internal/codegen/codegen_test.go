package codegen

import (
	"go/format"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/heni/tide-lang/internal/lexer"
	"github.com/heni/tide-lang/internal/parser"
)

func emitString(t *testing.T, src string) string {
	t.Helper()
	return emitWithFile(t, src, "")
}

func emitWithFile(t *testing.T, src, file string) string {
	t.Helper()
	toks, lerr := lexer.Lex(src)
	if lerr != nil {
		t.Fatalf("lex error: %v", lerr)
	}
	f, perr := parser.Parse(toks)
	if perr != nil {
		t.Fatalf("parse error: %v", perr)
	}
	out, err := Emit(f, file)
	if err != nil {
		t.Fatalf("emit error: %v", err)
	}
	return out
}

func TestEmitHello(t *testing.T) {
	src := `import fmt

func main() {
  fmt.println("Tide is rising.")
}
`
	got := emitString(t, src)
	want := `package main

import "fmt"

func main() {
	fmt.Println("Tide is rising.")
}
`
	if got != want {
		t.Errorf("emit mismatch:\n--- got ---\n%s--- want ---\n%s", got, want)
	}
}

func TestEmitFizzBuzz(t *testing.T) {
	src := `import fmt

func main() {
  for i in 1..=100 {
    if i % 15 == 0 {
      fmt.println("FizzBuzz")
    } else if i % 3 == 0 {
      fmt.println("Fizz")
    } else if i % 5 == 0 {
      fmt.println("Buzz")
    } else {
      fmt.println(i)
    }
  }
}
`
	got := emitString(t, src)
	// Smoke check: contains the inclusive-range termination,
	// the chained else-if, and the Go-side fmt.Println calls.
	for _, fragment := range []string{
		"package main",
		"import \"fmt\"",
		"for i := 1; i <= 100; i++ {",
		"if i%15 == 0 {",
		"} else if i%3 == 0 {",
		"} else if i%5 == 0 {",
		"} else {",
		"fmt.Println(\"FizzBuzz\")",
		"fmt.Println(\"Fizz\")",
		"fmt.Println(\"Buzz\")",
		"fmt.Println(i)",
	} {
		if !strings.Contains(got, fragment) {
			t.Errorf("emit missing fragment %q. Full:\n%s", fragment, got)
		}
	}
}

// TestEmitGofmtStable verifies the gofmt -s round-trip property
// stated in lang-spec/test-contract.md §GO and
// lang-spec/lowering-go.md §Output formatting.
func TestEmitGofmtStable(t *testing.T) {
	cases := []string{
		`func main() {
  fmt.println("hi")
}`,
		`func main() {
  for i in 0..10 {
    if i % 2 == 0 {
      fmt.println(i)
    }
  }
}`,
	}
	for _, src := range cases {
		out := emitString(t, "import fmt\n\n"+src+"\n")
		formatted, err := format.Source([]byte(out))
		if err != nil {
			t.Errorf("emitted code does not parse:\n%s\nerror: %v", out, err)
			continue
		}
		if string(formatted) != out {
			t.Errorf("emit is not gofmt-stable.\n--- emit ---\n%s\n--- gofmt ---\n%s",
				out, formatted)
		}
	}
}

// TestEmitCompiles writes the emitted Go to a temp file and runs
// `go build` on it; failure points at a real codegen bug.
func TestEmitCompiles(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available; skip compile check")
	}
	src := `import fmt

func main() {
  fmt.println("hi")
}
`
	out := emitString(t, src)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(out), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	// Need a go.mod so `go build` works on the temp dir.
	mod := "module tide-codegen-test\n\ngo 1.22\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(mod), 0644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	cmd := exec.Command("go", "build", "-o", "/dev/null", "./...")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("go build failed: %v\n%s", err, out)
	}
}

// TestEmitWithFilePath exercises the load-bearing //line path
// (D8 / lowering-go.md §Source maps). Verifies (a) the directives
// appear, (b) the output remains gofmt-stable, (c) Go still
// compiles it.
func TestEmitWithFilePath(t *testing.T) {
	src := `import fmt

func main() {
  for i in 1..=3 {
    if i == 1 {
      fmt.println("a")
    } else if i == 2 {
      fmt.println("b")
    } else {
      fmt.println("c")
    }
  }
}
`
	out := emitWithFile(t, src, "src.td")
	// Must contain //line directives — at least one per
	// top-level statement, and one for each else-if's nested
	// condition.
	if !strings.Contains(out, "//line src.td:3:1") {
		t.Errorf("missing //line for func main: %s", out)
	}
	if !strings.Contains(out, "//line src.td:4:1") {
		t.Errorf("missing //line for the for-loop: %s", out)
	}
	if !strings.Contains(out, "//line src.td:5:1") {
		t.Errorf("missing //line for the if condition: %s", out)
	}
	if !strings.Contains(out, "//line src.td:7:1") {
		t.Errorf("missing //line for else-if at line 7: %s", out)
	}
	// gofmt-stability — Emit already pipes through go/format, so
	// it should be byte-stable. Confirm.
	formatted, err := format.Source([]byte(out))
	if err != nil {
		t.Fatalf("emit-with-//line failed to parse: %v", err)
	}
	if string(formatted) != out {
		t.Errorf("emit-with-//line is not gofmt-stable:\n--- emit ---\n%s\n--- gofmt ---\n%s", out, formatted)
	}
	// Compile sanity.
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available; skip compile check")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(out), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module tide-codegen-test\n\ngo 1.22\n"), 0644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	cmd := exec.Command("go", "build", "-o", "/dev/null", "./...")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("emit-with-//line failed to compile: %v\n%s", err, out)
	}
}

// TestEmitRunHello does a full compile-and-run, checking STDOUT
// matches the expected line. PR-D will wire this into the
// fixture runner.
func TestEmitRunHello(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available")
	}
	src := `import fmt

func main() {
  fmt.println("Tide is rising.")
}
`
	out := emitString(t, src)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(out), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	mod := "module tide-codegen-test\n\ngo 1.22\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(mod), 0644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	cmd := exec.Command("go", "run", "./...")
	cmd.Dir = dir
	stdout, err := cmd.Output()
	if err != nil {
		t.Fatalf("go run failed: %v", err)
	}
	if got := string(stdout); got != "Tide is rising.\n" {
		t.Errorf("hello stdout = %q; want %q", got, "Tide is rising.\n")
	}
}
