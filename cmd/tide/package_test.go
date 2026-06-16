package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Driver coverage for directory packages — every `.td` file in a
// directory is one package sharing top-level scope (RFC-0002
// §"Package = directory").

// writePkg writes name→source files into a fresh temp dir and returns it.
func writePkg(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

// TestPackageMultiFileRun — three files share one scope: main calls a
// function from each sibling file, with imports unioned across files.
func TestPackageMultiFileRun(t *testing.T) {
	dir := writePkg(t, map[string]string{
		"util.td":  "import strings\nfunc shout(s: string): string { return strings.toUpper(s) }\n",
		"greet.td": "func greeting(): string { return shout(\"hi\") }\n",
		"main.td":  "import fmt\nfunc main() { fmt.println(greeting()) }\n",
	})
	stdout, stderr, exit := runTide(t, "run", dir)
	if exit != 0 {
		t.Fatalf("package run exited %d (stderr: %s)", exit, stderr)
	}
	if stdout != "HI\n" {
		t.Errorf("package stdout = %q; want \"HI\\n\"", stdout)
	}
}

// TestPackageNoMain — a package built to a binary needs a `func main`;
// its absence is a Tide-coordinate error, not a leaked Go-toolchain one.
func TestPackageNoMain(t *testing.T) {
	dir := writePkg(t, map[string]string{
		"lib.td": "func helper(): int { return 1 }\n",
	})
	_, stderr, exit := runTide(t, "run", dir)
	if exit == 0 {
		t.Fatal("expected non-zero exit for a package with no main")
	}
	if !strings.Contains(stderr, "no `func main`") {
		t.Errorf("stderr = %q; want a `no func main` error", stderr)
	}
}

// TestPackageCrossFileDuplicate — a top-level name declared in two files
// of the same package is E0113, attributed to the offending file.
func TestPackageCrossFileDuplicate(t *testing.T) {
	dir := writePkg(t, map[string]string{
		"a.td": "func dup(): int { return 1 }\nfunc main() {}\n",
		"b.td": "func dup(): int { return 2 }\n",
	})
	_, stderr, exit := runTide(t, "run", dir)
	if exit == 0 {
		t.Fatal("expected non-zero exit for a cross-file duplicate decl")
	}
	if !strings.Contains(stderr, "E0113") || !strings.Contains(stderr, "b.td") {
		t.Errorf("stderr = %q; want E0113 attributed to b.td", stderr)
	}
}

// TestPackageCrossFileDiagPath — a type error in one file reports that
// file's path (not the first file's), proving per-file attribution.
func TestPackageCrossFileDiagPath(t *testing.T) {
	dir := writePkg(t, map[string]string{
		"a.td": "func main() {}\n",
		"b.td": "func bad(): int { return \"no\" }\n",
	})
	_, stderr, exit := runTide(t, "run", dir)
	if exit == 0 {
		t.Fatal("expected non-zero exit for a type error")
	}
	if !strings.Contains(stderr, "b.td") {
		t.Errorf("stderr = %q; want the diagnostic attributed to b.td", stderr)
	}
}
