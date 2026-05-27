package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// projectRoot resolves the project root regardless of where the
// test runner sets the working directory.
func projectRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// cmd/tide/main_test.go → project root.
	return filepath.Join(filepath.Dir(file), "..", "..")
}

var (
	buildOnce sync.Once
	buildBin  string
	buildErr  error
)

// tideBinary builds (once per test process) a real tide binary so
// the test harness can read the inner exit code directly — `go
// run` wraps any non-zero exit as 1, masking the actual code.
// Uses a process-wide MkdirTemp (not t.TempDir) so all tests in
// the package share one binary.
func tideBinary(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		root := projectRoot(t)
		dir, err := os.MkdirTemp("", "tide-test-bin-*")
		if err != nil {
			buildErr = err
			return
		}
		bin := filepath.Join(dir, "tide-test")
		cmd := exec.Command("go", "build", "-o", bin, "./cmd/tide")
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		if err != nil {
			buildErr = err
			t.Logf("build output: %s", out)
			return
		}
		buildBin = bin
	})
	if buildErr != nil {
		t.Fatalf("build tide failed: %v", buildErr)
	}
	return buildBin
}

// runTide invokes the test-built tide binary from the project
// root and returns (stdout, stderr, exitCode).
func runTide(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	bin := tideBinary(t)
	cmd := exec.Command(bin, args...)
	cmd.Dir = projectRoot(t)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	exit := 0
	if ee, ok := err.(*exec.ExitError); ok {
		exit = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("tide failed unexpectedly: %v\n%s", err, stderr.String())
	}
	return stdout.String(), stderr.String(), exit
}

func TestHelloEndToEnd(t *testing.T) {
	stdout, stderr, exit := runTide(t, "run", "examples/hello.td")
	if exit != 0 {
		t.Fatalf("hello.td exited %d (stderr: %s)", exit, stderr)
	}
	if stdout != "Tide is rising.\n" {
		t.Errorf("hello.td stdout = %q; want \"Tide is rising.\\n\"", stdout)
	}
}

func TestFizzBuzzEndToEnd(t *testing.T) {
	stdout, _, exit := runTide(t, "run", "examples/interview/fizzbuzz.td")
	if exit != 0 {
		t.Fatalf("fizzbuzz.td exited %d", exit)
	}
	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	if len(lines) != 100 {
		t.Errorf("fizzbuzz.td produced %d lines; want 100", len(lines))
	}
	// Spot-check the canonical FizzBuzz expectations.
	for _, c := range []struct {
		idx  int // 1-indexed
		want string
	}{
		{1, "1"},
		{2, "2"},
		{3, "Fizz"},
		{5, "Buzz"},
		{15, "FizzBuzz"},
		{30, "FizzBuzz"},
		{100, "Buzz"},
	} {
		if lines[c.idx-1] != c.want {
			t.Errorf("fizzbuzz.td line %d = %q; want %q", c.idx, lines[c.idx-1], c.want)
		}
	}
}

func TestVersion(t *testing.T) {
	stdout, _, exit := runTide(t, "version")
	if exit != 0 {
		t.Fatalf("tide version exited %d", exit)
	}
	if !strings.HasPrefix(stdout, "tide ") {
		t.Errorf("tide version stdout = %q; want 'tide ' prefix", stdout)
	}
}

func TestUnknownSubcommand(t *testing.T) {
	_, stderr, exit := runTide(t, "frobnicate")
	if exit != 2 {
		t.Errorf("unknown subcommand exit = %d; want 2", exit)
	}
	if !strings.Contains(stderr, `unknown subcommand "frobnicate"`) {
		t.Errorf("expected helpful error in stderr; got %q", stderr)
	}
}

func TestRunMissingArg(t *testing.T) {
	_, stderr, exit := runTide(t, "run")
	if exit != 2 {
		t.Errorf("tide run (no args) exit = %d; want 2", exit)
	}
	if !strings.Contains(stderr, "expected exactly one <file.td>") {
		t.Errorf("expected usage hint in stderr; got %q", stderr)
	}
}

func TestEmitHello(t *testing.T) {
	stdout, _, exit := runTide(t, "emit", "examples/hello.td")
	if exit != 0 {
		t.Fatalf("tide emit hello.td exit = %d", exit)
	}
	// Spot-check: the lowered Go contains package main, fmt
	// import, the Println call, and at least one //line
	// directive pointing back at the .td source.
	for _, want := range []string{
		"package main",
		`import "fmt"`,
		`fmt.Println("Tide is rising.")`,
		"//line examples/hello.td:",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("emit output missing %q. Full output:\n%s", want, stdout)
		}
	}
}

func TestEmitMissingArg(t *testing.T) {
	_, stderr, exit := runTide(t, "emit")
	if exit != 2 {
		t.Errorf("tide emit (no args) exit = %d; want 2", exit)
	}
	if !strings.Contains(stderr, "expected exactly one") {
		t.Errorf("expected usage hint; got %q", stderr)
	}
}

// runTideStdin invokes the test-built tide binary with the
// given args, piping the supplied stdin string. Used by REPL
// tests that drive the interactive prompt non-interactively.
func runTideStdin(t *testing.T, stdin string, args ...string) (string, string, int) {
	t.Helper()
	bin := tideBinary(t)
	cmd := exec.Command(bin, args...)
	cmd.Dir = projectRoot(t)
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	exit := 0
	if ee, ok := err.(*exec.ExitError); ok {
		exit = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("tide failed unexpectedly: %v\n%s", err, stderr.String())
	}
	return stdout.String(), stderr.String(), exit
}

func TestReplPrintsHello(t *testing.T) {
	input := "import fmt\nfmt.println(\"hello from repl\")\n:quit\n"
	stdout, stderr, exit := runTideStdin(t, input, "repl")
	if exit != 0 {
		t.Fatalf("repl exit = %d (stderr: %s)", exit, stderr)
	}
	if !strings.Contains(stdout, "hello from repl") {
		t.Errorf("repl stdout missing user output; got:\n%s", stdout)
	}
}

func TestReplSilencesUnusedBinding(t *testing.T) {
	// `let x = 42` alone should NOT trip Go's
	// declared-and-not-used error — the session renderer
	// appends a `let _ = x` silence-use.
	input := "import fmt\nlet x = 42\nfmt.println(\"x =\", x)\n:quit\n"
	stdout, stderr, exit := runTideStdin(t, input, "repl")
	if exit != 0 {
		t.Fatalf("repl exit = %d (stderr: %s)", exit, stderr)
	}
	if !strings.Contains(stdout, "x = 42") {
		t.Errorf("repl stdout missing 'x = 42'; got:\n%s\n--stderr:\n%s", stdout, stderr)
	}
	if strings.Contains(stderr, "declared and not used") {
		t.Errorf("repl emitted Go-side unused-var error:\n%s", stderr)
	}
}

func TestReplRejectsTopLevelControlFlow(t *testing.T) {
	input := "if true { 1 }\n:quit\n"
	_, stderr, exit := runTideStdin(t, input, "repl")
	if exit != 0 {
		t.Fatalf("repl exit = %d", exit)
	}
	if !strings.Contains(stderr, "top-level control-flow") {
		t.Errorf("expected top-level control-flow rejection; got stderr:\n%s", stderr)
	}
}

func TestReplRollsBackCompileFailure(t *testing.T) {
	// First stmt fails to compile (undefined identifiers). The
	// REPL must roll it back so the follow-up `let y` works.
	input := "import fmt\nlet x = oh dear\nlet y = 99\nfmt.println(y)\n:quit\n"
	stdout, _, exit := runTideStdin(t, input, "repl")
	if exit != 0 {
		t.Fatalf("repl exit = %d", exit)
	}
	if !strings.Contains(stdout, "99") {
		t.Errorf("rollback did not restore session; stdout:\n%s", stdout)
	}
}

func TestReplMultiLineDecl(t *testing.T) {
	// A function declaration spread across continuation lines
	// must accumulate into one decl, then be callable from a
	// later input. Exercises balanced()'s tracking across `(` `{`.
	input := "import fmt\n" +
		"func multi(\n" +
		"  a: int,\n" +
		"  b: int\n" +
		") {\n" +
		"  fmt.println(a + b)\n" +
		"}\n" +
		"multi(2, 3)\n" +
		":quit\n"
	stdout, stderr, exit := runTideStdin(t, input, "repl")
	if exit != 0 {
		t.Fatalf("repl exit = %d (stderr: %s)", exit, stderr)
	}
	if !strings.Contains(stdout, "5") {
		t.Errorf("multi-line decl + call missing '5'; stdout:\n%s", stdout)
	}
}

func TestReplImportsListing(t *testing.T) {
	input := "import fmt\n:imports\n:quit\n"
	stdout, _, exit := runTideStdin(t, input, "repl")
	if exit != 0 {
		t.Fatalf("repl exit = %d", exit)
	}
	if !strings.Contains(stdout, "import fmt") {
		t.Errorf(":imports did not list fmt; stdout:\n%s", stdout)
	}
}

func TestReplMetaShowAndReset(t *testing.T) {
	input := "import fmt\nlet x = 1\n:show\n:reset\n:show\n:quit\n"
	stdout, _, exit := runTideStdin(t, input, "repl")
	if exit != 0 {
		t.Fatalf("repl exit = %d", exit)
	}
	// First :show must reflect the input; after :reset the
	// rendered session should be the empty main().
	if !strings.Contains(stdout, "import fmt") {
		t.Errorf("first :show missing import; stdout:\n%s", stdout)
	}
	if !strings.Contains(stdout, "(session cleared)") {
		t.Errorf("missing :reset acknowledgement; stdout:\n%s", stdout)
	}
}

func TestReplAutoPrintExpression(t *testing.T) {
	input := "1 + 2\n\"hi\"\n:quit\n"
	stdout, stderr, exit := runTideStdin(t, input, "repl")
	if exit != 0 {
		t.Fatalf("repl exit = %d (stderr: %s)", exit, stderr)
	}
	if !strings.Contains(stdout, "3") || !strings.Contains(stdout, `"hi"`) {
		t.Errorf("auto-print missing expected output; stdout:\n%s", stdout)
	}
}

func TestReplMetaType(t *testing.T) {
	input := ":type 42\n:quit\n"
	stdout, _, exit := runTideStdin(t, input, "repl")
	if exit != 0 {
		t.Fatalf("repl exit = %d", exit)
	}
	if !strings.Contains(stdout, "int") {
		t.Errorf(":type 42 should print 'int'; got:\n%s", stdout)
	}
}

func TestReplMetaInspect(t *testing.T) {
	input := "class Point { var x: int\n  var y: int }\n:inspect Point(3, 4)\n:quit\n"
	stdout, _, exit := runTideStdin(t, input, "repl")
	if exit != 0 {
		t.Fatalf("repl exit = %d", exit)
	}
	if !strings.Contains(stdout, "Point{x: 3, y: 4}") {
		t.Errorf(":inspect should pretty-print Point; got:\n%s", stdout)
	}
}

func TestReplCallStatementNotAutoPrinted(t *testing.T) {
	// `fmt.println(x)` is a side-effecting call that returns
	// `(int, error)`; auto-printing it would wrap the multi-
	// return in `reflect.box(...)` which doesn't compile. The
	// classifier must treat it as a plain statement.
	input := "import fmt\nlet x = 7\nfmt.println(\"x is\", x)\n:quit\n"
	stdout, stderr, exit := runTideStdin(t, input, "repl")
	if exit != 0 {
		t.Fatalf("repl exit = %d (stderr: %s)", exit, stderr)
	}
	if !strings.Contains(stdout, "x is 7") {
		t.Errorf("fmt.println output missing; stdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
}

func TestReplAutoPrintLastOnly(t *testing.T) {
	// Three bare expressions in sequence. Only the most-
	// recently-entered one prints its value on the latest run;
	// earlier auto-prints collapse to silent `let _ = (...)` so
	// the accumulating-source rerun does not replay all three
	// values every turn.
	input := "1 + 2\n3 + 4\n5 + 6\n:quit\n"
	stdout, _, exit := runTideStdin(t, input, "repl")
	if exit != 0 {
		t.Fatalf("repl exit = %d", exit)
	}
	// Each value `3`, `7`, `11` must appear exactly once — the
	// turn it became the latest input. If the auto-print
	// collapse to silent `let _ = (...)` were not in place,
	// `3` would print on every subsequent rerun.
	for _, want := range []string{"tide> 3\n", "tide> 7\n", "tide> 11\n"} {
		if got := strings.Count(stdout, want); got != 1 {
			t.Errorf("expected %q exactly once; got %d; stdout:\n%s", want, got, stdout)
		}
	}
}

func TestReplAssignmentNotAutoPrinted(t *testing.T) {
	// `x = 5` is an assignment, not a bare expression. It must
	// not trigger auto-print (which would wrap it as
	// `reflect.box((x = 5))` and fail to compile).
	input := "var x = 1\nx = 99\nx\n:quit\n"
	stdout, stderr, exit := runTideStdin(t, input, "repl")
	if exit != 0 {
		t.Fatalf("repl exit = %d (stderr: %s)", exit, stderr)
	}
	if !strings.Contains(stdout, "99") {
		t.Errorf("expected '99' from auto-print of x after assignment; stdout:\n%s", stdout)
	}
}

func TestReplAutoPrintAddsFmtImport(t *testing.T) {
	// User never typed `import fmt` or `import reflect`. The
	// auto-print machinery must add them silently so a bare
	// expression evaluates without further setup.
	input := "42\n:quit\n"
	stdout, _, exit := runTideStdin(t, input, "repl")
	if exit != 0 {
		t.Fatalf("repl exit = %d", exit)
	}
	if !strings.Contains(stdout, "42") {
		t.Errorf("auto-print did not add fmt/reflect; stdout:\n%s", stdout)
	}
}

func TestReplShowOriginalSource(t *testing.T) {
	// `:show` is the diagnostic aid — it must print the user's
	// original input, NOT the wrapped auto-print rendering.
	input := "1 + 2\n:show\n:quit\n"
	stdout, _, exit := runTideStdin(t, input, "repl")
	if exit != 0 {
		t.Fatalf("repl exit = %d", exit)
	}
	if !strings.Contains(stdout, "1 + 2") {
		t.Errorf(":show must include the user-typed expression; stdout:\n%s", stdout)
	}
	if strings.Contains(stdout, "reflect.box") {
		t.Errorf(":show must NOT leak the auto-print wrap; stdout:\n%s", stdout)
	}
}

func TestBuildOutputFlag(t *testing.T) {
	outPath := filepath.Join(t.TempDir(), "hello-bin")
	_, stderr, exit := runTide(t, "build", "-o", outPath, "examples/hello.td")
	if exit != 0 {
		t.Fatalf("tide build -o failed: %d (stderr: %s)", exit, stderr)
	}
	st, err := os.Stat(outPath)
	if err != nil {
		t.Fatalf("expected binary at %s: %v", outPath, err)
	}
	if st.Mode()&0o111 == 0 {
		t.Errorf("expected %s to be executable; mode = %v", outPath, st.Mode())
	}
	// Run the resulting binary to make sure it actually works.
	out, err := exec.Command(outPath).Output()
	if err != nil {
		t.Fatalf("run binary: %v", err)
	}
	if string(out) != "Tide is rising.\n" {
		t.Errorf("binary stdout = %q; want %q", string(out), "Tide is rising.\n")
	}
}
