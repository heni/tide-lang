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
	"bytes"
	"context"
	"os/exec"
	"strings"
	"time"
)

// TimeoutCode is the synthetic exit code RunExample reports when the process
// exceeds its deadline (mirrors coreutils `timeout`). It is non-zero, so the
// caller's "any non-zero is failure" rule already rejects a hung example.
const TimeoutCode = 124

// Result is the opaque handle a run carries: the subprocess's combined
// stdout+stderr (for diagnostics), its stdout alone (for the run_ok output
// diff), and its exit code (0 on success).
type Result struct {
	out    string
	stdout string
	code   int
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

// RunExample executes a built example binary for the run_ok metric: it feeds
// stdin, enforces a wall-clock timeout (timeoutMs; <= 0 means none), and
// captures stdout and stderr separately so the caller can diff stdout while
// still surfacing stderr in the combined output. The exit code is the
// process's on a normal exit, TimeoutCode on a deadline kill, and -1 when the
// process could not be started.
func RunExample(name string, args []string, stdin string, timeoutMs int) *Result {
	ctx := context.Background()
	cancel := context.CancelFunc(func() {})
	if timeoutMs > 0 {
		ctx, cancel = context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
	}
	defer cancel()

	c := exec.CommandContext(ctx, name, args...)
	if stdin != "" {
		c.Stdin = strings.NewReader(stdin)
	}
	var outBuf, errBuf bytes.Buffer
	c.Stdout = &outBuf
	c.Stderr = &errBuf
	err := c.Run()

	code := 0
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			code = TimeoutCode
		} else if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			code = -1
		}
	}
	// out concatenates stdout then stderr (not the real-time interleave Run
	// gets from CombinedOutput); it is the diagnostic surface, while stdout is
	// the run_ok diff surface.
	return &Result{out: outBuf.String() + errBuf.String(), stdout: outBuf.String(), code: code}
}

// Bytes converts a string to a byte slice. Tide has no `[]byte(s)`
// conversion surface yet, and `os.WriteFile` needs `[]byte`; this is the
// one-line adapter for it.
func Bytes(s string) []byte { return []byte(s) }

// Out returns the captured combined output (valid on success and failure).
func (r *Result) Out() string { return r.out }

// Stdout returns the captured stdout alone (the run_ok output-diff surface).
func (r *Result) Stdout() string { return r.stdout }

// Code returns the exit code (0 = success).
func (r *Result) Code() int { return r.code }
