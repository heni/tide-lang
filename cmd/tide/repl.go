package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mattn/go-isatty"
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
	// TTY → go-prompt path with up-arrow history and a coloured
	// prompt; non-TTY (pipe / test harness) → bufio.Scanner
	// fallback so input from stdin pipes still works for tests.
	if isatty.IsTerminal(os.Stdin.Fd()) {
		return runReplPrompt()
	}
	return runRepl(os.Stdin, os.Stdout, os.Stderr)
}

const (
	replPrompt     = "tide> "
	replContPrompt = "... > "
	replBanner     = "tide repl 0.2 — type :help for commands, :quit to exit."
)

// replSession holds the accumulated session source. Each
// successful input is appended; on the next turn the whole
// thing is re-emitted into a fresh `func main()` wrapper and
// re-run from scratch. This is the simplest model that honours
// the RFC's "accumulating-source REPL" choice (Tier 1 cost
// model — every input pays for every prior side-effecting line).
type replSession struct {
	imports    []string        // dedup'd import paths in append order
	decls      []string        // top-level decls (func / class / type / interface)
	stmts      []replStmt      // body statements in append order
	lastSlot   replSlot        // which slot the most recent add() appended to
	lastChange lastChange      // precise undo info for rollback (handles redefinition)
	rejected   map[string]bool // inputs that already failed compile — refuse to re-stash
}

// replSlot identifies which session slot was last appended to,
// so rollback() knows which slice to pop from. Without this, a
// failed decl could leave its broken text in `decls` while
// rollback() pops an innocent prior stmt.
type replSlot uint8

const (
	slotNone replSlot = iota
	slotImports
	slotDecls
	slotStmts
)

// lastChange records what the most recent add() did so rollback
// can invert it precisely. `kind == replaced` means a same-
// name decl was overwritten — to roll back we restore prevText
// at prevIndex rather than truncating the slice.
type lastChangeKind uint8

const (
	changeAppended lastChangeKind = iota
	changeReplaced
)

type lastChange struct {
	kind      lastChangeKind
	slot      replSlot
	prevIndex int
	prevText  string
}

// replStmt is one body-position input. `autoPrint == true` means
// the original input was a bare expression and the REPL is
// responsible for printing its value. When the session is
// re-rendered between turns, only the most recently added
// auto-print stmt actually prints — earlier auto-print stmts
// are re-emitted as `let _ = (<expr>)` so they keep their side
// effects without replaying their output every turn.
type replStmt struct {
	src       string
	autoPrint bool
}

// runRepl is the testable entry point. Splitting `os.Stdin /
// Stdout / Stderr` out of the loop lets cmd/tide/main_test.go
// drive the prompt without spawning a subprocess.
func runRepl(stdin io.Reader, stdout, stderr io.Writer) int {
	fmt.Fprintln(stdout, replBanner)
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
			// rolled back so the session stays usable, AND
			// recorded in the rejected set so retyping the same
			// broken text doesn't re-enter the compile pipeline
			// forever. RFC §Errors allows kept side effects from
			// prior successful inputs *in the same input* — we
			// don't currently split a multi-form input, so
			// rollback is whole-input.
			sess.markRejected(input)
			sess.rollback()
		}
	}
}

// add classifies the input and stashes it in the right slot of
// the session. Returns an error for inputs rejected at the REPL
// boundary (see RFC §What the REPL accepts).
//
// Bare-expression inputs are wrapped in a synthetic auto-print
// before storage: `<expr>` becomes
// `fmt.println(reflect.show(reflect.box((<expr>))))` so the
// value renders type-aware on the next run, matching RFC
// §Auto-printing. `let` / `var` / assignment inputs go through
// untouched.
func (s *replSession) add(input string) error {
	if s.rejected[input] {
		return fmt.Errorf("input previously failed to compile — not re-stashed (use :reset to clear history)")
	}
	head := firstWord(input)
	switch head {
	case "import":
		s.imports = append(s.imports, input)
		s.lastSlot = slotImports
		s.lastChange = lastChange{kind: changeAppended, slot: slotImports}
	case "func", "class", "type", "interface":
		// REPL last-wins shadowing per RFC §What the REPL
		// accepts ("Re-declaring a name shadows the prior
		// definition"). If a decl with the same name already
		// exists, replace it in-place so Go does not error on
		// the duplicate; otherwise append.
		if name := declName(head, input); name != "" {
			if idx := s.findDeclByName(head, name); idx >= 0 {
				s.lastChange = lastChange{kind: changeReplaced, slot: slotDecls, prevIndex: idx, prevText: s.decls[idx]}
				s.decls[idx] = input
				s.lastSlot = slotDecls
				return nil
			}
		}
		s.decls = append(s.decls, input)
		s.lastSlot = slotDecls
		s.lastChange = lastChange{kind: changeAppended, slot: slotDecls}
	case "if", "for", "while", "match", "return", "break", "continue":
		return fmt.Errorf("top-level control-flow not supported in v1 — wrap it in a func")
	case "let", "var":
		s.stmts = append(s.stmts, replStmt{src: input})
		s.lastSlot = slotStmts
		s.lastChange = lastChange{kind: changeAppended, slot: slotStmts}
	default:
		if isAssignment(input) || looksLikeCallStatement(input) {
			// `fmt.println(x)` and friends — side-effecting calls
			// whose return values aren't useful to print. Treated
			// as plain statements: run for the side effect, do not
			// wrap with auto-print. The user can still inspect a
			// call's return by binding: `let r = call(); r`.
			s.stmts = append(s.stmts, replStmt{src: input})
		} else {
			s.stmts = append(s.stmts, replStmt{src: input, autoPrint: true})
		}
		s.lastSlot = slotStmts
		s.lastChange = lastChange{kind: changeAppended, slot: slotStmts}
	}
	return nil
}

// declName extracts the name of a top-level declaration so the
// REPL can implement last-wins shadowing. Recognises the simple
// shapes: `func <name>(...)`, `class <Name> { ... }`, `type
// <Name> = ...`, `interface <Name> { ... }`. Returns "" for
// shapes the parser handles but this extractor doesn't — a
// generic class `class Box<T>` strips the `<T>` part. The head
// keyword must be followed by whitespace so `funcfoo()` is not
// mistaken for `func foo()` — letting it through would let a
// malformed input transiently displace a working same-name
// decl before compile rejects it.
func declName(head, src string) string {
	trimmed := strings.TrimLeft(src, " \t")
	if !strings.HasPrefix(trimmed, head) {
		return ""
	}
	after := trimmed[len(head):]
	if after == "" || (after[0] != ' ' && after[0] != '\t') {
		return ""
	}
	rest := strings.TrimLeft(after, " \t")
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
	return rest[:end]
}

// findDeclByName scans s.decls for a same-head, same-name decl
// and returns its index, or -1 if none exists.
func (s *replSession) findDeclByName(head, name string) int {
	for i, d := range s.decls {
		if firstWord(d) != head {
			continue
		}
		if declName(head, d) == name {
			return i
		}
	}
	return -1
}

// markRejected records that this exact input text has already
// failed to compile and must not be re-added to the session.
// Without this guard, a user retyping the same broken input
// would land it back in the session, fail again, get rolled
// back, and so on — infinite loop of the same broken construct
// re-entering the compile pipeline.
func (s *replSession) markRejected(input string) {
	if s.rejected == nil {
		s.rejected = map[string]bool{}
	}
	s.rejected[input] = true
}

// looksLikeCallStatement reports whether the input parses as a
// single `ident(...)` / `pkg.field(...)` call at the top level
// — the shape used for void-returning bindings (most stdlib
// printers, mutating methods). The check peels off the leading
// `ident('.' ident)*` sequence and looks at the first
// non-whitespace character after it: if that character is `(`
// and the remainder of the input is entirely the matched parens,
// the input is a single bare call.
func looksLikeCallStatement(src string) bool {
	src = strings.TrimSpace(src)
	i := 0
	for i < len(src) {
		c := src[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9' && i > 0) || c == '_' {
			i++
			continue
		}
		if c == '.' && i > 0 && i+1 < len(src) {
			nx := src[i+1]
			if (nx >= 'a' && nx <= 'z') || (nx >= 'A' && nx <= 'Z') || nx == '_' {
				i++
				continue
			}
		}
		break
	}
	if i == 0 {
		return false
	}
	rest := strings.TrimLeft(src[i:], " \t")
	if !strings.HasPrefix(rest, "(") {
		return false
	}
	// Walk the call and check nothing trails the closing paren.
	depth := 0
	for j := 0; j < len(rest); j++ {
		switch rest[j] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return strings.TrimSpace(rest[j+1:]) == ""
			}
		}
	}
	return false
}

// renderStmt produces the body-position Tide line for a stmt
// given whether it is the most recently entered auto-print stmt
// (the only one whose value actually prints this turn). Earlier
// auto-print stmts collapse into `let _ = (<expr>)` so the side
// effects of the expression are preserved without replaying the
// output every accumulating-source turn.
func renderStmt(st replStmt, printNow bool) string {
	if !st.autoPrint {
		return st.src
	}
	if printNow {
		return "fmt.println(reflect.show(reflect.box((" + st.src + "))))"
	}
	return "let _ = (" + st.src + ")"
}

// isAssignment reports whether the input is a top-level `lhs =
// expr` assignment as opposed to a bare expression. The check is
// deliberately syntactic: walk the source skipping string / char
// / comment regions, track paren / brace / bracket depth, and
// look for a single `=` that is not part of `==`, `!=`, `<=`,
// `>=`. Record / map literal `k: v` fields use `:` not `=`, so
// a literal nested inside the expression won't trip the check.
func isAssignment(src string) bool {
	depth := 0
	i := 0
	for i < len(src) {
		c := src[i]
		switch c {
		case '"':
			j := i + 1
			for j < len(src) && src[j] != '"' {
				if src[j] == '\\' && j+1 < len(src) {
					j += 2
					continue
				}
				j++
			}
			i = j + 1
			continue
		case '\'':
			j := i + 1
			for j < len(src) && src[j] != '\'' {
				if src[j] == '\\' && j+1 < len(src) {
					j += 2
					continue
				}
				j++
			}
			i = j + 1
			continue
		case '/':
			if i+1 < len(src) && src[i+1] == '/' {
				j := i + 2
				for j < len(src) && src[j] != '\n' {
					j++
				}
				i = j
				continue
			}
			if i+1 < len(src) && src[i+1] == '*' {
				j := i + 2
				for j+1 < len(src) && !(src[j] == '*' && src[j+1] == '/') {
					j++
				}
				i = j + 2
				continue
			}
		case '(', '{', '[':
			depth++
		case ')', '}', ']':
			depth--
		case '=':
			if depth == 0 {
				prev := byte(0)
				if i > 0 {
					prev = src[i-1]
				}
				next := byte(0)
				if i+1 < len(src) {
					next = src[i+1]
				}
				// Skip ==, !=, <=, >=, => (match arm arrow).
				if prev == '=' || prev == '!' || prev == '<' || prev == '>' {
					i++
					continue
				}
				if next == '=' || next == '>' {
					i++
					continue
				}
				return true
			}
		}
		i++
	}
	return false
}

// rollback drops the most recently added entry — used when the
// inner compile / run failed so the session does not keep a
// broken form. Consults lastChange so a replaced decl is
// restored rather than truncated: if `func foo()` redefined a
// prior `func foo()` and the new definition fails to compile,
// we want the original back in the session.
func (s *replSession) rollback() {
	switch s.lastChange.kind {
	case changeAppended:
		switch s.lastChange.slot {
		case slotStmts:
			if len(s.stmts) > 0 {
				s.stmts = s.stmts[:len(s.stmts)-1]
			}
		case slotDecls:
			if len(s.decls) > 0 {
				s.decls = s.decls[:len(s.decls)-1]
			}
		case slotImports:
			if len(s.imports) > 0 {
				s.imports = s.imports[:len(s.imports)-1]
			}
		}
	case changeReplaced:
		// Only slotDecls is reachable here today — replacement-
		// in-place only fires for func / class / type / interface.
		// decls cannot shrink between add() and rollback() since
		// nothing else mutates the slice in that window, so the
		// invariant prevIndex < len(s.decls) holds by construction.
		if s.lastChange.slot == slotDecls {
			s.decls[s.lastChange.prevIndex] = s.lastChange.prevText
		}
	}
	s.lastSlot = slotNone
	s.lastChange = lastChange{}
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
	return s.renderWith(nil)
}

// renderOriginal produces the user-facing view of the session:
// the original Tide source the user typed, with imports and
// decls verbatim and stmts in append order — no auto-print
// wrap, no silence-uses. Used by `:show` so users get a
// faithful echo of their inputs rather than the compiled
// harness's instrumented form.
func (s *replSession) renderOriginal() string {
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
		b.WriteString(st.src)
		b.WriteByte('\n')
	}
	b.WriteString("}\n")
	return b.String()
}

// renderWith is the rendering core, used by render() (no extra
// stmts) and the metas (`:type` / `:inspect`) which append a
// one-shot stmt to the session-derived source without mutating
// the persistent session. `fmt` and `reflect` are silently
// added to the imports when the rendered stmts reference them,
// so users get auto-print and the meta-commands without having
// to manually `import fmt` first.
func (s *replSession) renderWith(extra []string) string {
	var b strings.Builder
	// Compute which session stmt is the most recently added
	// auto-print one — only that line prints its value on this
	// turn; earlier auto-prints collapse to a silent let _ = …
	lastAutoPrint := -1
	if len(extra) == 0 {
		for i, st := range s.stmts {
			if st.autoPrint {
				lastAutoPrint = i
			}
		}
	}
	rendered := make([]string, 0, len(s.stmts)+len(extra))
	for i, st := range s.stmts {
		rendered = append(rendered, renderStmt(st, i == lastAutoPrint))
	}
	rendered = append(rendered, extra...)
	imports := ensureImports(s.imports, rendered, s.decls)
	for _, imp := range imports {
		b.WriteString(imp)
		b.WriteByte('\n')
	}
	for _, d := range s.decls {
		b.WriteString(d)
		b.WriteString("\n\n")
	}
	b.WriteString("func main() {\n")
	for _, st := range rendered {
		b.WriteString("  ")
		b.WriteString(st)
		b.WriteByte('\n')
	}
	for _, name := range s.bindingNames() {
		b.WriteString("  let _ = ")
		b.WriteString(name)
		b.WriteByte('\n')
	}
	for _, ref := range importSilencesFor(imports) {
		b.WriteString("  let _ = ")
		b.WriteString(ref)
		b.WriteByte('\n')
	}
	b.WriteString("}\n")
	return b.String()
}

// ensureImports adds `import fmt` / `import reflect` to the
// import list if the rendered stmts / decls reference them and
// the user has not imported them yet. This is what enables the
// REPL's auto-print to work without the user having to type
// `import fmt` / `import reflect` first.
func ensureImports(imports, stmts, decls []string) []string {
	have := map[string]bool{}
	for _, imp := range imports {
		path := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(imp), "import"))
		path = strings.TrimSpace(strings.SplitN(path, " ", 2)[0])
		have[path] = true
	}
	need := func(pkg string) bool {
		needle := pkg + "."
		for _, st := range stmts {
			if strings.Contains(st, needle) {
				return true
			}
		}
		for _, d := range decls {
			if strings.Contains(d, needle) {
				return true
			}
		}
		return false
	}
	out := append([]string{}, imports...)
	for _, pkg := range []string{"fmt", "reflect"} {
		if !have[pkg] && need(pkg) {
			out = append(out, "import "+pkg)
		}
	}
	return out
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
		trimmed := strings.TrimLeft(st.src, " \t")
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

// importSilencesFor returns a Tide expression per imported
// package referencing an exported symbol so the generated Go
// file always "uses" the import. Operates over the rendered
// import list (which can include synthetic fmt / reflect from
// ensureImports), so silence-uses are emitted for them too.
func importSilencesFor(imports []string) []string {
	var out []string
	for _, imp := range imports {
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
	"reflect":  "reflect.TypeOf",
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

	bin := filepath.Join(src.dir, "repl.bin")
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
	fields := strings.Fields(line)
	switch fields[0] {
	case ":quit", ":q":
		return true
	case ":help":
		fmt.Fprint(stdout, helpText)
	case ":reset":
		// `:reset` wipes the whole session; `:reset main` wipes
		// only the main() body — imports and decls (func / class
		// / type / interface) survive so the user can iterate on
		// statements against an established scaffolding without
		// re-declaring it. The rejected set is cleared in both
		// modes because its entries are exact-text keys; after
		// a body-only reset the user may want to retry a
		// previously broken stmt against the surviving scaffolding.
		if len(fields) > 1 && fields[1] == "main" {
			s.stmts = nil
			s.rejected = nil
			s.lastSlot = slotNone
			fmt.Fprintln(stdout, "(main body cleared; imports and declarations kept)")
		} else {
			*s = replSession{}
			fmt.Fprintln(stdout, "(session cleared)")
		}
	case ":show":
		// `:show` is a diagnostic aid — print the original
		// user-typed source, not the auto-print-wrapped render
		// that the run harness sees. The wrap version belongs
		// inside the compile harness; here we want a faithful
		// echo of what the user has actually given the REPL.
		fmt.Fprint(stdout, s.renderOriginal())
	case ":imports":
		if len(s.imports) == 0 {
			fmt.Fprintln(stdout, "(no imports)")
		} else {
			for _, imp := range s.imports {
				fmt.Fprintln(stdout, imp)
			}
		}
	case ":type", ":inspect":
		expr := strings.TrimSpace(strings.TrimPrefix(line, fields[0]))
		if expr == "" {
			fmt.Fprintf(stderr, "repl: %s expects an expression\n", fields[0])
			return false
		}
		oneShot(s, fields[0], expr, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "repl: unknown meta-command %s\n", fields[0])
	}
	return false
}

// oneShot runs `:type` / `:inspect` against the live session
// without persisting the synthesised stmt. The session is
// rendered with an extra stmt that prints the requested
// reflection answer, compiled and executed; success / failure
// is the meta-command's own — the persistent session is not
// touched.
func oneShot(s *replSession, kind, expr string, stdout, stderr io.Writer) {
	var stmt string
	switch kind {
	case ":type":
		stmt = "fmt.println(reflect.typeName(reflect.typeOf(reflect.box((" + expr + ")))))"
	case ":inspect":
		stmt = "fmt.println(reflect.show(reflect.box((" + expr + "))))"
	}
	src, err := compileSourceToTempGo(s.renderWith([]string{stmt}), "repl.td")
	if err != nil {
		fmt.Fprintln(stderr, "repl:", err)
		return
	}
	defer os.RemoveAll(src.dir)
	bin := filepath.Join(src.dir, "repl.bin")
	build := exec.Command("go", "build", "-o", bin, "./...")
	build.Dir = src.dir
	build.Stdout = stderr
	build.Stderr = stderr
	if err := build.Run(); err != nil {
		fmt.Fprintln(stderr, "repl: meta-command compile failed")
		return
	}
	run := exec.Command(bin)
	run.Dir = src.dir
	run.Stdout = stdout
	run.Stderr = stderr
	_ = run.Run()
}

const helpText = `Meta-commands:
  :help                show this list
  :quit / :q           exit the REPL
  :reset               drop all session state
  :reset main          drop only the main() body (keep imports + decls)
  :imports             list active imports
  :show                print the accumulated session source
  :type <expr>         print the static type of <expr>
  :inspect <expr>      pretty-print <expr> via reflect.show

Inputs accepted in v1:
  import <path>                              add an import
  func / class / type / interface            top-level declaration
  let / var / <lvalue> = <expr>              session-scoped statement
  <expr>                                     auto-print via reflect.show
  (the _ / _error last-value bindings land with PR-REPL-2b.)
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
