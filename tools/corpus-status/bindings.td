// FFI bindings for the corpus-status tool (ffi.md). Curated `extern`
// declarations over the Go stdlib plus the vendored corpusexec adapter
// (std/vendor/corpusexec) for the subprocess boundary. Built-in module
// bindings (`import os/strings/fmt/json/sort`) cover the rest.

// --- Subprocess (vendored adapter; the (T,error) Result lift is lossy for
// CombinedOutput, so the adapter carries output + exit code on a handle) ---

extern type RunResult @go("example.com/corpusexec")

extern func run(name: string, args: []string): RunResult @go("example.com/corpusexec.Run")

extern func toBytes(s: string): []byte @go("example.com/corpusexec.Bytes")

extern impl RunResult {
  out():  string @go("Out")
  code(): int    @go("Code")
}

// --- Filesystem (os.readFile, os.args, os.exit, os.getenv are built-in) ---

extern func writeFile(name: string, data: []byte, perm: int): Result<unit, error> @go("os.WriteFile")

extern func mkdirTemp(dir: string, pattern: string): Result<string, error> @go("os.MkdirTemp")

extern func removeAll(path: string): Result<unit, error> @go("os.RemoveAll")

extern func chdir(dir: string): Result<unit, error> @go("os.Chdir")

// --- Paths ---

extern func pathJoin(elems: ...string): string @go("path/filepath.Join")

// --- strings helpers absent from the built-in `strings` table ---

extern func indexOf(s: string, sub: string): int @go("strings.Index")

extern func splitN(s: string, sep: string, n: int): []string @go("strings.SplitN")
