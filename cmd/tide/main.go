// Command tide is the compiler and toolchain for the Tide programming language.
//
// Tide is pre-alpha. Three subcommands wire the lexer / parser / codegen
// pipeline:
//
//	tide emit  <file.td>             print the lowered Go source to stdout
//	tide build [-o out] <file.td>    compile to a Go binary (default: ./<basename>)
//	tide run   <file.td>             compile and execute (stdio passed through,
//	                                 exit code propagated)
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/heni/tide-lang/internal/codegen"
	"github.com/heni/tide-lang/internal/lexer"
	"github.com/heni/tide-lang/internal/parser"
	"github.com/heni/tide-lang/internal/sema"
)

const version = "0.0.0-dev"

func main() {
	if len(os.Args) < 2 {
		usage(os.Stderr)
		os.Exit(2)
	}

	switch os.Args[1] {
	case "version", "-v", "--version":
		fmt.Printf("tide %s\n", version)
	case "emit":
		os.Exit(cmdEmit(os.Args[2:]))
	case "build":
		os.Exit(cmdBuild(os.Args[2:]))
	case "run":
		os.Exit(cmdRun(os.Args[2:]))
	case "repl":
		os.Exit(cmdRepl(os.Args[2:]))
	case "bindgen":
		fmt.Fprintln(os.Stderr, "tide bindgen: not implemented yet")
		os.Exit(1)
	case "help", "-h", "--help":
		usage(os.Stdout)
	default:
		fmt.Fprintf(os.Stderr, "tide: unknown subcommand %q\n\n", os.Args[1])
		usage(os.Stderr)
		os.Exit(2)
	}
}

func cmdEmit(args []string) int {
	fs := flag.NewFlagSet("tide emit", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	noLine := fs.Bool("no-line", false, "strip //line directives from the lowered Go (for human reading)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: tide emit [-no-line] <file.td>")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "tide emit: expected exactly one <file.td>")
		return 2
	}
	goSrc, err := emitGoSourceOpts(fs.Arg(0), *noLine)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Print(goSrc)
	return 0
}

func cmdBuild(args []string) int {
	fs := flag.NewFlagSet("tide build", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	out := fs.String("o", "", "output binary path (default: ./<basename>)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: tide build [-o <path>] <file.td>")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "tide build: expected exactly one <file.td>")
		return 2
	}
	srcPath := fs.Arg(0)
	src, err := compileToTempGo(srcPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(src.dir)

	outPath := *out
	if outPath == "" {
		outPath = outputBinaryName(srcPath)
	}
	absOut, err := filepath.Abs(outPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tide build: %v\n", err)
		return 1
	}
	cmd := exec.Command("go", "build", "-o", absOut, "./...")
	cmd.Dir = src.dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "tide build: go build failed: %v\n", err)
		return 1
	}
	return 0
}

// emitGoSource lexes / parses / lowers the file and returns the
// generated Go source string. Used by cmdEmit and (indirectly via
// compileToTempGo) by build / run.
func emitGoSource(path string) (string, error) {
	return emitGoSourceOpts(path, false)
}

// emitGoSourceOpts is the variant that takes the `no-line` flag —
// when true, the //line directives mapping back to the .td source
// are suppressed (useful for reading the lowered Go directly).
// Build / run keep them on so panic traces and `go vet` errors
// still point at Tide source coordinates.
func emitGoSourceOpts(path string, stripLine bool) (string, error) {
	srcBytes, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("tide: cannot read %s: %w", path, err)
	}
	label := path
	if stripLine {
		label = ""
	}
	return emitGoFromText(string(srcBytes), label)
}

// emitGoFromText runs the lexer / parser / codegen pipeline over
// an in-memory string. Used by REPL execution where the source
// is synthesised between turns rather than read from disk.
func emitGoFromText(src, file string) (string, error) {
	// Pass the path verbatim into diagnostics and //line
	// directives. test-contract.md §File paths requires
	// repo-relative paths so two files with the same basename
	// (e.g. a shared entry filename under different example dirs)
	// remain distinguishable in panic traces and diagnostics.
	toks, lerr := lexer.LexFile(src, file)
	if lerr != nil {
		return "", lerr
	}
	tree, perr := parser.ParseFile(toks, file)
	if perr != nil {
		return "", perr
	}
	info, diags := sema.Check(tree, file)
	if len(diags) > 0 {
		// Report all diags; return the first as the error so
		// callers stop, after printing the full batch.
		for _, d := range diags {
			fmt.Fprintln(os.Stderr, d.Error())
		}
		return "", fmt.Errorf("tide: sema failed")
	}
	goSrc, err := codegen.EmitWithInfo(tree, file, info)
	if err != nil {
		return "", fmt.Errorf("tide: %s", err)
	}
	return goSrc, nil
}

func cmdRun(args []string) int {
	fs := flag.NewFlagSet("tide run", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: tide run <file.td>")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "tide run: expected exactly one <file.td>")
		return 2
	}
	src, err := compileToTempGo(fs.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(src.dir)

	cmd := exec.Command("go", "run", "./...")
	cmd.Dir = src.dir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode()
		}
		fmt.Fprintf(os.Stderr, "tide run: go run failed: %v\n", err)
		return 1
	}
	return 0
}

// compiledSource bundles the path to a temporary directory that
// contains main.go + go.mod ready for `go build` / `go run`.
type compiledSource struct {
	dir string // caller is responsible for RemoveAll
}

// compileToTempGo lexes / parses / lowers the given .td file and
// writes main.go + go.mod into a fresh temp dir. The caller must
// RemoveAll the returned dir.
func compileToTempGo(path string) (*compiledSource, error) {
	goSrc, err := emitGoSource(path)
	if err != nil {
		return nil, err
	}
	return writeTempModule(goSrc)
}

// compileSourceToTempGo is the in-memory variant used by the
// REPL: takes Tide source text + a synthetic file label for
// diagnostics, returns a runnable temp module.
func compileSourceToTempGo(src, label string) (*compiledSource, error) {
	goSrc, err := emitGoFromText(src, label)
	if err != nil {
		return nil, err
	}
	return writeTempModule(goSrc)
}

func writeTempModule(goSrc string) (*compiledSource, error) {
	dir, err := os.MkdirTemp("", "tide-build-*")
	if err != nil {
		return nil, fmt.Errorf("tide: mkdir temp: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(goSrc), 0o644); err != nil {
		os.RemoveAll(dir)
		return nil, fmt.Errorf("tide: write main.go: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module tide-output\n\ngo 1.22\n"), 0o644); err != nil {
		os.RemoveAll(dir)
		return nil, fmt.Errorf("tide: write go.mod: %w", err)
	}
	return &compiledSource{dir: dir}, nil
}

// outputBinaryName turns "examples/core-language/hello/hello.td" → "./hello".
func outputBinaryName(path string) string {
	base := filepath.Base(path)
	if i := strings.LastIndexByte(base, '.'); i > 0 {
		base = base[:i]
	}
	return filepath.Join(".", base)
}

func usage(w io.Writer) {
	fmt.Fprintln(w, `Tide - modern TypeScript-style syntax on the Go runtime.

Usage:
  tide <command> [arguments]

Commands:
  emit   [-no-line] <file.td>  print the lowered Go source to stdout
  build  [-o out] <file.td>    compile to a native binary (default: ./<basename>)
  run    <file.td>             compile and execute (stdio passed through)
  repl                         interactive prompt (RFC-0003 skeleton)
  bindgen                      generate Tide bindings from a Go package (not implemented)
  version                      print the compiler version
  help                         print this message

Status: pre-alpha.`)
}
