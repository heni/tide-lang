package parser

import (
	"regexp"
	"testing"

	"github.com/heni/tide-lang/internal/ast"
	"github.com/heni/tide-lang/internal/lexer"
)

// spanRE strips the `@line:col-line:col` coordinate suffixes from a
// canonical AST S-expression, leaving only the structural shape. Two
// layouts of the same program differ only in coordinates, so a
// span-stripped comparison is the equivalence we want to assert.
var spanRE = regexp.MustCompile(` @\d+:\d+-\d+:\d+`)

// parseShape lexes+parses src and returns its span-stripped canonical
// AST. ok is false (with a diagnostic string) if lexing or parsing
// failed.
func parseShape(src string) (shape string, diag string, ok bool) {
	toks, lerr := lexer.LexFile(src, "src.td")
	if lerr != nil {
		return "", lerr.Error(), false
	}
	f, perr := ParseFile(toks, "src.td")
	if perr != nil {
		return "", perr.Error(), false
	}
	return spanRE.ReplaceAllString(ast.Canonical(f), ""), "", true
}

// TestLayoutEquivalence is the layout-robustness guard: each case pairs
// a canonical single-line program with variants that insert newlines,
// whitespace, and comments at continuation positions the grammar deems
// insignificant (inside `(...)`/`[...]`/`<...>`). Every variant must
// parse to the same span-stripped AST as the canonical form. This
// catches the *class* of newline-suppression bugs that per-construct
// fixtures miss (grammar.ebnf §SyntaxNewlineSuppression).
func TestLayoutEquivalence(t *testing.T) {
	cases := []struct {
		name     string
		canon    string
		variants []string
	}{
		{
			name:  "binary inside parens",
			canon: "func main() { let x = (1 + 2)\n}",
			variants: []string{
				"func main() { let x = (1 +\n2)\n}",        // newline after operator
				"func main() { let x = (1\n+ 2)\n}",        // newline before operator
				"func main() { let x = (  1  +  2  )\n}",   // extra whitespace
				"func main() { let x = (1 + /* c */ 2)\n}", // block comment
				"func main() { let x = (1 + // c\n2)\n}",   // line comment + newline
				"func main() { let x = (\n  1 + 2\n)\n}",   // newlines after ( and before )
			},
		},
		{
			name:  "multiline boolean condition",
			canon: "func main() { if (1 < 2 && 3 < 4) { return }\n}",
			variants: []string{
				"func main() { if (1 < 2 &&\n3 < 4) { return }\n}",
				"func main() { if (1 < 2\n&& 3 < 4) { return }\n}",
			},
		},
		{
			name:  "call arguments",
			canon: `func main() { print(1, 2, 3) }`,
			variants: []string{
				"func main() { print(\n1, 2, 3) }",
				"func main() { print(1,\n2,\n3) }",
				"func main() { print(1, /* a */ 2, // b\n3) }",
				"func main() { print(1,\t2,   3) }",
			},
		},
		{
			name: "inferred slice literal",
			canon: `func main() { let xs = [1, 2, 3]
}`,
			variants: []string{
				"func main() { let xs = [\n1,\n2,\n3,\n]\n}",  // newlines + trailing comma
				"func main() { let xs = [1, /* c */ 2, 3]\n}", // comment
				"func main() { let xs = [ 1 , 2 , 3 ]\n}",     // whitespace
			},
		},
		{
			name:  "generic type-argument list",
			canon: "func main() { let m = Map<int, string>{}\n}",
			variants: []string{
				"func main() { let m = Map<int,\nstring>{}\n}",   // newline after `,`
				"func main() { let m = Map<int, string\n>{}\n}",  // newline before `>`
				"func main() { let m = Map<\nint, string>{}\n}",  // newline after `<`
				"func main() { let m = Map< int , string >{}\n}", // whitespace
			},
		},
		{
			name:  "leading-dot method chain",
			canon: "func main() { let s = items.filter(p).map(f)\n}",
			variants: []string{
				"func main() { let s = items\n.filter(p)\n.map(f)\n}",      // newline before each `.`
				"func main() { let s = items // c\n.filter(p)\n.map(f)\n}", // line comment before `.`
				"func main() { let s = items\n  .filter(p) .map(f)\n}",     // mixed
			},
		},
		{
			name:  "block interior keeps significant newlines under enclosing parens",
			canon: "func apply(f: func(): int): int { return f() }\nfunc main() { let r = apply(func(): int { let a = 1\nreturn a })\n}",
			variants: []string{
				// The closure body's newlines stay significant even though
				// the whole thing is inside apply(...)'s parens.
				"func apply(f: func(): int): int { return f() }\nfunc main() { let r = apply(func(): int {\nlet a = 1\nreturn a\n})\n}",
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			want, diag, ok := parseShape(c.canon)
			if !ok {
				t.Fatalf("canonical form failed to parse: %s\nsrc:\n%s", diag, c.canon)
			}
			for i, v := range c.variants {
				got, diag, ok := parseShape(v)
				if !ok {
					t.Errorf("variant %d failed to parse: %s\nsrc:\n%s", i, diag, v)
					continue
				}
				if got != want {
					t.Errorf("variant %d AST differs from canonical\n--- canonical ---\n%s\n--- variant %d ---\n%s", i, want, i, got)
				}
			}
		})
	}
}

// TestUnbracketedContinuationStillErrors locks the design decision
// (2026-06-13): outside brackets a newline terminates the expression —
// operator/leading-operator continuation is NOT adopted. (Leading-`.`
// method-chain continuation is handled separately and is allowed.)
func TestUnbracketedContinuationStillErrors(t *testing.T) {
	srcs := []string{
		"func main() { let x = 1 +\n2\n}", // trailing operator, no brackets
		"func main() { let x = 1\n+ 2\n}", // leading operator, no brackets
	}
	for i, src := range srcs {
		if _, _, ok := parseShape(src); ok {
			t.Errorf("case %d: expected a parse error for unbracketed continuation, got none\nsrc:\n%s", i, src)
		}
	}
}
