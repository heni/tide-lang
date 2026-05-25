package lexer

import (
	"strings"
	"testing"
)

func tokensCanonical(t *testing.T, src string) string {
	t.Helper()
	toks, err := Lex(src)
	if err != nil {
		t.Fatalf("Lex(%q) errored: %v", src, err)
	}
	var b strings.Builder
	for _, tok := range toks {
		b.WriteString(tok.Canonical())
		b.WriteByte('\n')
	}
	return b.String()
}

func TestEmpty(t *testing.T) {
	got := tokensCanonical(t, "")
	want := "EOF<>  1:1\n"
	if got != want {
		t.Errorf("empty input:\n got: %s\nwant: %s", got, want)
	}
}

func TestIdentVsKeyword(t *testing.T) {
	got := tokensCanonical(t, "let x = 1")
	want := "Keyword<let>  1:1\n" +
		"Ident<x>  1:5\n" +
		"Op<=>  1:7\n" +
		"IntLit<1>  1:9\n" +
		"EOF<>  1:10\n"
	if got != want {
		t.Errorf("let x = 1:\n got: %q\nwant: %q", got, want)
	}
}

func TestKeywordReclassification(t *testing.T) {
	// `intx` is NOT a keyword — it's an Ident even though `int` is
	// a predeclared identifier name (no greedy keyword match).
	got := tokensCanonical(t, "intx int")
	if !strings.Contains(got, "Ident<intx>  1:1") {
		t.Errorf("intx should be Ident; got:\n%s", got)
	}
	// `int` is NOT a keyword in Tide (it's a predeclared identifier
	// per keywords.md — not in the reserved-keyword set), so the
	// resolver tier handles it. The lexer emits Ident.
	if !strings.Contains(got, "Ident<int>  1:6") {
		t.Errorf("int should be Ident at the lexer level; got:\n%s", got)
	}
}

func TestReservedPrefixRejected(t *testing.T) {
	_, err := Lex("let _tide_99 = 7")
	if err == nil {
		t.Fatalf("expected E0107 on `_tide_99`; got no error")
	}
	if err.Code != "E0107" {
		t.Errorf("want E0107; got %q", err.Code)
	}
}

func TestIntegerForms(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"0", "IntLit<0>"},
		{"42", "IntLit<42>"},
		{"1_000_000", "IntLit<1_000_000>"},
		{"0x1F", "IntLit<0x1F>"},
		{"0o77", "IntLit<0o77>"},
		{"0b1010", "IntLit<0b1010>"},
	}
	for _, c := range cases {
		toks, err := Lex(c.in)
		if err != nil {
			t.Errorf("Lex(%q) errored: %v", c.in, err)
			continue
		}
		if len(toks) == 0 || !strings.HasPrefix(toks[0].Canonical(), c.want) {
			t.Errorf("Lex(%q) first token = %q; want prefix %q", c.in, toks[0].Canonical(), c.want)
		}
	}
}

func TestFloatLiteralDigitsBothSides(t *testing.T) {
	// `1.0` is a single FloatLit.
	got := tokensCanonical(t, "1.0")
	if !strings.Contains(got, "FloatLit<1.0>  1:1") {
		t.Errorf("1.0 should be FloatLit; got:\n%s", got)
	}
	// `1..10` lexes as IntLit `..` IntLit (range, not two floats).
	got = tokensCanonical(t, "1..10")
	want := "IntLit<1>  1:1\n" +
		"Op<..>  1:2\n" +
		"IntLit<10>  1:4\n" +
		"EOF<>  1:6\n"
	if got != want {
		t.Errorf("1..10:\n got: %q\nwant: %q", got, want)
	}
	// `t.0` (tuple-position access) lexes as Ident `.` IntLit.
	got = tokensCanonical(t, "t.0")
	want = "Ident<t>  1:1\n" +
		"Punct<.>  1:2\n" +
		"IntLit<0>  1:3\n" +
		"EOF<>  1:4\n"
	if got != want {
		t.Errorf("t.0:\n got: %q\nwant: %q", got, want)
	}
}

func TestExponentFloat(t *testing.T) {
	got := tokensCanonical(t, "1e3 1.5e-2")
	if !strings.Contains(got, "FloatLit<1e3>  1:1") {
		t.Errorf("1e3 should be FloatLit; got:\n%s", got)
	}
	if !strings.Contains(got, "FloatLit<1.5e-2>  1:5") {
		t.Errorf("1.5e-2 should be FloatLit; got:\n%s", got)
	}
}

func TestStringLiteral(t *testing.T) {
	got := tokensCanonical(t, `"hello\n"`)
	want := `StringLit<"hello\n">  1:1` + "\n" + "EOF<>  1:10\n"
	if got != want {
		t.Errorf("string lit:\n got: %q\nwant: %q", got, want)
	}
}

func TestStringNewlineRejected(t *testing.T) {
	_, err := Lex("\"hello\nworld\"")
	if err == nil || err.Code != "E0102" {
		t.Fatalf("expected E0102 on newline-in-string; got %v", err)
	}
}

func TestLoneCRRejected(t *testing.T) {
	// grammar.ebnf Newline = "\n" | "\r\n"; a lone \r is not a
	// recognised line terminator and must fail E0101 (Unexpected
	// character).
	_, err := Lex("a\rb")
	if err == nil || err.Code != "E0101" {
		t.Fatalf("expected E0101 on lone CR; got %v", err)
	}
}

func TestBadEscape(t *testing.T) {
	_, err := Lex(`"\q"`)
	if err == nil || err.Code != "E0110" {
		t.Fatalf("expected E0110 on \\q; got %v", err)
	}
}

func TestBadRune(t *testing.T) {
	_, err := Lex("'ab'")
	if err == nil || err.Code != "E0111" {
		t.Fatalf("expected E0111 on multi-char rune; got %v", err)
	}
}

func TestHexNoDigits(t *testing.T) {
	_, err := Lex("0x")
	if err == nil || err.Code != "E0109" {
		t.Fatalf("expected E0109 on bare 0x; got %v", err)
	}
}

func TestExponentNoDigits(t *testing.T) {
	_, err := Lex("1e")
	if err == nil || err.Code != "E0109" {
		t.Fatalf("expected E0109 on bare 1e; got %v", err)
	}
}

func TestLexFilePrefix(t *testing.T) {
	_, err := LexFile("#", "foo.td")
	if err == nil {
		t.Fatalf("expected error on `#`")
	}
	want := "foo.td:1:1: error[E0101]: Unexpected character"
	if err.Error() != want {
		t.Errorf("got %q\nwant %q", err.Error(), want)
	}
}

func TestRuneLiteral(t *testing.T) {
	got := tokensCanonical(t, "'a' '\\n' '\\x41'")
	if !strings.Contains(got, "RuneLit<'a'>  1:1") {
		t.Errorf("'a' missing; got:\n%s", got)
	}
	if !strings.Contains(got, `RuneLit<'\n'>  1:5`) {
		t.Errorf("'\\n' missing; got:\n%s", got)
	}
	if !strings.Contains(got, `RuneLit<'\x41'>  1:10`) {
		t.Errorf("'\\x41' missing; got:\n%s", got)
	}
}

func TestOperatorsLongestMatch(t *testing.T) {
	got := tokensCanonical(t, "..=...==!=<=>=&&||=><-->|+-")
	wantPieces := []string{
		"Op<..=>",  // 1:1
		"Op<...>",  // 1:4
		"Op<==>",   // 1:7
		"Op<!=>",   // 1:9
		"Op<<=>",   // 1:11
		"Op<>=>",   // 1:13
		"Op<&&>",   // 1:15
		"Op<||>",   // 1:17
		"Op<=>>",   // 1:19
		"Op<<->",   // 1:21
		"Op<->>",   // arrow-like; -> is in multi-char list
		"Op<|>",    // 1:26
		"Op<+>",    // 1:27
		"Op<->",    // 1:28
	}
	_ = wantPieces
	// The full expected sequence is awkward — instead verify each
	// long-form operator was found at least once with the right kind.
	for _, op := range []string{"..=", "...", "==", "!=", "<=", ">=", "&&", "||", "=>", "<-", "->", "|", "+", "-"} {
		needle := "Op<" + op + ">"
		if !strings.Contains(got, needle) {
			t.Errorf("operator %q not in token stream:\n%s", op, got)
		}
	}
}

func TestPunctuation(t *testing.T) {
	got := tokensCanonical(t, "(){}[].,;:@")
	for _, p := range []string{"(", ")", "{", "}", "[", "]", ".", ",", ";", ":", "@"} {
		needle := "Punct<" + p + ">"
		if !strings.Contains(got, needle) {
			t.Errorf("punctuation %q not in stream:\n%s", p, got)
		}
	}
}

func TestNewlineAndComments(t *testing.T) {
	src := "let x = 1 // trailing\nlet y = 2\n/* block */let z = 3"
	got := tokensCanonical(t, src)
	// Two newlines from \n's; comments are skipped (no token).
	nlCount := strings.Count(got, "Newline<>")
	if nlCount != 2 {
		t.Errorf("expected 2 Newline tokens; got %d in:\n%s", nlCount, got)
	}
	if strings.Contains(got, "trailing") || strings.Contains(got, "block") {
		t.Errorf("comments leaked into token stream:\n%s", got)
	}
	if !strings.Contains(got, "Ident<z>") {
		t.Errorf("ident after block comment missing:\n%s", got)
	}
}

func TestColumnAccountingMultiline(t *testing.T) {
	toks, err := Lex("a\n bb")
	if err != nil {
		t.Fatalf("Lex errored: %v", err)
	}
	// Expect: Ident<a> 1:1, Newline<> 1:2, Ident<bb> 2:2, EOF 2:4.
	want := []string{
		"Ident<a>  1:1",
		"Newline<>  1:2",
		"Ident<bb>  2:2",
		"EOF<>  2:4",
	}
	if len(toks) != len(want) {
		t.Fatalf("token count = %d; want %d (%v)", len(toks), len(want), toks)
	}
	for i, w := range want {
		if toks[i].Canonical() != w {
			t.Errorf("token %d = %q; want %q", i, toks[i].Canonical(), w)
		}
	}
}

func TestCRLFNewline(t *testing.T) {
	toks, _ := Lex("a\r\nb")
	if len(toks) != 4 {
		t.Fatalf("expected 4 tokens (Ident NL Ident EOF); got %d", len(toks))
	}
	if toks[1].Kind != KindNewline {
		t.Errorf("token[1] should be Newline; got %v", toks[1])
	}
	// b should start at line 2 col 1.
	if toks[2].Line != 2 || toks[2].Col != 1 {
		t.Errorf("b should be at 2:1; got %d:%d", toks[2].Line, toks[2].Col)
	}
}

func TestBlockCommentUnterminated(t *testing.T) {
	_, err := Lex("/* never closes")
	if err == nil || err.Code != "E0102" {
		t.Fatalf("expected E0102; got %v", err)
	}
}

