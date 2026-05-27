package main

import (
	"fmt"
	"os"
	"strings"

	prompt "github.com/c-bata/go-prompt"
)

// runReplPrompt is the Tier-2 (go-prompt) entry point. Live in a
// real terminal: up-arrow history, in-line edit, cursor keys,
// stable prompt rendering. Falls back to the bufio.Scanner path
// (runRepl) when stdin is not a TTY so piped-input tests keep
// working unchanged.
//
// Multi-line input is accumulated in a buffer between Executor
// calls; once `balanced()` reports the buffer closed, the
// accumulated text feeds into the same session machinery used
// by the Tier-1 path. The continuation prompt displays the
// current `{`-depth as leading dots so the user has a visual
// auto-indent hint while typing nested blocks.
//
// Per-token syntax highlighting (e.g. keywords in cyan) needs
// a Lexer hook that c-bata/go-prompt v0.2.6 does not expose;
// the elk-language fork adds it but requires Go 1.24+. We stick
// with single-colour user text for now and revisit once we bump
// the toolchain.
func runReplPrompt() int {
	fmt.Println(replBanner)
	state := &promptState{sess: &replSession{}}
	p := prompt.New(
		state.execute,
		emptyCompleter,
		prompt.OptionPrefix(replPrompt),
		prompt.OptionLivePrefix(state.livePrefix),
		prompt.OptionTitle("tide"),
		prompt.OptionInputTextColor(prompt.Cyan),
		prompt.OptionPrefixTextColor(prompt.Green),
		prompt.OptionAddKeyBind(prompt.KeyBind{
			Key: prompt.ControlC,
			Fn:  state.handleCtrlC,
		}),
	)
	p.Run()
	return state.exitCode
}

// promptState is the per-session state held across go-prompt
// Executor invocations. go-prompt runs Executor once per Enter,
// so multi-line input must be accumulated in `buf` between
// calls and reset only when the buffer is balanced.
type promptState struct {
	sess     *replSession
	buf      strings.Builder
	exitCode int
}

// livePrefix returns the prompt prefix for the next input. A
// fresh input gets `tide> `; a continuation gets `... > ` plus
// two spaces per open brace so the user sees a visual
// auto-indent cue mirroring the depth of the current block.
func (s *promptState) livePrefix() (string, bool) {
	if s.buf.Len() == 0 {
		return replPrompt, false
	}
	depth := openDepth(s.buf.String())
	if depth < 0 {
		depth = 0
	}
	return replContPrompt + strings.Repeat("  ", depth), true
}

// handleCtrlC: on a non-empty multi-line buffer, abandon the
// buffer (RFC §Multi-line input). On a fresh prompt, do not
// exit — convention (Python, Node, irb) is that Ctrl-C at the
// top level only clears the line and prints a hint. Termination
// is reserved for `:quit` / Ctrl-D.
func (s *promptState) handleCtrlC(buf *prompt.Buffer) {
	if s.buf.Len() == 0 {
		fmt.Fprintln(os.Stdout, "(use :quit or Ctrl-D to exit)")
		return
	}
	s.buf.Reset()
	fmt.Fprintln(os.Stdout, "(input cancelled)")
}

func (s *promptState) execute(line string) {
	// Empty line on a fresh prompt is just "press Enter" — do
	// nothing. On a continuation it preserves the blank inside
	// the multi-line buffer (could matter for a string literal).
	if s.buf.Len() == 0 && strings.TrimSpace(line) == "" {
		return
	}
	if s.buf.Len() == 0 && strings.HasPrefix(strings.TrimSpace(line), ":") {
		if handleMeta(strings.TrimSpace(line), s.sess, os.Stdout, os.Stderr) {
			// :quit / :q — go-prompt has no clean Stop() in
			// v0.2.6, so exit the process with the REPL's
			// own exit code.
			// TODO: replace with prompt.Prompt.Stop() if a
			// later release adds one; the current os.Exit
			// bypasses any future deferred cleanup
			// (history-file flush, runSession finalisers).
			os.Exit(s.exitCode)
		}
		return
	}
	if s.buf.Len() > 0 {
		s.buf.WriteByte('\n')
	}
	s.buf.WriteString(line)
	if !balanced(s.buf.String()) {
		return
	}
	input := strings.TrimSpace(s.buf.String())
	s.buf.Reset()
	if input == "" {
		return
	}
	if err := s.sess.add(input); err != nil {
		fmt.Fprintln(os.Stderr, "repl:", err)
		return
	}
	if len(s.sess.stmts) == 0 {
		return
	}
	if err := runSession(s.sess, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "repl:", err)
		s.sess.markRejected(input)
		s.sess.rollback()
	}
}

// openDepth reports the net `{`-depth of the buffer (other
// bracket kinds collapse without affecting indentation). String
// / char / comment regions are skipped so `{` inside a string
// literal doesn't tilt the indent.
func openDepth(src string) int {
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
				if src[j] == '\n' {
					break
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
		case '{':
			depth++
		case '}':
			depth--
		}
		i++
	}
	return depth
}

// emptyCompleter is the go-prompt completer hook. No
// suggestions in v1 — completion (variable names, decl names,
// stdlib symbols) is a follow-up. Returning an empty slice
// keeps the dropdown UI dormant.
func emptyCompleter(_ prompt.Document) []prompt.Suggest { return nil }
