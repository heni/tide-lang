// Package corpusexec is the OS-boundary adapter for the Tide corpus-status
// tool (tools/corpus-status). It is a vendored, self-contained Go module
// bound through the FFI (lang-spec/ffi.md §"Dependency model"), like
// std/vendor/tidekv.
//
// It exists because Tide's `(T, error) → Result<T, error>` boundary lift is
// *lossy*: it discards the value when the error is non-nil. A subprocess's
// combined output is meaningful precisely when the process *fails* (a build
// diagnostic), so `(*exec.Cmd).CombinedOutput()` cannot be bound directly —
// the diagnostic text would be thrown away on the non-zero exit the tool is
// trying to classify. This adapter reshapes the boundary into an opaque
// handle carrying both halves (output + exit code), each reachable through a
// value-returning method that the FFI binds cleanly.
package corpusexec

import (
	"os/exec"
)

// Result is the opaque handle a Run carries: the subprocess's combined
// stdout+stderr and its exit code (0 on success).
type Result struct {
	out  string
	code int
}

// Run executes name with args, capturing combined stdout+stderr. The exit
// code is 0 on success, the process's code on a normal non-zero exit, and
// -1 when the process could not be started (binary missing, etc.) — the
// caller treats any non-zero as failure, and the output carries the reason.
func Run(name string, args []string) *Result {
	c := exec.Command(name, args...)
	b, err := c.CombinedOutput()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			code = -1
		}
	}
	return &Result{out: string(b), code: code}
}

// Bytes converts a string to a byte slice. Tide has no `[]byte(s)`
// conversion surface yet, and `os.WriteFile` needs `[]byte`; this is the
// one-line adapter for it.
func Bytes(s string) []byte { return []byte(s) }

// Out returns the captured combined output (valid on success and failure).
func (r *Result) Out() string { return r.out }

// Code returns the exit code (0 = success).
func (r *Result) Code() int { return r.code }
