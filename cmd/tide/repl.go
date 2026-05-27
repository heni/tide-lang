package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// cmdRepl implements `tide repl` per RFC-0003. PR-REPL-1 lands
// the structural skeleton: input loop, multi-line balance
// detection, classification of each input into a session source
// model, and recompile-and-run between turns. Bare-expression
// auto-print, the reflection-driven meta-commands (`:type` /
// `:inspect`), and the `_` / `_error` last-value bindings land
// in PR-REPL-2. Tier-2 line-editing (go-prompt) is a later polish.
func cmdRepl(args []string) int {
	fs := flag.NewFlagSet("tide repl", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: tide repl")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "tide repl: takes no positional arguments")
		return 2
	}
	return runRepl(os.Stdin, os.Stdout, os.Stderr)
}

const (
	replPrompt     = "tide> "
	replContPrompt = "... > "
)

// replSession holds the accumulated session source. Each
// successful input is appended; on the next turn the whole
// thing is re-emitted into a fresh `func main()` wrapper and
// re-run from scratch. This is the simplest model that honours
// the RFC's "accumulating-source REPL" choice (Tier 1 cost
// model — every input pays for every prior side-effecting line).
type replSession struct {
	imports []string // dedup'd import paths in append order
	decls   []string // top-level decls (func / class / type / record / enum / trait)
	stmts   []string // body statements (let / var / assignment)
}

// runRepl is the testable entry point. Splitting `os.Stdin /
// Stdout / Stderr` out of the loop lets cmd/tide/main_test.go
// drive the prompt without spawning a subprocess.
func runRepl(stdin io.Reader, stdout, stderr io.Writer) int {
	fmt.Fprintln(stdout, "tide repl 0.1 — type :help for commands, :quit to exit.")
	sess := &replSession{}
	scanner := bufio.NewScanner(stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var buf strings.Builder
	prompt := replPrompt
	for {
		fmt.Fprint(stdout, prompt)
		if !scanner.Scan() {
			// EOF / Ctrl-D — clean exit.
			fmt.Fprintln(stdout)
			return 0
		}
		line := scanner.Text()
		if buf.Len() == 0 && strings.HasPrefix(strings.TrimSpace(line), ":") {
			// Meta-command: only valid at the start of a fresh
			// input. The `buf.Len() == 0` guard means a `:` mid
			// multi-line input is treated as ordinary source —
			// where Tide's `:` punctuator (type annotations,
			// match arms) is expected.
			cmd := strings.TrimSpace(line)
			if quit := handleMeta(cmd, sess, stdout, stderr); quit {
				return 0
			}
			continue
		}
		if buf.Len() > 0 {
			buf.WriteByte('\n')
		}
		buf.WriteString(line)
		if !balanced(buf.String()) {
			prompt = replContPrompt
			continue
		}
		input := strings.TrimSpace(buf.String())
		buf.Reset()
		prompt = replPrompt
		if input == "" {
			continue
		}
		if err := sess.add(input); err != nil {
			fmt.Fprintln(stderr, "repl:", err)
			continue
		}
		if len(sess.stmts) == 0 {
			// Imports and decls alone produce no runtime
			// observable behaviour; skip the compile-and-run
			// cycle. The eventual stmt input will trigger a
			// full compile that catches any decl-level issue.
			continue
		}
		if err := runSession(sess, stdout, stderr); err != nil {
			fmt.Fprintln(stderr, "repl:", err)
			// On compile / run failure the offending input is
			// rolled back so the session stays usable. RFC §Errors
			// allows kept side effects from prior successful
			// inputs *in the same input* — we don't currently
			// split a multi-form input, so rollback is whole-input.
			sess.rollback()
		}
	}
}

// add classifies the input and stashes it in the right slot of
// the session. Returns an error for inputs rejected at the REPL
// boundary (see RFC §What the REPL accepts).
func (s *replSession) add(input string) error {
	head := firstWord(input)
	switch head {
	case "import":
		s.imports = append(s.imports, input)
	case "func", "class", "type", "interface":
		s.decls = append(s.decls, input)
	case "if", "for", "while", "match", "return", "break", "continue":
		return fmt.Errorf("top-level control-flow not supported in v1 — wrap it in a func")
	default:
		s.stmts = append(s.stmts, input)
	}
	return nil
}

// rollback drops the most recently added entry — used when the
// inner compile / run failed so the session does not keep a
// broken form.
func (s *replSession) rollback() {
	switch {
	case len(s.stmts) > 0:
		s.stmts = s.stmts[:len(s.stmts)-1]
	case len(s.decls) > 0:
		s.decls = s.decls[:len(s.decls)-1]
	case len(s.imports) > 0:
		s.imports = s.imports[:len(s.imports)-1]
	}
}

// render builds the synthetic Tide source for the current
// session. The output is a complete `.td` file that compiles
// to a Go program with `main()` running every accumulated
// statement.
//
// At the bottom of `main()` the REPL emits silence-use lines —
// `let _ = name` for every prior `let`/`var` binding, and
// `let _ = pkg.<symbol>` for every imported package — so an
// intermediate session state that introduces a binding without
// yet referencing it still compiles. Without this, typing
// `import fmt` followed by `let x = 42` would trip Go's
// imported-and-not-used / declared-and-not-used errors before
// the user could enter the line that actually uses them.
func (s *replSession) render() string {
	var b strings.Builder
	for _, imp := range s.imports {
		b.WriteString(imp)
		b.WriteByte('\n')
	}
	for _, d := range s.decls {
		b.WriteString(d)
		b.WriteString("\n\n")
	}
	b.WriteString("func main() {\n")
	for _, st := range s.stmts {
		b.WriteString("  ")
		b.WriteString(st)
		b.WriteByte('\n')
	}
	for _, name := range s.bindingNames() {
		b.WriteString("  let _ = ")
		b.WriteString(name)
		b.WriteByte('\n')
	}
	for _, ref := range s.importSilences() {
		b.WriteString("  let _ = ")
		b.WriteString(ref)
		b.WriteByte('\n')
	}
	b.WriteString("}\n")
	return b.String()
}

// bindingNames extracts the identifier introduced by each
// `let <name>` / `var <name>` statement in the session. The
// pattern recognised is the simple-identifier form; pattern
// bindings (`let (a, b) = ...`) are silenced by their
// surrounding expression's referenced names, so they need no
// extra handling here.
func (s *replSession) bindingNames() []string {
	var out []string
	for _, st := range s.stmts {
		trimmed := strings.TrimLeft(st, " \t")
		var rest string
		switch {
		case strings.HasPrefix(trimmed, "let "):
			rest = trimmed[len("let "):]
		case strings.HasPrefix(trimmed, "var "):
			rest = trimmed[len("var "):]
		default:
			continue
		}
		rest = strings.TrimLeft(rest, " \t")
		end := 0
		for end < len(rest) {
			c := rest[end]
			if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
				(c >= '0' && c <= '9' && end > 0) || c == '_' {
				end++
				continue
			}
			break
		}
		if end == 0 || rest[:end] == "_" {
			continue
		}
		out = append(out, rest[:end])
	}
	return out
}

// importSilences returns a Tide expression per imported package
// that references an exported symbol from that package, so the
// generated Go file always "uses" the import. Unknown packages
// fall back silently — the user will see Go's
// imported-and-not-used error on the next compile cycle.
func (s *replSession) importSilences() []string {
	var out []string
	for _, imp := range s.imports {
		// Tide grammar (grammar.ebnf §Import) is just
		// `import Ident ("/" Ident)*` — no quoted form, no `as`
		// alias. Strip the keyword and take the first token.
		path := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(imp), "import"))
		path = strings.TrimSpace(strings.SplitN(path, " ", 2)[0])
		if ref, ok := importSilenceRef[path]; ok {
			out = append(out, ref)
		}
	}
	return out
}

// importSilenceRef maps a Tide-side package name to a Tide
// expression that references an exported symbol from the
// corresponding Go-side package. The expression is referenced
// (not called) inside the synthetic `let _ = …` line, which is
// enough to mark the import as used in Go.
//
// The symbol is spelt with its Go-side capitalisation (e.g.
// `strings.Split`, not `strings.split`) because PR-C's binding
// shortcut (`mapFieldName` in `internal/codegen/codegen.go`)
// only normalises a handful of `fmt.*` names; for every other
// package the field name is passed through verbatim. Using the
// Go spelling avoids depending on the binding layer.
//
// Source for this list is the stdlib namespace set recognised
// by codegen (`isStdlibNamespace`); when that set widens, this
// map should track it. `reflect` is Tide-internal but a user
// still spells the import, so it lives here too.
var importSilenceRef = map[string]string{
	"fmt":      "fmt.Println",
	"os":       "os.Stdin",
	"strings":  "strings.Split",
	"strconv":  "strconv.Itoa",
	"bufio":    "bufio.NewScanner",
	"context":  "context.Background",
	"time":     "time.Now",
	"sync":     "sync.WaitGroup",
	"io":       "io.Copy",
	"log":      "log.Println",
	"net":      "net.Listen",
	"encoding": "encoding.BinaryMarshaler",
	"math":     "math.Pi",
	"reflect":  "reflect.typeOf",
}

// runSession compiles and executes the current session source.
// Compile is split out of run so a Go-side build error rolls
// back the offending input while a runtime non-zero exit (the
// user's program panicking or `os.exit(1)`-ing) keeps the
// session intact — that matches RFC §Errors.
func runSession(s *replSession, stdout, stderr io.Writer) error {
	src, err := compileSourceToTempGo(s.render(), "repl.td")
	if err != nil {
		return err
	}
	defer os.RemoveAll(src.dir)

	bin := src.dir + "/repl.bin"
	build := exec.Command("go", "build", "-o", bin, "./...")
	build.Dir = src.dir
	build.Stdout = stderr
	build.Stderr = stderr
	if err := build.Run(); err != nil {
		return fmt.Errorf("compile failed")
	}
	run := exec.Command(bin)
	run.Dir = src.dir
	run.Stdout = stdout
	run.Stderr = stderr
	if err := run.Run(); err != nil {
		if _, ok := err.(*exec.ExitError); ok {
			// User program exited non-zero — already printed
			// its own diagnostics to stderr; not the session's
			// fault, keep the source state intact.
			return nil
		}
		return fmt.Errorf("execution failed: %w", err)
	}
	return nil
}

// handleMeta executes a `:command` line. Returns true if the
// REPL should exit.
func handleMeta(line string, s *replSession, stdout, stderr io.Writer) bool {
	switch fields := strings.Fields(line); fields[0] {
	case ":quit", ":q":
		return true
	case ":help":
		fmt.Fprint(stdout, helpText)
	case ":reset":
		*s = replSession{}
		fmt.Fprintln(stdout, "(session cleared)")
	case ":show":
		fmt.Fprint(stdout, s.render())
	case ":imports":
		if len(s.imports) == 0 {
			fmt.Fprintln(stdout, "(no imports)")
		} else {
			for _, imp := range s.imports {
				fmt.Fprintln(stdout, imp)
			}
		}
	default:
		fmt.Fprintf(stderr, "repl: unknown meta-command %s\n", fields[0])
	}
	return false
}

const helpText = `Meta-commands:
  :help            show this list
  :quit / :q       exit the REPL
  :reset           drop all session state
  :imports         list active imports
  :show            print the accumulated session source

Inputs accepted in v1:
  import <path>                            add an import
  func / class / type / record / enum      top-level declaration
  let / var / <lvalue> = <expr>            session-scoped statement
  (bare expression auto-print, :type, :inspect, and the _ / _error
  last-value bindings land with PR-REPL-2.)
`

// balanced reports whether all of {} () [] in src are matched.
// String / char literals are skipped so a `{` inside `"…"` does
// not count. Block comments `/* ... */` and line comments `// …`
// are also skipped. This is a pragmatic v1: it handles every
// shape in the existing example suite. Edge cases (escaped
// quote inside a raw string, triple-quoted strings) wait on a
// dedicated multi-line tokeniser pass.
func balanced(src string) bool {
	depth := 0
	i := 0
	for i < len(src) {
		c := src[i]
		switch c {
		case '"':
			// Walk to the closing quote, respecting backslash
			// escapes. If the string is unterminated treat the
			// input as unbalanced (the user is still typing).
			j := i + 1
			for j < len(src) && src[j] != '"' {
				if src[j] == '\\' && j+1 < len(src) {
					j += 2
					continue
				}
				if src[j] == '\n' {
					return false
				}
				j++
			}
			if j >= len(src) {
				return false
			}
			i = j + 1
		case '\'':
			j := i + 1
			for j < len(src) && src[j] != '\'' {
				if src[j] == '\\' && j+1 < len(src) {
					j += 2
					continue
				}
				if src[j] == '\n' {
					return false
				}
				j++
			}
			if j >= len(src) {
				return false
			}
			i = j + 1
		case '/':
			if i+1 < len(src) && src[i+1] == '/' {
				j := i + 2
				for j < len(src) && src[j] != '\n' {
					j++
				}
				i = j
			} else if i+1 < len(src) && src[i+1] == '*' {
				j := i + 2
				for j+1 < len(src) && !(src[j] == '*' && src[j+1] == '/') {
					j++
				}
				if j+1 >= len(src) {
					return false
				}
				i = j + 2
			} else {
				i++
			}
		case '{', '(', '[':
			depth++
			i++
		case '}', ')', ']':
			depth--
			i++
		default:
			i++
		}
	}
	// `depth == 0` rather than `<= 0`: a session that has more
	// closers than openers is malformed, but we flush it anyway
	// so the parser produces the diagnostic — leaving it in the
	// continuation buffer would hang forever.
	return depth <= 0
}

// firstWord returns the leading whitespace-delimited token of
// src. Used to classify a fresh REPL input by its keyword.
func firstWord(src string) string {
	src = strings.TrimLeft(src, " \t")
	end := 0
	for end < len(src) {
		c := src[end]
		if c == ' ' || c == '\t' || c == '\n' || c == '(' || c == '{' || c == '[' || c == '<' {
			break
		}
		end++
	}
	return src[:end]
}
