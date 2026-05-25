// Command tide is the compiler and toolchain for the Tide programming language.
//
// Tide is pre-alpha. PR-D wires the lexer / parser / codegen pipeline
// behind two subcommands:
//
//	tide build <file.td>   compile to a Go binary in ./<basename>
//	tide run   <file.td>   compile and execute (stdout / stderr passed
//	                       through, exit code propagated)
//
// Both subcommands emit Go to a temporary working directory, drop a
// minimal go.mod beside it, and shell out to the Go toolchain.
package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/heni/tide-lang/internal/codegen"
	"github.com/heni/tide-lang/internal/lexer"
	"github.com/heni/tide-lang/internal/parser"
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
	case "build":
		os.Exit(cmdBuild(os.Args[2:]))
	case "run":
		os.Exit(cmdRun(os.Args[2:]))
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

func cmdBuild(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "tide build: expected <file.td>")
		return 2
	}
	src, err := compileToTempGo(args[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(src.dir)

	out := outputBinaryName(args[0])
	absOut, err := filepath.Abs(out)
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

func cmdRun(args []string) int {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "tide run: expected <file.td>")
		return 2
	}
	src, err := compileToTempGo(args[0])
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
	srcBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("tide: cannot read %s: %w", path, err)
	}
	src := string(srcBytes)
	file := filepath.Base(path)

	toks, lerr := lexer.LexFile(src, file)
	if lerr != nil {
		return nil, fmt.Errorf("%s", lerr.Error())
	}
	tree, perr := parser.ParseFile(toks, file)
	if perr != nil {
		return nil, fmt.Errorf("%s", perr.Error())
	}
	goSrc, err := codegen.Emit(tree, file)
	if err != nil {
		return nil, fmt.Errorf("tide: %s", err)
	}

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

// outputBinaryName turns "examples/hello.td" → "./hello".
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
  build  <file.td>   compile a Tide program to a native binary
  run    <file.td>   compile and execute a Tide program
  bindgen            generate Tide bindings from a Go package (not implemented)
  version            print the compiler version
  help               print this message

Status: pre-alpha.`)
}
