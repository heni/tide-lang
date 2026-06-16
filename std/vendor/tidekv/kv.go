// Package tidekv is a tiny key=value config reader. It is a vendored,
// self-contained stand-in for a *third-party* Go module: a separate Go
// module (own go.mod, own import path) that lives outside the compiler's
// own module, used to prove Tide's hermetic FFI dependency plumbing —
// generated Go that imports it builds with a manifest-driven `require` +
// `replace` to this committed copy, never touching the network
// (lang-spec/ffi.md §"Dependency model"). The same mechanism binds a
// real third-party package (e.g. github.com/BurntSushi/toml) once
// vendored.
package tidekv

import "strings"

// Value returns the value bound to key in a newline-separated
// `key = value` config (blank lines and `#` comments ignored), or the
// empty string when the key is absent.
func Value(data, key string) string {
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok && strings.TrimSpace(k) == key {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// Count returns the number of `key = value` entries in data.
func Count(data string) int {
	n := 0
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.Contains(line, "=") {
			n++
		}
	}
	return n
}
