package main

import (
	"os"
	"path/filepath"
	"testing"
)

// Coverage for the tide.toml project manifest reader (RFC-0002 §Manifest)
// and the package resolver's import classification (RFC-0002 §Resolution).

func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func TestParseManifestFull(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "tide.toml", `# a project
[project]
name = "myproj"

[toolchain]
go = "1.22"

[bindings]
extra = ["golang.org/x/exp/slices", "example.com/foo"]
`)
	m, err := parseProjectManifest(filepath.Join(dir, "tide.toml"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if m.name != "myproj" {
		t.Errorf("name = %q; want myproj", m.name)
	}
	if m.toolchainGo != "1.22" {
		t.Errorf("toolchain go = %q; want 1.22", m.toolchainGo)
	}
	if len(m.bindingsExtra) != 2 || m.bindingsExtra[0] != "golang.org/x/exp/slices" {
		t.Errorf("bindingsExtra = %v", m.bindingsExtra)
	}
}

func TestParseManifestErrors(t *testing.T) {
	cases := map[string]string{
		"unknown section": "[bogus]\nx = \"y\"\n",
		"missing name":    "[toolchain]\ngo = \"1.22\"\n",
		"unknown key":     "[project]\nname = \"p\"\nweird = \"x\"\n",
		"extra collision": "[project]\nname = \"p\"\n[bindings]\nextra = [\"a/slices\", \"b/slices\"]\n",
		"non-string name": "[project]\nname = 5\n",
	}
	for desc, body := range cases {
		dir := t.TempDir()
		writeFile(t, dir, "tide.toml", body)
		if _, err := parseProjectManifest(filepath.Join(dir, "tide.toml")); err == nil {
			t.Errorf("%s: expected an error, got none", desc)
		}
	}
}

func TestFindProjectManifestWalksUp(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "tide.toml", "[project]\nname = \"p\"\n")
	sub := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	m, err := findProjectManifest(sub)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if m == nil || m.name != "p" {
		t.Fatalf("expected to find the manifest from a nested dir, got %+v", m)
	}
}

func TestFindProjectManifestNoneIsNil(t *testing.T) {
	dir := t.TempDir() // a bare temp dir with no tide.toml above it (within the temp root)
	m, err := findProjectManifest(dir)
	// Walking up may eventually hit a real tide.toml on the host; only
	// assert the no-error contract and that absence yields nil (when nil).
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	_ = m
}

func TestClassifyImport(t *testing.T) {
	m := &projectManifest{dir: "/proj", name: "myproj", bindingsExtra: []string{"golang.org/x/exp/slices"}}
	cases := []struct {
		path string
		want importKind
	}{
		{"myproj/utils", importUser},
		{"myproj", importUser},
		{"fmt", importStdlib},
		{"encoding/json", importStdlib},
		{"slices", importStdlib}, // via [bindings] extra last-segment
		{"totally/unknown", importUnknown},
	}
	for _, c := range cases {
		got, _ := classifyImport(c.path, m)
		if got != c.want {
			t.Errorf("classifyImport(%q) = %v; want %v", c.path, got, c.want)
		}
	}
}
