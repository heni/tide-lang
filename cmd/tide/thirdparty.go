package main

// thirdparty.go — hermetic third-party dependency plumbing for FFI
// bindings (lang-spec/ffi.md §"Dependency model"). When generated Go
// imports a manifest-listed third-party package, the emitted go.mod gains
// a `require` plus a `replace` to the vendored copy, so the build never
// touches the network.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// thirdPartyDep is one entry of the binding manifest (std/bindings.json).
type thirdPartyDep struct {
	ImportPath string `json:"importPath"`
	Module     string `json:"module"`
	Version    string `json:"version"`
	Vendor     string `json:"vendor"` // path to the vendored copy, relative to the tide root
}

type manifest struct {
	ThirdParty []thirdPartyDep `json:"thirdParty"`
}

// findTideRoot locates the directory holding std/bindings.json — the
// $TIDE_ROOT override, else the nearest ancestor of the cwd that
// contains it. Returns "" when no manifest is reachable (a stdlib-only
// install): third-party binding is simply unavailable, not an error.
func findTideRoot() string {
	if r := os.Getenv("TIDE_ROOT"); r != "" {
		return r
	}
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "std", "bindings.json")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// loadManifest reads std/bindings.json under root. A missing manifest
// yields an empty (not failed) manifest — third-party deps are opt-in.
func loadManifest(root string) (manifest, error) {
	var m manifest
	if root == "" {
		return m, nil
	}
	data, err := os.ReadFile(filepath.Join(root, "std", "bindings.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return m, nil
		}
		return m, fmt.Errorf("tide: read binding manifest: %w", err)
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return m, fmt.Errorf("tide: parse binding manifest: %w", err)
	}
	return m, nil
}

// usedThirdParty returns the manifest deps whose import path the emitted
// Go actually references (an `import "<path>"` line).
func usedThirdParty(goSrc string, m manifest) []thirdPartyDep {
	var used []thirdPartyDep
	for _, d := range m.ThirdParty {
		if strings.Contains(goSrc, `"`+d.ImportPath+`"`) {
			used = append(used, d)
		}
	}
	return used
}

// goModText renders the temp build module's go.mod, adding a `require`
// and a hermetic `replace` (to an absolute vendored path) for each used
// third-party dep.
func goModText(root string, used []thirdPartyDep) string {
	var b strings.Builder
	b.WriteString("module tide-output\n\ngo 1.22\n")
	for _, d := range used {
		fmt.Fprintf(&b, "\nrequire %s %s\n", d.Module, d.Version)
		abs := d.Vendor
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(root, d.Vendor)
		}
		fmt.Fprintf(&b, "replace %s => %s\n", d.Module, abs)
	}
	return b.String()
}

// thirdPartyGoMod computes the go.mod for a generated program, resolving
// any third-party bindings it uses against the manifest.
func thirdPartyGoMod(goSrc string) (string, error) {
	root := findTideRoot()
	m, err := loadManifest(root)
	if err != nil {
		return "", err
	}
	return goModText(root, usedThirdParty(goSrc, m)), nil
}
