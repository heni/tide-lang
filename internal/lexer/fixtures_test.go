package lexer

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestFixtures walks every *.txt manifest in ../../tests/lexer/,
// lexes the INPUT section, and diffs the canonical token stream
// against the TOKENS section (or, for negative cases, the ERRORS
// section against the diagnostic the lexer produced).
//
// Manifest format: see lang-spec/test-contract.md §"File format".
func TestFixtures(t *testing.T) {
	root := filepath.Join("..", "..", "tests", "lexer")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read %s: %v", root, err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".txt") {
			continue
		}
		name := e.Name()
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(root, name)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			sections := parseManifest(string(data))
			input, ok := sections["INPUT"]
			if !ok {
				t.Fatalf("%s: missing INPUT section", name)
			}

			tokens, diag := LexFile(input, "src.td")

			if want, ok := sections["TOKENS"]; ok {
				got := serializeTokens(tokens)
				if normaliseWS(got) != normaliseWS(want) {
					t.Errorf("%s: TOKENS mismatch\n--- got ---\n%s\n--- want ---\n%s",
						name, got, want)
				}
				if diag != nil {
					t.Errorf("%s: TOKENS expected but lexer also errored: %v", name, diag)
				}
				return
			}

			if want, ok := sections["ERRORS"]; ok {
				if diag == nil {
					t.Errorf("%s: ERRORS expected but lexer succeeded; tokens:\n%s",
						name, serializeTokens(tokens))
					return
				}
				got := diag.Error()
				if strings.TrimSpace(got) != strings.TrimSpace(want) {
					t.Errorf("%s: ERROR mismatch\n got: %s\nwant: %s", name, got, want)
				}
				return
			}

			t.Fatalf("%s: manifest has neither TOKENS nor ERRORS section", name)
		})
	}
}

// parseManifest returns a map from section name (e.g. "INPUT",
// "TOKENS") to its body. Section bodies preserve internal whitespace
// but the leading and trailing blank line surrounding the delimiter
// are stripped.
func parseManifest(s string) map[string]string {
	delim := regexp.MustCompile(`(?m)^---\s+([A-Z_]+)\s+---\s*$`)
	matches := delim.FindAllStringSubmatchIndex(s, -1)
	out := map[string]string{}
	for i, m := range matches {
		name := s[m[2]:m[3]]
		bodyStart := m[1]
		bodyEnd := len(s)
		if i+1 < len(matches) {
			bodyEnd = matches[i+1][0]
		}
		body := s[bodyStart:bodyEnd]
		// The byte immediately after `--- NAME ---` is always a
		// newline (the delimiter occupies its own line); strip it.
		body = strings.TrimPrefix(body, "\n")
		// Strip all trailing newlines — fixture writers can rely
		// on "INPUT body ends with the last visible line", with
		// no spurious trailing newline. Tokens for input-internal
		// newlines (between lines of a multi-line input) still
		// emit; only the trailing whitespace is normalised away.
		body = strings.TrimRight(body, "\n")
		out[name] = body
	}
	return out
}

func serializeTokens(toks []Token) string {
	var b strings.Builder
	for _, t := range toks {
		b.WriteString(t.Canonical())
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

// normaliseWS collapses any run of horizontal whitespace into a
// single space so that fixture writers can pad the lexeme-to-span
// gap with any width ≥ 2 (per test-contract.md §TOKENS).
func normaliseWS(s string) string {
	s = strings.TrimRight(s, "\n")
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		lines[i] = collapseHWS(strings.TrimRight(ln, " \t"))
	}
	return strings.Join(lines, "\n")
}

var hwsRun = regexp.MustCompile(`[ \t]+`)

func collapseHWS(s string) string { return hwsRun.ReplaceAllString(s, " ") }
