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

// TestCrossPackageManifestRun — a project with a tide.toml: the entry
// file imports a user package (`myproj/utils`), whose directory's .td
// files are pulled into the build and run (RFC-0002 §Resolution).
func TestCrossPackageManifestRun(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "utils"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "tide.toml", "[project]\nname = \"myproj\"\n")
	writeFile(t, dir, "main.td", "import fmt\nimport myproj/utils\nfunc main() { fmt.println(shout(\"x\")) }\n")
	writeFile(t, filepath.Join(dir, "utils"), "strs.td", "import strings\nfunc shout(s: string): string { return strings.toUpper(s) }\n")
	stdout, stderr, exit := runTide(t, "run", filepath.Join(dir, "main.td"))
	if exit != 0 {
		t.Fatalf("cross-package run exited %d (stderr: %s)", exit, stderr)
	}
	if stdout != "X\n" {
		t.Errorf("stdout = %q; want \"X\\n\"", stdout)
	}
}

// TestUnknownImportPath — an import that is neither a local user package
// nor a stdlib namespace is E0117.
func TestUnknownImportPath(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "tide.toml", "[project]\nname = \"p\"\n")
	writeFile(t, dir, "main.td", "import totally/unknown\nfunc main() {}\n")
	_, stderr, exit := runTide(t, "run", dir)
	if exit == 0 {
		t.Fatal("expected non-zero exit for an unknown import")
	}
	if !strings.Contains(stderr, "E0117") {
		t.Errorf("stderr = %q; want E0117", stderr)
	}
}

// TestCyclicPackageImport — a user-package import cycle is E0116.
func TestCyclicPackageImport(t *testing.T) {
	dir := t.TempDir()
	for _, d := range []string{"a", "b"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, dir, "tide.toml", "[project]\nname = \"c\"\n")
	writeFile(t, dir, "main.td", "import c/a\nfunc main() {}\n")
	writeFile(t, filepath.Join(dir, "a"), "a.td", "import c/b\nfunc fa() {}\n")
	writeFile(t, filepath.Join(dir, "b"), "b.td", "import c/a\nfunc fb() {}\n")
	_, stderr, exit := runTide(t, "run", dir)
	if exit == 0 {
		t.Fatal("expected non-zero exit for a cyclic import")
	}
	if !strings.Contains(stderr, "E0116") {
		t.Errorf("stderr = %q; want E0116", stderr)
	}
}

// TestDiamondImportNotACycle — a diamond (root→a, root→b, a→c, b→c) is a
// DAG, not a cycle: it must build and run, guarding against a
// false-positive in cycle detection.
func TestDiamondImportNotACycle(t *testing.T) {
	dir := t.TempDir()
	for _, d := range []string{"a", "b", "c"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, dir, "tide.toml", "[project]\nname = \"d\"\n")
	writeFile(t, dir, "main.td", "import d/a\nimport d/b\nimport fmt\nfunc main() { fmt.println(fc()) }\n")
	writeFile(t, filepath.Join(dir, "a"), "a.td", "import d/c\nfunc fa(): int { return fc() }\n")
	writeFile(t, filepath.Join(dir, "b"), "b.td", "import d/c\nfunc fb(): int { return fc() }\n")
	writeFile(t, filepath.Join(dir, "c"), "c.td", "func fc(): int { return 42 }\n")
	stdout, stderr, exit := runTide(t, "run", dir)
	if exit != 0 {
		t.Fatalf("diamond DAG should build; exited %d (stderr: %s)", exit, stderr)
	}
	if stdout != "42\n" {
		t.Errorf("stdout = %q; want \"42\\n\"", stdout)
	}
}

// TestBareSelfImportIsNoOp — a package importing itself (bare `import
// myproj` from the project root) is a no-op, not a cycle (E0116).
func TestBareSelfImportIsNoOp(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "tide.toml", "[project]\nname = \"myproj\"\n")
	writeFile(t, dir, "main.td", "import fmt\nimport myproj\nfunc main() { fmt.println(\"ok\") }\n")
	stdout, stderr, exit := runTide(t, "run", dir)
	if exit != 0 {
		t.Fatalf("bare self-import should be a no-op; exited %d (stderr: %s)", exit, stderr)
	}
	if stdout != "ok\n" {
		t.Errorf("stdout = %q; want \"ok\\n\"", stdout)
	}
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
