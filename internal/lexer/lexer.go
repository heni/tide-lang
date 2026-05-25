// Package lexer turns Tide source text into a stream of tokens.
//
// Contract: lang-spec/grammar.ebnf lexical part.
// Canonical token serialization: lang-spec/test-contract.md §TOKENS.
package lexer

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Kind is the lexical category of a token, matching the names in
// grammar.ebnf and test-contract.md.
type Kind int

const (
	KindInvalid Kind = iota
	KindNewline
	KindIdent
	KindKeyword
	KindIntLit
	KindFloatLit
	KindStringLit
	KindRuneLit
	KindOp
	KindPunct
	KindEOF
)

var kindName = map[Kind]string{
	KindNewline:   "Newline",
	KindIdent:     "Ident",
	KindKeyword:   "Keyword",
	KindIntLit:    "IntLit",
	KindFloatLit:  "FloatLit",
	KindStringLit: "StringLit",
	KindRuneLit:   "RuneLit",
	KindOp:        "Op",
	KindPunct:     "Punct",
	KindEOF:       "EOF",
}

// String returns the canonical token-kind name (per test-contract.md).
func (k Kind) String() string {
	if s, ok := kindName[k]; ok {
		return s
	}
	return "Invalid"
}

// Token is one element of the lexer output stream.
type Token struct {
	Kind   Kind
	Lexeme string // source text the token covers; empty for Newline / EOF
	Line   int    // 1-indexed line number of the first character
	Col    int    // 1-indexed character (not byte) column
}

// Canonical returns the test-contract.md §TOKENS representation:
//
//	Kind<lexeme>  line:col
//
// Newline and EOF use an empty lexeme `<>`.
func (t Token) Canonical() string {
	return fmt.Sprintf("%s<%s>  %d:%d", t.Kind, t.Lexeme, t.Line, t.Col)
}

// Diag is a lexer-level diagnostic. Lexer halts on the first error
// and returns the tokens accumulated so far together with the error.
type Diag struct {
	Code    string // E0xxx; see diagnostics.md
	Message string
	Line    int
	Col     int
}

func (d *Diag) Error() string {
	return fmt.Sprintf("%d:%d: error[%s]: %s", d.Line, d.Col, d.Code, d.Message)
}

// keywordSet is the closed reclassification table from grammar.ebnf.
var keywordSet = map[string]bool{
	"import": true, "type": true, "class": true, "interface": true,
	"implements": true, "extends": true, "static": true,
	"func": true, "let": true, "var": true,
	"if": true, "else": true,
	"for": true, "in": true, "while": true, "return": true,
	"match": true, "try": true, "defer": true,
	"spawn": true, "scope": true, "select": true,
	"break": true, "continue": true,
	"true": true, "false": true, "unit": true,
	"this": true,
}

// Multi-character operator alternatives, sorted longest-first so that
// a linear scan picks the longest match (..= before .., etc.).
var multiCharOps = []string{
	"..=", "...",
	"..", "==", "!=", "<=", ">=", "&&", "||", "=>", "->", "<-",
}

// punctChars maps single-character punctuation tokens.
var punctChars = "(){}[].,;:@"

// singleCharOps maps single-character operators.
var singleCharOps = "+-*/%=!<>|"

// Lex scans the source text and returns the token stream.
// The terminating EOF token is always emitted.
func Lex(src string) ([]Token, *Diag) {
	l := &lexer{src: src, line: 1, col: 1, offset: 0}
	for !l.eof() {
		if err := l.next(); err != nil {
			return l.tokens, err
		}
	}
	l.emit(KindEOF, "", l.line, l.col)
	return l.tokens, nil
}

type lexer struct {
	src    string
	offset int // byte offset into src
	line   int // 1-indexed line of the next char to consume
	col    int // 1-indexed character column of the next char
	tokens []Token
}

func (l *lexer) eof() bool { return l.offset >= len(l.src) }

func (l *lexer) emit(k Kind, lexeme string, line, col int) {
	l.tokens = append(l.tokens, Token{Kind: k, Lexeme: lexeme, Line: line, Col: col})
}

// peek returns the rune at the current offset without consuming it.
// Returns (rune, byteSize). At EOF returns (-1, 0).
func (l *lexer) peek() (rune, int) {
	if l.eof() {
		return -1, 0
	}
	r, sz := utf8.DecodeRuneInString(l.src[l.offset:])
	return r, sz
}

// peekAt returns the rune at the current offset plus byte-offset n.
func (l *lexer) peekAt(byteOffset int) (rune, int) {
	off := l.offset + byteOffset
	if off >= len(l.src) {
		return -1, 0
	}
	r, sz := utf8.DecodeRuneInString(l.src[off:])
	return r, sz
}

// hasPrefix checks whether the source at the current offset begins
// with s.
func (l *lexer) hasPrefix(s string) bool {
	return strings.HasPrefix(l.src[l.offset:], s)
}

// advance consumes n bytes (assumed to be a complete UTF-8 sequence
// or ASCII), advancing line/col accordingly.
func (l *lexer) advance(n int) {
	for i := 0; i < n; {
		r, sz := utf8.DecodeRuneInString(l.src[l.offset+i:])
		if r == '\n' {
			l.line++
			l.col = 1
		} else {
			l.col++
		}
		i += sz
	}
	l.offset += n
}

// next consumes the next token (or skip / error).
func (l *lexer) next() *Diag {
	r, _ := l.peek()

	// Whitespace (space / tab) — skip.
	if r == ' ' || r == '\t' {
		l.advance(1)
		return nil
	}

	// Newline — emit as token. \r\n collapses to one newline.
	if r == '\n' {
		startLine, startCol := l.line, l.col
		l.advance(1)
		l.emit(KindNewline, "", startLine, startCol)
		return nil
	}
	if r == '\r' {
		startLine, startCol := l.line, l.col
		if l.peekAtIs(1, '\n') {
			l.advance(2)
		} else {
			l.advance(1)
		}
		l.emit(KindNewline, "", startLine, startCol)
		return nil
	}

	// Comments.
	if r == '/' {
		if r2, _ := l.peekAt(1); r2 == '/' {
			l.lineComment()
			return nil
		}
		if r2, _ := l.peekAt(1); r2 == '*' {
			return l.blockComment()
		}
	}

	startLine, startCol, startOffset := l.line, l.col, l.offset

	// Identifier / keyword.
	if isLetter(r) {
		for {
			r, sz := l.peek()
			if !isLetter(r) && !isDigit(r) {
				break
			}
			l.advance(sz)
		}
		lex := l.src[startOffset:l.offset]
		if strings.HasPrefix(lex, "_tide_") {
			return &Diag{
				Code:    "E0107",
				Message: "Reserved identifier prefix",
				Line:    startLine,
				Col:     startCol,
			}
		}
		if keywordSet[lex] {
			l.emit(KindKeyword, lex, startLine, startCol)
		} else {
			l.emit(KindIdent, lex, startLine, startCol)
		}
		return nil
	}

	// Numeric literal (Int or Float).
	if isDigit(r) {
		return l.numberLit(startLine, startCol, startOffset)
	}

	// String literal.
	if r == '"' {
		return l.stringLit(startLine, startCol, startOffset)
	}

	// Rune literal.
	if r == '\'' {
		return l.runeLit(startLine, startCol, startOffset)
	}

	// Multi-character operator (longest-match).
	for _, op := range multiCharOps {
		if l.hasPrefix(op) {
			l.advance(len(op))
			l.emit(KindOp, op, startLine, startCol)
			return nil
		}
	}

	// Single-character operator.
	if strings.ContainsRune(singleCharOps, r) {
		l.advance(1)
		l.emit(KindOp, string(r), startLine, startCol)
		return nil
	}

	// Punctuation.
	if strings.ContainsRune(punctChars, r) {
		l.advance(1)
		l.emit(KindPunct, string(r), startLine, startCol)
		return nil
	}

	return &Diag{
		Code:    "E0101",
		Message: fmt.Sprintf("Unexpected token: %q", string(r)),
		Line:    startLine,
		Col:     startCol,
	}
}

func (l *lexer) peekAtIs(byteOffset int, want rune) bool {
	r, _ := l.peekAt(byteOffset)
	return r == want
}

func (l *lexer) lineComment() {
	// We're at the leading '/'. Consume up to (but not including)
	// the next newline; the newline is emitted as a Token by next().
	for !l.eof() {
		r, sz := l.peek()
		if r == '\n' || r == '\r' {
			return
		}
		l.advance(sz)
	}
}

func (l *lexer) blockComment() *Diag {
	startLine, startCol := l.line, l.col
	l.advance(2) // consume "/*"
	for !l.eof() {
		if l.hasPrefix("*/") {
			l.advance(2)
			return nil
		}
		_, sz := l.peek()
		l.advance(sz)
	}
	return &Diag{
		Code:    "E0102",
		Message: "Unterminated block comment",
		Line:    startLine,
		Col:     startCol,
	}
}

func (l *lexer) numberLit(startLine, startCol, startOffset int) *Diag {
	// Check for hex / octal / binary prefixes.
	r, _ := l.peek()
	if r == '0' {
		r2, _ := l.peekAt(1)
		switch r2 {
		case 'x', 'X':
			l.advance(2)
			return l.intLitRadix(startLine, startCol, startOffset, isHexDigit)
		case 'o', 'O':
			l.advance(2)
			return l.intLitRadix(startLine, startCol, startOffset, isOctDigit)
		case 'b', 'B':
			l.advance(2)
			return l.intLitRadix(startLine, startCol, startOffset, isBinDigit)
		}
	}

	// Decimal digits.
	for {
		r, sz := l.peek()
		if !isDigit(r) && r != '_' {
			break
		}
		l.advance(sz)
	}

	// Float? Requires `.` followed by at least one digit.
	if r, _ := l.peek(); r == '.' {
		r2, _ := l.peekAt(1)
		if isDigit(r2) {
			l.advance(1) // consume '.'
			for {
				r, sz := l.peek()
				if !isDigit(r) && r != '_' {
					break
				}
				l.advance(sz)
			}
			// Optional exponent.
			if r, _ := l.peek(); r == 'e' || r == 'E' {
				if d := l.consumeExponent(); d != nil {
					return d
				}
			}
			l.emit(KindFloatLit, l.src[startOffset:l.offset], startLine, startCol)
			return nil
		}
	}

	// Exponent without a fractional part: 1e3 is also a FloatLit.
	if r, _ := l.peek(); r == 'e' || r == 'E' {
		if d := l.consumeExponent(); d != nil {
			return d
		}
		l.emit(KindFloatLit, l.src[startOffset:l.offset], startLine, startCol)
		return nil
	}

	l.emit(KindIntLit, l.src[startOffset:l.offset], startLine, startCol)
	return nil
}

func (l *lexer) consumeExponent() *Diag {
	startLine, startCol := l.line, l.col
	l.advance(1) // consume 'e' or 'E'
	if r, _ := l.peek(); r == '+' || r == '-' {
		l.advance(1)
	}
	if r, _ := l.peek(); !isDigit(r) {
		return &Diag{
			Code:    "E0101",
			Message: "Exponent has no digits",
			Line:    startLine,
			Col:     startCol,
		}
	}
	for {
		r, sz := l.peek()
		if !isDigit(r) {
			break
		}
		l.advance(sz)
	}
	return nil
}

func (l *lexer) intLitRadix(
	startLine, startCol, startOffset int,
	digitOK func(rune) bool,
) *Diag {
	hasDigit := false
	for {
		r, sz := l.peek()
		if !digitOK(r) && r != '_' {
			break
		}
		if r != '_' {
			hasDigit = true
		}
		l.advance(sz)
	}
	if !hasDigit {
		return &Diag{
			Code:    "E0101",
			Message: "Integer literal has no digits after radix prefix",
			Line:    startLine,
			Col:     startCol,
		}
	}
	l.emit(KindIntLit, l.src[startOffset:l.offset], startLine, startCol)
	return nil
}

func (l *lexer) stringLit(startLine, startCol, startOffset int) *Diag {
	l.advance(1) // consume opening '"'
	for {
		if l.eof() {
			return &Diag{Code: "E0102", Message: "Unterminated string literal", Line: startLine, Col: startCol}
		}
		r, sz := l.peek()
		if r == '\n' || r == '\r' {
			return &Diag{Code: "E0102", Message: "Unterminated string literal (newline in string)", Line: startLine, Col: startCol}
		}
		if r == '"' {
			l.advance(sz)
			l.emit(KindStringLit, l.src[startOffset:l.offset], startLine, startCol)
			return nil
		}
		if r == '\\' {
			if d := l.consumeEscape(); d != nil {
				return d
			}
			continue
		}
		l.advance(sz)
	}
}

func (l *lexer) runeLit(startLine, startCol, startOffset int) *Diag {
	l.advance(1) // consume opening '\''
	if l.eof() {
		return &Diag{Code: "E0102", Message: "Unterminated rune literal", Line: startLine, Col: startCol}
	}
	r, sz := l.peek()
	if r == '\n' || r == '\r' || r == '\'' {
		return &Diag{Code: "E0101", Message: "Empty or malformed rune literal", Line: startLine, Col: startCol}
	}
	if r == '\\' {
		if d := l.consumeEscape(); d != nil {
			return d
		}
	} else {
		l.advance(sz)
	}
	if r2, _ := l.peek(); r2 != '\'' {
		return &Diag{Code: "E0101", Message: "Rune literal must contain exactly one character", Line: startLine, Col: startCol}
	}
	l.advance(1) // consume closing '\''
	l.emit(KindRuneLit, l.src[startOffset:l.offset], startLine, startCol)
	return nil
}

// consumeEscape consumes a "\X" sequence per EscapeChar in grammar.ebnf.
// On entry the lexer points at the leading backslash.
func (l *lexer) consumeEscape() *Diag {
	startLine, startCol := l.line, l.col
	l.advance(1) // consume '\\'
	if l.eof() {
		return &Diag{Code: "E0102", Message: "Trailing backslash in string/rune literal", Line: startLine, Col: startCol}
	}
	r, sz := l.peek()
	switch r {
	case 'n', 't', 'r', '\\', '"', '\'', '0':
		l.advance(sz)
		return nil
	case 'x':
		l.advance(sz)
		return l.consumeHexDigits(2, startLine, startCol)
	case 'u':
		l.advance(sz)
		return l.consumeHexDigits(4, startLine, startCol)
	default:
		return &Diag{
			Code:    "E0101",
			Message: fmt.Sprintf("Unknown escape sequence \\%s", string(r)),
			Line:    startLine,
			Col:     startCol,
		}
	}
}

func (l *lexer) consumeHexDigits(n, startLine, startCol int) *Diag {
	for i := 0; i < n; i++ {
		r, sz := l.peek()
		if !isHexDigit(r) {
			return &Diag{
				Code:    "E0101",
				Message: fmt.Sprintf("Escape sequence expects %d hex digits", n),
				Line:    startLine,
				Col:     startCol,
			}
		}
		l.advance(sz)
	}
	return nil
}

func isLetter(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_'
}
func isDigit(r rune) bool    { return r >= '0' && r <= '9' }
func isHexDigit(r rune) bool { return isDigit(r) || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F') }
func isOctDigit(r rune) bool { return r >= '0' && r <= '7' }
func isBinDigit(r rune) bool { return r == '0' || r == '1' }
