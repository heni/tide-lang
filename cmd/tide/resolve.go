package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/heni/tide-lang/internal/codegen"
	"github.com/heni/tide-lang/internal/lexer"
	"github.com/heni/tide-lang/internal/parser"
)

// resolved is the build unit produced by the package resolver: the full,
// ordered set of `.td` source files (the entry package plus the
// transitive closure of imported user packages) and the set of
// user-package import paths — which are resolved by pulling those files
// into the build, not by a Go import, so codegen strips them from the
// emitted import block.
type resolved struct {
	files       []string        // all .td files, deterministic order
	userImports map[string]bool // import paths that name a user package
}

// resolvePackages computes the build unit for an entry target. Without a
// manifest (m == nil) the entry is a lone package — its files, no
// cross-package resolution (RFC-0002 §Resolution: step 1 skipped). With
// a manifest, each `import P` is classified (RFC-0002 §Resolution):
//   - a path under the project name → a local user package (its
//     directory's .td files join the build); a missing directory is
//     E0117.
//   - a stdlib / [bindings] extra path → left for the binding registry.
//   - anything else → E0117 unknown import path.
//
// Cycles in the user-package import graph are E0116.
func resolvePackages(entry []string, m *projectManifest) (*resolved, error) {
	r := &resolved{userImports: map[string]bool{}}
	seenDir := map[string]bool{} // package dirs already gathered
	onStack := map[string]bool{} // dirs on the current DFS path (cycle detection)

	var visit func(dir string, files []string, trail []string) error
	visit = func(dir string, files []string, trail []string) error {
		abs, _ := filepath.Abs(dir)
		if onStack[abs] {
			return fmt.Errorf("tide: error[E0116]: cyclic package import: %s", strings.Join(append(trail, dir), " -> "))
		}
		if seenDir[abs] {
			return nil
		}
		seenDir[abs] = true
		onStack[abs] = true
		defer func() { onStack[abs] = false }()

		for _, f := range files {
			imps, err := fileImports(f)
			if err != nil {
				// Defer the real error to the authoritative parse in
				// compilePackage; skip import discovery for this file.
				continue
			}
			for _, p := range imps {
				kind, target := classifyImport(p, m)
				switch kind {
				case importStdlib:
					// resolved by the binding registry; nothing to gather.
				case importUser:
					r.userImports[p] = true
					// A package importing itself (e.g. bare `import
					// myproj` from the project root, or a file re-importing
					// its own package) is a no-op, not a cycle.
					if absTarget, _ := filepath.Abs(target); absTarget == abs {
						continue
					}
					sub, err := gatherSources(target)
					if err != nil {
						return fmt.Errorf("tide: error[E0117]: unknown import path %q (no package directory at %s)", p, target)
					}
					if err := visit(target, sub, append(trail, dir)); err != nil {
						return err
					}
				case importUnknown:
					return fmt.Errorf("tide: error[E0117]: unknown import path %q", p)
				}
			}
		}
		r.files = append(r.files, files...)
		return nil
	}

	if err := visit(filepath.Dir(entry[0]), entry, nil); err != nil {
		return nil, err
	}
	r.files = dedupeSorted(r.files)
	return r, nil
}

type importKind int

const (
	importStdlib importKind = iota
	importUser
	importUnknown
)

// classifyImport decides how an import path resolves (RFC-0002
// §Resolution). For a user package it also returns the directory the
// package lives in (relative to the manifest root).
func classifyImport(p string, m *projectManifest) (importKind, string) {
	head := strings.SplitN(p, "/", 2)[0]
	if m != nil && (p == m.name || strings.HasPrefix(p, m.name+"/")) {
		rest := strings.TrimPrefix(strings.TrimPrefix(p, m.name), "/")
		return importUser, filepath.Join(m.dir, filepath.FromSlash(rest))
	}
	if codegen.IsStdlibNamespace(head) {
		return importStdlib, ""
	}
	if m != nil {
		for _, extra := range m.bindingsExtra {
			if lastSegment(extra) == head {
				return importStdlib, ""
			}
		}
	}
	return importUnknown, ""
}

// fileImports lexes + parses one .td file and returns its import paths.
// Errors are returned so the caller can defer to the authoritative parse.
func fileImports(path string) ([]string, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	toks, lerr := lexer.LexFile(string(src), path)
	if lerr != nil {
		return nil, lerr
	}
	tree, perr := parser.ParseFile(toks, path)
	if perr != nil {
		return nil, perr
	}
	var out []string
	for _, im := range tree.Imports {
		out = append(out, im.Path)
	}
	return out, nil
}

func dedupeSorted(xs []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	sort.Strings(out)
	return out
}
