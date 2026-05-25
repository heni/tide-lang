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
