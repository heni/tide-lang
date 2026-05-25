package codegen

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/heni/tide-lang/internal/lexer"
	"github.com/heni/tide-lang/internal/parser"
)

// TestFixtures walks every *.txt manifest in ../../tests/codegen/,
// lexes + parses + emits Go for INPUT, and byte-compares the
// result against the GO section. STDOUT / EXIT sections are
// declared but executed in PR-D's integration runner.
func TestFixtures(t *testing.T) {
	root := filepath.Join("..", "..", "tests", "codegen")
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
			toks, lerr := lexer.Lex(input)
			if lerr != nil {
				t.Fatalf("%s: lex error: %v", name, lerr)
			}
			f, perr := parser.Parse(toks)
			if perr != nil {
				t.Fatalf("%s: parse error: %v", name, perr)
			}
			got, err := Emit(f, "")
			if err != nil {
				t.Fatalf("%s: emit error: %v", name, err)
			}
			want, ok := sections["GO"]
			if !ok {
				t.Fatalf("%s: missing GO section", name)
			}
			if strings.TrimRight(got, "\n") != strings.TrimRight(want, "\n") {
				t.Errorf("%s: GO mismatch\n--- got ---\n%s\n--- want ---\n%s",
					name, got, want)
			}
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
