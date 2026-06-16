// FFI bindings for the corpus-status tool (ffi.md). Curated `extern`
// declarations over the Go stdlib plus the vendored corpusexec adapter
// (std/vendor/corpusexec) for the subprocess boundary. Built-in module
// bindings (`import os/strings/fmt/json/sort`) cover the rest.

// --- Subprocess (vendored adapter; the (T,error) Result lift is lossy for
// CombinedOutput, so the adapter carries output + exit code on a handle) ---

extern type RunResult @go("example.com/corpusexec")

extern func run(name: string, args: []string): RunResult @go("example.com/corpusexec.Run")

// runExample runs a built example for the run_ok metric: feeds stdin, enforces
// a wall-clock timeout (ms; <= 0 = none), and captures stdout separately from
// stderr (see RunResult.stdout). code is TimeoutCode (124) on a deadline kill.
extern func runExample(name: string, args: []string, stdin: string, timeoutMs: int): RunResult @go("example.com/corpusexec.RunExample")

extern func toBytes(s: string): []byte @go("example.com/corpusexec.Bytes")

extern impl RunResult {
  out():    string @go("Out")
  stdout(): string @go("Stdout")
  code():   int    @go("Code")
}

// --- Filesystem (os.readFile, os.args, os.exit, os.getenv are built-in) ---

extern func writeFile(name: string, data: []byte, perm: int): Result<unit, error> @go("os.WriteFile")

extern func mkdirTemp(dir: string, pattern: string): Result<string, error> @go("os.MkdirTemp")

extern func removeAll(path: string): Result<unit, error> @go("os.RemoveAll")

extern func chdir(dir: string): Result<unit, error> @go("os.Chdir")

// --- Paths ---

extern func pathJoin(elems: ...string): string @go("path/filepath.Join")

extern func pathDir(path: string): string @go("path/filepath.Dir")

extern func pathBase(path: string): string @go("path/filepath.Base")

extern func pathAbs(path: string): Result<string, error> @go("path/filepath.Abs")

// --- strings helpers absent from the built-in `strings` table ---

extern func indexOf(s: string, sub: string): int @go("strings.Index")

extern func splitN(s: string, sep: string, n: int): []string @go("strings.SplitN")

// --- Regexp (the negative-case code extractor) ---

extern type Regexp @go("regexp")

extern func compileRegexp(expr: string): Result<Regexp, error> @go("regexp.Compile")

extern impl Regexp {
  findAll(s: string, n: int): []string @go("FindAllString")
}
