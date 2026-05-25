package parser

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/heni/tide-lang/internal/ast"
	"github.com/heni/tide-lang/internal/lexer"
)

// TestFixtures walks every *.txt manifest in ../../tests/grammar/,
// lexes + parses the INPUT, and diffs the canonical AST
// S-expression against the AST section.
//
// Manifest format: see lang-spec/test-contract.md §"File format".
func TestFixtures(t *testing.T) {
	root := filepath.Join("..", "..", "tests", "grammar")
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

			toks, lerr := lexer.LexFile(input, "src.td")
			if lerr != nil {
				if want, ok := sections["ERRORS"]; ok {
					if strings.TrimSpace(lerr.Error()) != strings.TrimSpace(want) {
						t.Errorf("%s: ERRORS mismatch\n got: %s\nwant: %s", name, lerr.Error(), want)
					}
					return
				}
				t.Fatalf("%s: lex error: %v", name, lerr)
			}

			f, perr := ParseFile(toks, "src.td")
			if perr != nil {
				if want, ok := sections["ERRORS"]; ok {
					if strings.TrimSpace(perr.Error()) != strings.TrimSpace(want) {
						t.Errorf("%s: ERRORS mismatch\n got: %s\nwant: %s", name, perr.Error(), want)
					}
					return
				}
				t.Fatalf("%s: parse error: %v", name, perr)
			}

			if want, ok := sections["AST"]; ok {
				got := ast.Canonical(f)
				if normaliseAST(got) != normaliseAST(want) {
					t.Errorf("%s: AST mismatch\n--- got ---\n%s\n--- want ---\n%s",
						name, got, want)
				}
				return
			}
			t.Fatalf("%s: manifest has neither AST nor ERRORS section", name)
		})
	}
}

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
		body = strings.TrimPrefix(body, "\n")
		body = strings.TrimRight(body, "\n")
		out[name] = body
	}
	return out
}

// normaliseAST canonicalises whitespace: strips trailing
// spaces / tabs per line and trailing newlines. Indentation is
// significant (it's part of the canonical form) so internal
// whitespace is preserved.
func normaliseAST(s string) string {
	s = strings.TrimRight(s, "\n")
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		lines[i] = strings.TrimRight(ln, " \t")
	}
	return strings.Join(lines, "\n")
}
