package parser

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/heni/tide-lang/internal/ast"
	"github.com/heni/tide-lang/internal/lexer"
)

// Diag is a parser-level diagnostic with the same shape as
// lexer.Diag (canonical file:line:col format).
type Diag struct {
	File    string
	Code    string
	Message string
	Line    int
	Col     int
}

func (d *Diag) Error() string {
	if d.File == "" {
		return fmt.Sprintf("%d:%d: error[%s]: %s", d.Line, d.Col, d.Code, d.Message)
	}
	return fmt.Sprintf("%s:%d:%d: error[%s]: %s", d.File, d.Line, d.Col, d.Code, d.Message)
}

// Parse takes a token stream (from lexer.Lex) and returns a *File.
// The first diagnostic encountered halts parsing.
func Parse(toks []lexer.Token) (*ast.File, *Diag) {
	return ParseFile(toks, "")
}

// ParseFile is Parse but tags diagnostics with the source filename.
func ParseFile(toks []lexer.Token, file string) (*ast.File, *Diag) {
	p := &parser{toks: toks, file: file}
	return p.parseFile()
}

type parser struct {
	toks []lexer.Token
	pos  int
	file string
	// noBrace suppresses brace-literal parsing of a trailing `{` so
	// control-flow headers (`if cond {`, `for x in it {`, `while
	// cond {`, `match subj {`) read the `{` as their block rather
	// than as `cond { … }` brace literal. Set while parsing a header
	// expression; reset inside any delimited sub-expression where a
	// `{` is unambiguous (parens, call args, brackets, brace body).
	noBrace bool
}

// withNoBrace runs fn (a header-expression parse) with brace-literal
// suppression on, restoring noBrace after — so a trailing `{` reads as
// the control-flow block, not a `cond { … }` brace literal. Delimited
// contexts (parens, call args, brackets, brace bodies) instead use the
// inline `defer` form to re-enable braces.
func (p *parser) withNoBrace(fn func() (ast.Expr, *Diag)) (ast.Expr, *Diag) {
	saved := p.noBrace
	p.noBrace = true
	e, err := fn()
	p.noBrace = saved
	return e, err
}

// ---- token cursor helpers ----

func (p *parser) peek() lexer.Token {
	if p.pos >= len(p.toks) {
		// Defensive: lexer always appends EOF so this shouldn't
		// happen, but emit a synthetic EOF if it does.
		return lexer.Token{Kind: lexer.KindEOF}
	}
	return p.toks[p.pos]
}

// peekAhead returns the token n positions past the cursor (n == 0 is
// peek()), or a synthetic EOF past the end.
func (p *parser) peekAhead(n int) lexer.Token {
	if p.pos+n >= len(p.toks) {
		return lexer.Token{Kind: lexer.KindEOF}
	}
	return p.toks[p.pos+n]
}

func (p *parser) at(k lexer.Kind, lex ...string) bool {
	t := p.peek()
	if t.Kind != k {
		return false
	}
	if len(lex) == 0 {
		return true
	}
	for _, want := range lex {
		if t.Lexeme == want {
			return true
		}
	}
	return false
}

// skipNewlines consumes runs of Newline tokens. Newlines are
// statement separators, but several positions don't care about
// them (after an open brace, before a closing one, between
// tokens of a single expression continued on the next line via
// open brackets, …). PR-B's parser is lenient: newlines are
// skipped at most positions, treated as a separator only between
// statements inside a Block.
func (p *parser) skipNewlines() {
	for p.at(lexer.KindNewline) {
		p.pos++
	}
}

// skipStmtSeps consumes statement terminators between block
// statements — newlines and `;` in any interleaving, per grammar.ebnf
// Stmtterm = Newline+ | ";" Newline*.
func (p *parser) skipStmtSeps() {
	for p.at(lexer.KindNewline) || p.at(lexer.KindPunct, ";") {
		p.pos++
	}
}

func (p *parser) advance() lexer.Token {
	t := p.peek()
	p.pos++
	return t
}

// expect consumes a token of kind k (and matching lexeme, if given)
// or returns a diagnostic.
func (p *parser) expect(k lexer.Kind, lex string) (lexer.Token, *Diag) {
	t := p.peek()
	if t.Kind != k || (lex != "" && t.Lexeme != lex) {
		return t, p.diag("E0112", fmt.Sprintf("expected %s %q, got %s %q",
			k, lex, t.Kind, t.Lexeme), t.Line, t.Col)
	}
	p.pos++
	return t, nil
}

func (p *parser) diag(code, msg string, line, col int) *Diag {
	return &Diag{File: p.file, Code: code, Message: msg, Line: line, Col: col}
}

// ---- file ----

func (p *parser) parseFile() (*ast.File, *Diag) {
	startLine, startCol := 1, 1
	f := &ast.File{}

	p.skipNewlines()
	// Imports first.
	for p.at(lexer.KindKeyword, "import") {
		im, err := p.parseImport()
		if err != nil {
			return nil, err
		}
		f.Imports = append(f.Imports, im)
		p.skipNewlines()
	}
	// Then declarations.
	for !p.at(lexer.KindEOF) {
		d, err := p.parseDecl()
		if err != nil {
			return nil, err
		}
		f.Decls = append(f.Decls, d)
		p.skipNewlines()
	}
	if len(f.Decls) == 0 {
		return nil, p.diag("E0112", "File has no declarations", 1, 1)
	}
	eof := p.peek()
	f.Span = ast.Span{
		StartLine: startLine, StartCol: startCol,
		EndLine: eof.Line, EndCol: eof.Col,
	}
	return f, nil
}

func (p *parser) parseImport() (*ast.Import, *Diag) {
	kw := p.advance() // consume 'import'
	// Path is one-or-more identifiers separated by `/` per
	// grammar.ebnf PackagePath = Ident ("/" Ident)*. Dots are not
	// admitted (member access on a package is the field operator
	// `.`, not part of the import path).
	var parts []string
	end := kw
	if !p.at(lexer.KindIdent) {
		t := p.peek()
		return nil, p.diag("E0112", "expected identifier after `import`", t.Line, t.Col)
	}
	t := p.advance()
	parts = append(parts, t.Lexeme)
	end = t
	for p.at(lexer.KindOp, "/") {
		sep := p.advance()
		parts = append(parts, sep.Lexeme)
		if !p.at(lexer.KindIdent) {
			t = p.peek()
			return nil, p.diag("E0112", "expected identifier in import path", t.Line, t.Col)
		}
		next := p.advance()
		parts = append(parts, next.Lexeme)
		end = next
	}
	return &ast.Import{
		Span: ast.Span{
			StartLine: kw.Line, StartCol: kw.Col,
			EndLine: end.Line, EndCol: end.Col + utf8.RuneCountInString(end.Lexeme),
		},
		Path: strings.Join(parts, ""),
	}, nil
}

func (p *parser) parseDecl() (ast.Decl, *Diag) {
	if p.at(lexer.KindKeyword, "func") {
		return p.parseFuncDecl()
	}
	if p.at(lexer.KindKeyword, "type") {
		return p.parseTypeDecl()
	}
	if p.at(lexer.KindKeyword, "class") {
		return p.parseClassDecl()
	}
	t := p.peek()
	return nil, p.diag("E0112",
		fmt.Sprintf("expected top-level declaration, got %s %q", t.Kind, t.Lexeme),
		t.Line, t.Col)
}

// parseTypeDecl parses `type Name = TypeBody`. PR-F2 supports
// SumTypeBody (nullary variants) and AliasBody. RecordTypeBody
// and TupleAliasBody land with later PRs.
func (p *parser) parseTypeDecl() (*ast.TypeDecl, *Diag) {
	kw := p.advance() // consume 'type'
	nameTok, err := p.expect(lexer.KindIdent, "")
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.KindOp, "="); err != nil {
		return nil, err
	}
	p.skipNewlines()
	var body ast.TypeBody
	// SumTypeBody iff the body starts with `|`; RecordTypeBody iff
	// it starts with `{`.
	if p.at(lexer.KindOp, "|") {
		sb, err := p.parseSumTypeBody()
		if err != nil {
			return nil, err
		}
		body = sb
	} else if p.at(lexer.KindPunct, "{") {
		rb, err := p.parseRecordTypeBody()
		if err != nil {
			return nil, err
		}
		body = rb
	} else {
		// AliasBody — single TypeExpr.
		startLine, startCol := p.peek().Line, p.peek().Col
		ty, err := p.parseTypeExpr()
		if err != nil {
			return nil, err
		}
		body = &ast.AliasBody{
			Span: ast.Span{
				StartLine: startLine, StartCol: startCol,
				EndLine: ty.NodeSpan().EndLine, EndCol: ty.NodeSpan().EndCol,
			},
			Aliased: ty,
		}
	}
	return &ast.TypeDecl{
		Span: ast.Span{
			StartLine: kw.Line, StartCol: kw.Col,
			EndLine: body.NodeSpan().EndLine, EndCol: body.NodeSpan().EndCol,
		},
		Name: nameTok.Lexeme,
		Body: body,
	}, nil
}

// parseSumTypeBody expects the cursor at the leading `|`.
func (p *parser) parseSumTypeBody() (*ast.SumTypeBody, *Diag) {
	startTok := p.peek()
	var variants []*ast.Variant
	for p.at(lexer.KindOp, "|") {
		p.advance() // consume '|'
		p.skipNewlines()
		if !p.at(lexer.KindIdent) {
			t := p.peek()
			return nil, p.diag("E0112", "expected variant name after `|`", t.Line, t.Col)
		}
		vnTok := p.advance()
		v := &ast.Variant{
			Span: ast.Span{
				StartLine: vnTok.Line, StartCol: vnTok.Col,
				EndLine: vnTok.Line, EndCol: vnTok.Col + utf8.RuneCountInString(vnTok.Lexeme),
			},
			Name: vnTok.Lexeme,
		}
		// Optional payload: `(name: T, name: T, ...)`.
		if p.at(lexer.KindPunct, "(") {
			p.advance() // consume '('
			p.skipNewlines()
			for !p.at(lexer.KindPunct, ")") {
				if !p.at(lexer.KindIdent) {
					t := p.peek()
					return nil, p.diag("E0112", "expected field name in variant payload", t.Line, t.Col)
				}
				fnTok := p.advance()
				if _, err := p.expect(lexer.KindPunct, ":"); err != nil {
					return nil, err
				}
				ft, err := p.parseTypeExpr()
				if err != nil {
					return nil, err
				}
				v.Fields = append(v.Fields, &ast.FieldDecl{
					Span: ast.Span{
						StartLine: fnTok.Line, StartCol: fnTok.Col,
						EndLine: ft.NodeSpan().EndLine, EndCol: ft.NodeSpan().EndCol,
					},
					Name:     fnTok.Lexeme,
					DeclType: ft,
				})
				p.skipNewlines()
				if !p.at(lexer.KindPunct, ",") {
					break
				}
				p.advance() // consume ','
				p.skipNewlines()
			}
			closeTok, err := p.expect(lexer.KindPunct, ")")
			if err != nil {
				return nil, err
			}
			v.Span.EndLine = closeTok.Line
			v.Span.EndCol = closeTok.Col + 1
		}
		variants = append(variants, v)
		p.skipNewlines()
	}
	if len(variants) < 2 {
		// ast.md:111 — SumTypeBody requires variants.len() >= 2.
		// A single-variant "sum" should be a class or a struct.
		return nil, p.diag("E0112", "sum type must have at least two variants", startTok.Line, startTok.Col)
	}
	last := variants[len(variants)-1]
	return &ast.SumTypeBody{
		Span: ast.Span{
			StartLine: startTok.Line, StartCol: startTok.Col,
			EndLine: last.Span.EndLine, EndCol: last.Span.EndCol,
		},
		Variants: variants,
	}, nil
}

// parseRecordTypeBody parses `{ f1: T1, f2: T2, ... }` (grammar.ebnf
// RecordType). Fields are separated by commas and/or newlines; a
// trailing separator is allowed. Cursor at the opening `{`.
func (p *parser) parseRecordTypeBody() (*ast.RecordTypeBody, *Diag) {
	open, err := p.expect(lexer.KindPunct, "{")
	if err != nil {
		return nil, err
	}
	p.skipStmtSeps()
	var fields []*ast.FieldDecl
	for !p.at(lexer.KindPunct, "}") && !p.at(lexer.KindEOF) {
		if !p.at(lexer.KindIdent) {
			t := p.peek()
			return nil, p.diag("E0112", "expected record field name", t.Line, t.Col)
		}
		nameTok := p.advance()
		if _, err := p.expect(lexer.KindPunct, ":"); err != nil {
			return nil, err
		}
		ft, err := p.parseTypeExpr()
		if err != nil {
			return nil, err
		}
		fields = append(fields, &ast.FieldDecl{
			Span: ast.Span{
				StartLine: nameTok.Line, StartCol: nameTok.Col,
				EndLine: ft.NodeSpan().EndLine, EndCol: ft.NodeSpan().EndCol,
			},
			Name:     nameTok.Lexeme,
			DeclType: ft,
		})
		// Field separator: a comma and/or newlines, or the closing `}`.
		if p.at(lexer.KindPunct, ",") {
			p.advance()
		}
		p.skipStmtSeps()
	}
	closeTok, err := p.expect(lexer.KindPunct, "}")
	if err != nil {
		return nil, err
	}
	if len(fields) == 0 {
		return nil, p.diag("E0112", "record type needs at least one field", open.Line, open.Col)
	}
	return &ast.RecordTypeBody{
		Span: ast.Span{
			StartLine: open.Line, StartCol: open.Col,
			EndLine: closeTok.Line, EndCol: closeTok.Col + 1,
		},
		Fields: fields,
	}, nil
}

// parseClassDecl parses `class Name<TypeParams> { fields, methods }`.
// Type parameters arrived with PR-G1 (the `<T, U>` list after the
// name is admitted; bare names only, constraints come with PR-G3).
// `implements` is still rejected at parse time and lands with the
// interface PR. A class member is either `let|var name: T` (field)
// or `[static] name<TypeParams>?(params)? body` (method); the
// parser commits based on the leading keyword.
func (p *parser) parseClassDecl() (*ast.ClassDecl, *Diag) {
	kw := p.advance() // consume 'class'
	nameTok, err := p.expect(lexer.KindIdent, "")
	if err != nil {
		return nil, err
	}
	typeParams, err := p.parseTypeParamList()
	if err != nil {
		return nil, err
	}
	if p.at(lexer.KindKeyword, "implements") {
		t := p.peek()
		return nil, p.diag("E0112", "`implements` on class declarations is not yet supported", t.Line, t.Col)
	}
	if _, err := p.expect(lexer.KindPunct, "{"); err != nil {
		return nil, err
	}
	p.skipNewlines()
	var fields []*ast.ClassField
	var methods []*ast.Method
	for !p.at(lexer.KindPunct, "}") {
		if p.at(lexer.KindKeyword, "let") || p.at(lexer.KindKeyword, "var") {
			f, err := p.parseClassField()
			if err != nil {
				return nil, err
			}
			fields = append(fields, f)
		} else {
			m, err := p.parseMethod()
			if err != nil {
				return nil, err
			}
			methods = append(methods, m)
		}
		p.skipNewlines()
	}
	closeTok := p.advance() // consume '}'
	return &ast.ClassDecl{
		Span: ast.Span{
			StartLine: kw.Line, StartCol: kw.Col,
			EndLine: closeTok.Line, EndCol: closeTok.Col + 1,
		},
		Name:       nameTok.Lexeme,
		TypeParams: typeParams,
		Fields:     fields,
		Methods:    methods,
	}, nil
}

func (p *parser) parseClassField() (*ast.ClassField, *Diag) {
	kw := p.advance() // 'let' or 'var'
	mut := "Let"
	if kw.Lexeme == "var" {
		mut = "Var"
	}
	nameTok, err := p.expect(lexer.KindIdent, "")
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.KindPunct, ":"); err != nil {
		return nil, err
	}
	ty, err := p.parseTypeExpr()
	if err != nil {
		return nil, err
	}
	return &ast.ClassField{
		Span: ast.Span{
			StartLine: kw.Line, StartCol: kw.Col,
			EndLine: ty.NodeSpan().EndLine, EndCol: ty.NodeSpan().EndCol,
		},
		Name:       nameTok.Lexeme,
		DeclType:   ty,
		Mutability: mut,
	}, nil
}

func (p *parser) parseMethod() (*ast.Method, *Diag) {
	startTok := p.peek()
	isStatic := false
	if p.at(lexer.KindKeyword, "static") {
		p.advance()
		isStatic = true
	}
	nameTok, err := p.expect(lexer.KindIdent, "")
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.KindPunct, "("); err != nil {
		return nil, err
	}
	params, err := p.parseParamList()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.KindPunct, ")"); err != nil {
		return nil, err
	}
	var retType ast.TypeExpr
	if p.at(lexer.KindPunct, ":") {
		p.advance()
		retType, err = p.parseTypeExpr()
		if err != nil {
			return nil, err
		}
	}
	body, err := p.parseBlock()
	if err != nil {
		return nil, err
	}
	return &ast.Method{
		Span: ast.Span{
			StartLine: startTok.Line, StartCol: startTok.Col,
			EndLine: body.Span.EndLine, EndCol: body.Span.EndCol,
		},
		Name:       nameTok.Lexeme,
		IsStatic:   isStatic,
		Params:     params,
		ReturnType: retType,
		Body:       body,
	}, nil
}

func (p *parser) parseFuncDecl() (*ast.FuncDecl, *Diag) {
	kw := p.advance() // consume 'func'
	name, err := p.expect(lexer.KindIdent, "")
	if err != nil {
		return nil, err
	}
	typeParams, err := p.parseTypeParamList()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.KindPunct, "("); err != nil {
		return nil, err
	}
	params, err := p.parseParamList()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.KindPunct, ")"); err != nil {
		return nil, err
	}
	var retType ast.TypeExpr
	if p.at(lexer.KindPunct, ":") {
		p.advance() // consume ':'
		retType, err = p.parseTypeExpr()
		if err != nil {
			return nil, err
		}
	}
	body, err := p.parseBlock()
	if err != nil {
		return nil, err
	}
	return &ast.FuncDecl{
		Span: ast.Span{
			StartLine: kw.Line, StartCol: kw.Col,
			EndLine: body.Span.EndLine, EndCol: body.Span.EndCol,
		},
		Name:       name.Lexeme,
		TypeParams: typeParams,
		Params:     params,
		ReturnType: retType,
		Body:       body,
	}, nil
}

// parseTypeParamList reads an optional `<T, U, ...>` list at
// the declaration head. v1 admits only bare names (default
// constraint `any`); user-written constraints land in PR-G3.
func (p *parser) parseTypeParamList() ([]string, *Diag) {
	if !p.at(lexer.KindOp, "<") {
		return nil, nil
	}
	p.advance() // consume '<'
	var out []string
	for {
		p.skipNewlines()
		if !p.at(lexer.KindIdent) {
			t := p.peek()
			return nil, p.diag("E0112",
				fmt.Sprintf("expected type-parameter name, got %s %q", t.Kind, t.Lexeme),
				t.Line, t.Col)
		}
		tp := p.advance()
		out = append(out, tp.Lexeme)
		if p.at(lexer.KindPunct, ":") {
			t := p.peek()
			return nil, p.diag("E0112",
				"type-parameter constraints (`<T: SomeInterface>`) land in PR-G3 — drop the constraint and let `any` default apply",
				t.Line, t.Col)
		}
		if p.at(lexer.KindPunct, ",") {
			p.advance()
			continue
		}
		break
	}
	if _, err := p.expect(lexer.KindOp, ">"); err != nil {
		return nil, err
	}
	return out, nil
}

// parseParamList reads zero or more comma-separated `name: T`
// parameters up to (but not consuming) the closing ')'.
func (p *parser) parseParamList() ([]*ast.Param, *Diag) {
	var out []*ast.Param
	p.skipNewlines()
	if p.at(lexer.KindPunct, ")") {
		return nil, nil
	}
	for {
		p.skipNewlines()
		if !p.at(lexer.KindIdent) {
			t := p.peek()
			return nil, p.diag("E0112", "expected parameter name", t.Line, t.Col)
		}
		nameTok := p.advance()
		if _, err := p.expect(lexer.KindPunct, ":"); err != nil {
			return nil, err
		}
		ty, err := p.parseTypeExpr()
		if err != nil {
			return nil, err
		}
		out = append(out, &ast.Param{
			Span: ast.Span{
				StartLine: nameTok.Line, StartCol: nameTok.Col,
				EndLine: ty.NodeSpan().EndLine, EndCol: ty.NodeSpan().EndCol,
			},
			Name:     nameTok.Lexeme,
			DeclType: ty,
		})
		p.skipNewlines()
		if !p.at(lexer.KindPunct, ",") {
			break
		}
		p.advance() // consume ','
	}
	return out, nil
}

// parseTypeExpr emits PrimitiveType for the closed PrimitiveName
// set, SliceType for `[]T`, and NamedType for everything else.
// Form:
//
//	TypeExpr = PrimitiveType
//	         | NamedType
//	NamedType = Ident ("." Ident)*  ("<" TypeArgList ">")?
//
// Generic args are parsed if present (so `Result<int, error>` and
// `Map<string, int>` parse cleanly), even though the only
// type-bearing positions in PR-F1's corpus use bare primitives.
func (p *parser) parseTypeExpr() (ast.TypeExpr, *Diag) {
	// FuncType: `func(A, B) : R`.
	if p.at(lexer.KindKeyword, "func") {
		kw := p.advance() // consume 'func'
		if _, err := p.expect(lexer.KindPunct, "("); err != nil {
			return nil, err
		}
		var params []ast.TypeExpr
		p.skipNewlines()
		for !p.at(lexer.KindPunct, ")") && !p.at(lexer.KindEOF) {
			pt, err := p.parseTypeExpr()
			if err != nil {
				return nil, err
			}
			params = append(params, pt)
			p.skipNewlines()
			if !p.at(lexer.KindPunct, ",") {
				break
			}
			p.advance()
			p.skipNewlines()
		}
		closeTok, err := p.expect(lexer.KindPunct, ")")
		if err != nil {
			return nil, err
		}
		ft := &ast.FuncType{
			Span: ast.Span{StartLine: kw.Line, StartCol: kw.Col,
				EndLine: closeTok.Line, EndCol: closeTok.Col + 1},
			Params: params,
		}
		if p.at(lexer.KindPunct, ":") {
			p.advance()
			rt, err := p.parseTypeExpr()
			if err != nil {
				return nil, err
			}
			ft.ReturnType = rt
			ft.Span.EndLine, ft.Span.EndCol = rt.NodeSpan().EndLine, rt.NodeSpan().EndCol
		}
		return ft, nil
	}
	// TupleType: `(A, B, ...)` — arity ≥ 2.
	if p.at(lexer.KindPunct, "(") {
		open := p.advance() // consume '('
		p.skipNewlines()
		var comps []ast.TypeExpr
		for !p.at(lexer.KindPunct, ")") && !p.at(lexer.KindEOF) {
			c, err := p.parseTypeExpr()
			if err != nil {
				return nil, err
			}
			comps = append(comps, c)
			p.skipNewlines()
			if !p.at(lexer.KindPunct, ",") {
				break
			}
			p.advance() // consume ','
			p.skipNewlines()
		}
		closeTok, err := p.expect(lexer.KindPunct, ")")
		if err != nil {
			return nil, err
		}
		if len(comps) < 2 {
			return nil, p.diag("E0112", "tuple type needs at least two components", open.Line, open.Col)
		}
		return &ast.TupleType{
			Span: ast.Span{
				StartLine: open.Line, StartCol: open.Col,
				EndLine: closeTok.Line, EndCol: closeTok.Col + 1,
			},
			Components: comps,
		}, nil
	}
	// SliceType: `[]T`.
	if p.at(lexer.KindPunct, "[") {
		openTok := p.advance() // consume '['
		if _, err := p.expect(lexer.KindPunct, "]"); err != nil {
			return nil, err
		}
		elem, err := p.parseTypeExpr()
		if err != nil {
			return nil, err
		}
		return &ast.SliceType{
			Span: ast.Span{
				StartLine: openTok.Line, StartCol: openTok.Col,
				EndLine: elem.NodeSpan().EndLine, EndCol: elem.NodeSpan().EndCol,
			},
			Elem: elem,
		}, nil
	}
	if !p.at(lexer.KindIdent) {
		t := p.peek()
		return nil, p.diag("E0112",
			fmt.Sprintf("expected type expression, got %s %q", t.Kind, t.Lexeme),
			t.Line, t.Col)
	}
	first := p.advance()
	startLine, startCol := first.Line, first.Col
	endLine, endCol := first.Line, first.Col+utf8.RuneCountInString(first.Lexeme)

	// Commit to PrimitiveType when the first segment is a member
	// of the closed primitive-name set AND there is no further
	// qualification (`.`) or type-arg list. `int.Foo` and
	// `Result<int>` continue to flow through the NamedType path.
	isPrim := ast.PrimitiveNames[first.Lexeme]
	if isPrim && !p.at(lexer.KindPunct, ".") && !p.at(lexer.KindOp, "<") {
		return &ast.PrimitiveType{
			Span: ast.Span{
				StartLine: startLine, StartCol: startCol,
				EndLine: endLine, EndCol: endCol,
			},
			Name: first.Lexeme,
		}, nil
	}

	qname := []string{first.Lexeme}
	for p.at(lexer.KindPunct, ".") {
		p.advance() // consume '.'
		if !p.at(lexer.KindIdent) {
			t := p.peek()
			return nil, p.diag("E0112", "expected identifier after `.`", t.Line, t.Col)
		}
		next := p.advance()
		qname = append(qname, next.Lexeme)
		endLine, endCol = next.Line, next.Col+utf8.RuneCountInString(next.Lexeme)
	}
	var args []ast.TypeExpr
	if p.at(lexer.KindOp, "<") {
		p.advance() // consume '<'
		for {
			p.skipNewlines()
			arg, err := p.parseTypeExpr()
			if err != nil {
				return nil, err
			}
			args = append(args, arg)
			p.skipNewlines()
			if !p.at(lexer.KindPunct, ",") {
				break
			}
			p.advance() // consume ','
		}
		closeTok, err := p.expect(lexer.KindOp, ">")
		if err != nil {
			return nil, err
		}
		endLine, endCol = closeTok.Line, closeTok.Col+1
	}
	return &ast.NamedType{
		Span: ast.Span{
			StartLine: startLine, StartCol: startCol,
			EndLine: endLine, EndCol: endCol,
		},
		QName: qname,
		Args:  args,
	}, nil
}

// ---- block & statements ----

func (p *parser) parseBlock() (*ast.Block, *Diag) {
	open, err := p.expect(lexer.KindPunct, "{")
	if err != nil {
		return nil, err
	}
	blk := &ast.Block{}
	p.skipStmtSeps()
	for !p.at(lexer.KindPunct, "}") && !p.at(lexer.KindEOF) {
		s, err := p.parseStmt()
		if err != nil {
			return nil, err
		}
		blk.Stmts = append(blk.Stmts, s)
		p.skipStmtSeps()
	}
	closeTok, err := p.expect(lexer.KindPunct, "}")
	if err != nil {
		return nil, err
	}
	blk.Span = ast.Span{
		StartLine: open.Line, StartCol: open.Col,
		EndLine: closeTok.Line, EndCol: closeTok.Col + 1,
	}
	return blk, nil
}

// parseValueBlock parses a `{ ... }` block used in expression
// position (block-as-expression, `if`/`match`-arm bodies). It is
// `parseBlock` plus trailing-expression promotion: per grammar.ebnf
// `Block = "{" ( Stmt Stmtterm )* TrailingExpr? "}"`, a final bare
// expression (one not consumed as a statement-only form) becomes the
// block's value. Diverging trailing forms (`return`) stay statements
// — they have no value to yield.
func (p *parser) parseValueBlock() (*ast.Block, *Diag) {
	blk, err := p.parseBlock()
	if err != nil {
		return nil, err
	}
	if n := len(blk.Stmts); n > 0 {
		if es, ok := blk.Stmts[n-1].(*ast.ExprStmt); ok {
			if _, diverges := es.Expr.(*ast.ReturnExpr); !diverges {
				blk.Trailing = es.Expr
				blk.Stmts = blk.Stmts[:n-1]
			}
		}
	}
	return blk, nil
}

// parseIfExpr parses `if Cond Block ( "else" ( IfExpr | Block ) )?`
// in expression position. The `else` is syntactically optional here
// (a value-position `if` without `else` yields unit). The
// both-arms-required rule for a value `if` (grammar.ebnf IfExpr) is
// not yet enforced in sema — codegen rejects an else-less branch used
// as a value; the proper Barrier-D diagnostic is a follow-up. Branch
// blocks are value-blocks so their trailing expression becomes the
// branch value.
func (p *parser) parseIfExpr() (*ast.IfExpr, *Diag) {
	kw := p.advance() // consume 'if'
	cond, err := p.withNoBrace(p.parseExpr)
	if err != nil {
		return nil, err
	}
	then, err := p.parseValueBlock()
	if err != nil {
		return nil, err
	}
	ie := &ast.IfExpr{
		Span: ast.Span{
			StartLine: kw.Line, StartCol: kw.Col,
			EndLine: then.Span.EndLine, EndCol: then.Span.EndCol,
		},
		Cond:      cond,
		ThenBlock: then,
	}
	if p.at(lexer.KindKeyword, "else") {
		p.advance() // consume 'else'
		if p.at(lexer.KindKeyword, "if") {
			elseIf, err := p.parseIfExpr()
			if err != nil {
				return nil, err
			}
			ie.Else = elseIf
			ie.Span.EndLine, ie.Span.EndCol = elseIf.Span.EndLine, elseIf.Span.EndCol
		} else {
			elseBlk, err := p.parseValueBlock()
			if err != nil {
				return nil, err
			}
			ie.Else = elseBlk
			ie.Span.EndLine, ie.Span.EndCol = elseBlk.Span.EndLine, elseBlk.Span.EndCol
		}
	}
	return ie, nil
}

func (p *parser) parseStmt() (ast.Stmt, *Diag) {
	switch {
	case p.at(lexer.KindKeyword, "if"):
		return p.parseIfStmt()
	case p.at(lexer.KindKeyword, "for"):
		return p.parseForStmt()
	case p.at(lexer.KindKeyword, "while"):
		return p.parseWhileStmt()
	case p.at(lexer.KindKeyword, "let"):
		return p.parseLetOrVar(true)
	case p.at(lexer.KindKeyword, "const"):
		// `const` is a surface alias for `let` — both produce
		// an immutable binding. The keyword is kept distinct in
		// the lexer for readability of declarations the user
		// intends as named constants; downstream nodes don't
		// distinguish them.
		return p.parseLetOrVar(true)
	case p.at(lexer.KindKeyword, "var"):
		return p.parseLetOrVar(false)
	case p.at(lexer.KindKeyword, "return"):
		// `return` is a DivergingExpr (ast.md §Expr); when it
		// appears at statement position we wrap it in an
		// ExprStmt rather than introducing a separate ReturnStmt.
		re, err := p.parseReturnExpr()
		if err != nil {
			return nil, err
		}
		return &ast.ExprStmt{Span: re.Span, Expr: re}, nil
	default:
		// Expression statement OR assignment.
		e, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		// `lvalue = value` — distinguishing from `==` is a
		// non-issue because `=` is its own Op token (the lexer
		// emits `==` as a single token; bare `=` only appears
		// in assignment position).
		if p.at(lexer.KindOp, "=") {
			eqTok := p.advance()
			rhs, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			_ = eqTok
			return &ast.AssignStmt{
				Span: ast.Span{
					StartLine: e.NodeSpan().StartLine, StartCol: e.NodeSpan().StartCol,
					EndLine: rhs.NodeSpan().EndLine, EndCol: rhs.NodeSpan().EndCol,
				},
				LValue: e,
				Value:  rhs,
			}, nil
		}
		// Compound assignment: `lhs += rhs` desugars to
		// `lhs = lhs + rhs` at the AST level. The LValue is
		// reused on both sides — this is correct as long as
		// the LValue has no side-effecting subexpression
		// (a plain identifier or field/index path); sema will
		// tighten this once it lands.
		for _, op := range []string{"+=", "-=", "*=", "/=", "%="} {
			if p.at(lexer.KindOp, op) {
				opTok := p.advance()
				rhs, err := p.parseExpr()
				if err != nil {
					return nil, err
				}
				binOp := op[:1]
				return &ast.AssignStmt{
					Span: ast.Span{
						StartLine: e.NodeSpan().StartLine, StartCol: e.NodeSpan().StartCol,
						EndLine: rhs.NodeSpan().EndLine, EndCol: rhs.NodeSpan().EndCol,
					},
					LValue: e,
					Value: &ast.Binary{
						Span: ast.Span{
							StartLine: opTok.Line, StartCol: opTok.Col,
							EndLine: rhs.NodeSpan().EndLine, EndCol: rhs.NodeSpan().EndCol,
						},
						Op:    binOp,
						Left:  e,
						Right: rhs,
					},
				}, nil
			}
		}
		return &ast.ExprStmt{Span: e.NodeSpan(), Expr: e}, nil
	}
}

func (p *parser) parseLetOrVar(isLet bool) (ast.Stmt, *Diag) {
	kw := p.advance() // consume 'let' or 'var'
	if !p.at(lexer.KindIdent) {
		t := p.peek()
		return nil, p.diag("E0112", "expected binding name", t.Line, t.Col)
	}
	nameTok := p.advance()
	var declType ast.TypeExpr
	if p.at(lexer.KindPunct, ":") {
		p.advance() // consume ':'
		var err *Diag
		declType, err = p.parseTypeExpr()
		if err != nil {
			return nil, err
		}
	}
	// `var x: T` admitted without initialiser — sema will reject
	// per G1, but the AST schema (ast.md:222) makes Value
	// optional. `let` must always have an initialiser.
	var value ast.Expr
	if isLet || p.at(lexer.KindOp, "=") {
		if _, err := p.expect(lexer.KindOp, "="); err != nil {
			return nil, err
		}
		v, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		value = v
	}
	endLine, endCol := nameTok.Line, nameTok.Col+utf8.RuneCountInString(nameTok.Lexeme)
	if declType != nil {
		endLine, endCol = declType.NodeSpan().EndLine, declType.NodeSpan().EndCol
	}
	if value != nil {
		endLine, endCol = value.NodeSpan().EndLine, value.NodeSpan().EndCol
	}
	span := ast.Span{
		StartLine: kw.Line, StartCol: kw.Col,
		EndLine: endLine, EndCol: endCol,
	}
	if isLet {
		// LetStmt.Pattern — for PR-F1 always IdentPat; tuple /
		// variant / record destructuring patterns land later.
		pat := &ast.IdentPat{
			Span: ast.Span{
				StartLine: nameTok.Line, StartCol: nameTok.Col,
				EndLine: nameTok.Line, EndCol: nameTok.Col + utf8.RuneCountInString(nameTok.Lexeme),
			},
			Name: nameTok.Lexeme,
		}
		return &ast.LetStmt{
			Span: span, Pattern: pat, DeclType: declType, Value: value,
		}, nil
	}
	return &ast.VarStmt{
		Span: span, Name: nameTok.Lexeme, DeclType: declType, Value: value,
	}, nil
}

// parseReturnExpr parses `return` or `return <expr>` and returns
// it as a ReturnExpr (DivergingExpr per ast.md). Callers at
// statement position wrap it in an ExprStmt.
func (p *parser) parseReturnExpr() (*ast.ReturnExpr, *Diag) {
	kw := p.advance() // consume 'return'
	// Bare `return` ends at end-of-statement (newline, `}`, EOF).
	if p.at(lexer.KindNewline) || p.at(lexer.KindPunct, "}") || p.at(lexer.KindEOF) {
		return &ast.ReturnExpr{
			Span: ast.Span{
				StartLine: kw.Line, StartCol: kw.Col,
				EndLine: kw.Line, EndCol: kw.Col + utf8.RuneCountInString("return"),
			},
		}, nil
	}
	value, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	return &ast.ReturnExpr{
		Span: ast.Span{
			StartLine: kw.Line, StartCol: kw.Col,
			EndLine: value.NodeSpan().EndLine, EndCol: value.NodeSpan().EndCol,
		},
		Value: value,
	}, nil
}

func (p *parser) parseIfStmt() (*ast.IfStmt, *Diag) {
	kw := p.advance() // consume 'if'
	cond, err := p.withNoBrace(p.parseExpr)
	if err != nil {
		return nil, err
	}
	then, err := p.parseBlock()
	if err != nil {
		return nil, err
	}
	stmt := &ast.IfStmt{
		Span: ast.Span{
			StartLine: kw.Line, StartCol: kw.Col,
			EndLine: then.Span.EndLine, EndCol: then.Span.EndCol,
		},
		Cond:      cond,
		ThenBlock: then,
	}
	if p.at(lexer.KindKeyword, "else") {
		p.advance() // consume 'else'
		if p.at(lexer.KindKeyword, "if") {
			elseIf, err := p.parseIfStmt()
			if err != nil {
				return nil, err
			}
			stmt.Else = elseIf
			stmt.Span.EndLine = elseIf.Span.EndLine
			stmt.Span.EndCol = elseIf.Span.EndCol
		} else {
			elseBlk, err := p.parseBlock()
			if err != nil {
				return nil, err
			}
			stmt.Else = elseBlk
			stmt.Span.EndLine = elseBlk.Span.EndLine
			stmt.Span.EndCol = elseBlk.Span.EndCol
		}
	}
	return stmt, nil
}

func (p *parser) parseForStmt() (*ast.ForStmt, *Diag) {
	kw := p.advance() // consume 'for'
	// Loop variable is a pattern: a bare name, or a tuple pattern
	// `for (k, v) in m` for key/value (or paired) iteration.
	if !p.at(lexer.KindIdent) && !p.at(lexer.KindPunct, "(") {
		t := p.peek()
		return nil, p.diag("E0112", "expected loop variable name or tuple pattern", t.Line, t.Col)
	}
	pat, err := p.parsePattern()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.KindKeyword, "in"); err != nil {
		return nil, err
	}
	savedNB := p.noBrace
	p.noBrace = true
	iter, err := p.parseIterable()
	p.noBrace = savedNB
	if err != nil {
		return nil, err
	}
	body, err := p.parseBlock()
	if err != nil {
		return nil, err
	}
	return &ast.ForStmt{
		Span: ast.Span{
			StartLine: kw.Line, StartCol: kw.Col,
			EndLine: body.Span.EndLine, EndCol: body.Span.EndCol,
		},
		Pattern:  pat,
		Iterable: iter,
		Body:     body,
	}, nil
}

// parseWhileStmt parses `while Cond Block` (grammar.ebnf WhileStmt).
// The condition is a plain expression; like `if`/`for` headers it
// stops at the body's `{` (a bare `{` is never an expression
// continuation).
func (p *parser) parseWhileStmt() (*ast.WhileStmt, *Diag) {
	kw := p.advance() // consume 'while'
	cond, err := p.withNoBrace(p.parseExpr)
	if err != nil {
		return nil, err
	}
	body, err := p.parseBlock()
	if err != nil {
		return nil, err
	}
	return &ast.WhileStmt{
		Span: ast.Span{
			StartLine: kw.Line, StartCol: kw.Col,
			EndLine: body.Span.EndLine, EndCol: body.Span.EndCol,
		},
		Cond: cond,
		Body: body,
	}, nil
}

// parseIterable parses the right-hand side of `for x in <here>`.
// A RangeExpr (a..b / a..=b) is detected by looking for the range
// operator after the first sub-expression. Any other Expr is
// returned directly — Iterable is just `Node`, so both *RangeExpr
// and any concrete Expr satisfy it.
func (p *parser) parseIterable() (ast.Iterable, *Diag) {
	low, err := p.parseAddSubExpr()
	if err != nil {
		return nil, err
	}
	if p.at(lexer.KindOp, "..") || p.at(lexer.KindOp, "..=") {
		op := p.advance()
		high, err := p.parseAddSubExpr()
		if err != nil {
			return nil, err
		}
		return &ast.RangeExpr{
			Span: ast.Span{
				StartLine: low.NodeSpan().StartLine, StartCol: low.NodeSpan().StartCol,
				EndLine: high.NodeSpan().EndLine, EndCol: high.NodeSpan().EndCol,
			},
			Low:       low,
			High:      high,
			Inclusive: op.Lexeme == "..=",
		}, nil
	}
	return low, nil
}

// ---- expressions (precedence climbing) ----
//
// Precedence (high → low), matching grammar.ebnf §Operator table:
//   primary   — literal / ident / paren / call / field
//   unary     — !x, -x
//   mul       — *  /  %
//   add       — +  -
//   cmp       — ==  !=  <  <=  >  >=
//   logical   — &&
//   logical   — ||

func (p *parser) parseExpr() (ast.Expr, *Diag) { return p.parseLogicalOr() }

func (p *parser) parseLogicalOr() (ast.Expr, *Diag) {
	return p.parseBinaryL(p.parseLogicalAnd, []string{"||"})
}

func (p *parser) parseLogicalAnd() (ast.Expr, *Diag) {
	return p.parseBinaryL(p.parseEq, []string{"&&"})
}

// parseEq admits a SINGLE optional `==`/`!=` operator over parseCmp,
// matching grammar.ebnf EqExpr = CmpExpr ( ("==" | "!=") CmpExpr )?
// (non-associative).
func (p *parser) parseEq() (ast.Expr, *Diag) {
	return p.parseBinaryOnce(p.parseCmp, []string{"==", "!="})
}

// parseCmp admits a SINGLE optional `<`/`<=`/`>`/`>=` operator over
// parseAddSubExpr, matching grammar.ebnf CmpExpr = AddExpr
// ( ("<"|"<="|">"|">=") AddExpr )? (non-associative).
func (p *parser) parseCmp() (ast.Expr, *Diag) {
	return p.parseBinaryOnce(p.parseAddSubExpr, []string{"<", "<=", ">", ">="})
}

func (p *parser) parseAddSubExpr() (ast.Expr, *Diag) {
	return p.parseBinaryL(p.parseMulDiv, []string{"+", "-"})
}

func (p *parser) parseMulDiv() (ast.Expr, *Diag) {
	return p.parseBinaryL(p.parseUnary, []string{"*", "/", "%"})
}

// parseBinaryL is a left-associative binary-operator helper for a
// single precedence level. ops are the operator lexemes admitted
// at this level.
func (p *parser) parseBinaryL(next func() (ast.Expr, *Diag), ops []string) (ast.Expr, *Diag) {
	left, err := next()
	if err != nil {
		return nil, err
	}
	for {
		matched := false
		for _, op := range ops {
			if p.at(lexer.KindOp, op) {
				matched = true
				opTok := p.advance()
				right, err := next()
				if err != nil {
					return nil, err
				}
				left = &ast.Binary{
					Span: ast.Span{
						StartLine: left.NodeSpan().StartLine, StartCol: left.NodeSpan().StartCol,
						EndLine: right.NodeSpan().EndLine, EndCol: right.NodeSpan().EndCol,
					},
					Op:    opTok.Lexeme,
					Left:  left,
					Right: right,
				}
				break
			}
		}
		if !matched {
			return left, nil
		}
	}
}

// parseBinaryOnce admits at most ONE operator from ops over the
// `next` parselet (non-associative). Repeated operators at this
// level (e.g., `a == b == c`) produce E0112.
func (p *parser) parseBinaryOnce(next func() (ast.Expr, *Diag), ops []string) (ast.Expr, *Diag) {
	left, err := next()
	if err != nil {
		return nil, err
	}
	for _, op := range ops {
		if !p.at(lexer.KindOp, op) {
			continue
		}
		opTok := p.advance()
		right, err := next()
		if err != nil {
			return nil, err
		}
		result := &ast.Binary{
			Span: ast.Span{
				StartLine: left.NodeSpan().StartLine, StartCol: left.NodeSpan().StartCol,
				EndLine: right.NodeSpan().EndLine, EndCol: right.NodeSpan().EndCol,
			},
			Op:    opTok.Lexeme,
			Left:  left,
			Right: right,
		}
		// Reject another operator at the same precedence — the
		// production is non-associative.
		for _, op := range ops {
			if p.at(lexer.KindOp, op) {
				t := p.peek()
				return nil, p.diag("E0112",
					fmt.Sprintf("operator %q is non-associative; parenthesise the operands", op),
					t.Line, t.Col)
			}
		}
		return result, nil
	}
	return left, nil
}

func (p *parser) parseUnary() (ast.Expr, *Diag) {
	if p.at(lexer.KindOp, "!") || p.at(lexer.KindOp, "-") {
		op := p.advance()
		operand, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &ast.Unary{
			Span: ast.Span{
				StartLine: op.Line, StartCol: op.Col,
				EndLine: operand.NodeSpan().EndLine, EndCol: operand.NodeSpan().EndCol,
			},
			Op:      op.Lexeme,
			Operand: operand,
		}, nil
	}
	return p.parsePostfix()
}

func (p *parser) parsePostfix() (ast.Expr, *Diag) {
	e, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	for {
		switch {
		case p.at(lexer.KindPunct, "("):
			call, err := p.parseCallSuffix(e, nil)
			if err != nil {
				return nil, err
			}
			e = call
		case p.at(lexer.KindOp, "<") && p.couldBeGenericCallSite():
			// `<TypeArgs>` postfix per grammar.ebnf
			// §Generic-argument disambiguation. After the closing
			// `>` exactly one of `(`, `{`, or `.` may follow.
			typeArgs, err := p.parseCallTypeArgs()
			if err != nil {
				return nil, err
			}
			switch {
			case p.at(lexer.KindPunct, "("):
				call, err := p.parseCallSuffix(e, typeArgs)
				if err != nil {
					return nil, err
				}
				e = call
			case p.at(lexer.KindPunct, "."):
				// Generic static-method call:
				//   `ClassName<T1, ...>.method(args)`
				// The type-args bind to the *class*, not the
				// method. Build a Field with the class as the
				// receiver, then a Call that wraps the Field and
				// carries the type-args to codegen.
				p.advance() // consume '.'
				if !p.at(lexer.KindIdent) {
					t := p.peek()
					return nil, p.diag("E0112", "expected method name after `.`", t.Line, t.Col)
				}
				name := p.advance()
				field := &ast.Field{
					Span: ast.Span{
						StartLine: e.NodeSpan().StartLine, StartCol: e.NodeSpan().StartCol,
						EndLine: name.Line, EndCol: name.Col + len(name.Lexeme),
					},
					Receiver: e,
					Name:     name.Lexeme,
				}
				call, err := p.parseCallSuffix(field, typeArgs)
				if err != nil {
					return nil, err
				}
				e = call
			case p.at(lexer.KindPunct, "{"):
				t := p.peek()
				return nil, p.diag("E0112",
					"generic literal `T<...>{ ... }` (BraceLit) not yet supported — use the `T<...>(...)` constructor form",
					t.Line, t.Col)
			default:
				t := p.peek()
				return nil, p.diag("E0112",
					fmt.Sprintf("expected `(`, `.`, or `{{` after generic type arguments, got %s %q", t.Kind, t.Lexeme),
					t.Line, t.Col)
			}
		case p.at(lexer.KindPunct, "."):
			p.advance()
			// `.N` (integer) is tuple-field access; `.name` is a field.
			if p.at(lexer.KindIntLit) {
				idxTok := p.advance()
				pos, perr := strconv.Atoi(idxTok.Lexeme)
				if perr != nil {
					return nil, p.diag("E0112", "malformed tuple index", idxTok.Line, idxTok.Col)
				}
				e = &ast.TupleField{
					Span: ast.Span{
						StartLine: e.NodeSpan().StartLine, StartCol: e.NodeSpan().StartCol,
						EndLine: idxTok.Line, EndCol: idxTok.Col + len(idxTok.Lexeme),
					},
					Receiver: e,
					Position: pos,
				}
				break
			}
			if !p.at(lexer.KindIdent) {
				t := p.peek()
				return nil, p.diag("E0112", "expected field name after `.`", t.Line, t.Col)
			}
			name := p.advance()
			e = &ast.Field{
				Span: ast.Span{
					StartLine: e.NodeSpan().StartLine, StartCol: e.NodeSpan().StartCol,
					EndLine: name.Line, EndCol: name.Col + len(name.Lexeme),
				},
				Receiver: e,
				Name:     name.Lexeme,
			}
		case p.at(lexer.KindPunct, "["):
			next, err := p.parseIndexOrSlice(e)
			if err != nil {
				return nil, err
			}
			e = next
		case p.at(lexer.KindPunct, "{") && !p.noBrace:
			// `Ident { … }` brace literal — record / map / set /
			// stack. Suppressed in control-flow headers (noBrace).
			id, ok := e.(*ast.Ident)
			if !ok {
				return e, nil // `expr { … }` only forms a literal off a bare name
			}
			lit, err := p.parseBraceLitBody(id)
			if err != nil {
				return nil, err
			}
			e = lit
		default:
			return e, nil
		}
	}
}

// parseBraceLitBody parses `{ … }` after a type name, committing the
// BraceKind on the first entry (RecordEntry `Ident:`, MapEntry
// `MapKey:`, SetEntry bare expr); an empty `{}` stays Unknown for
// sema. Cursor at the opening `{`. Brace-literal suppression is lifted
// inside the body (entries may themselves contain literals).
func (p *parser) parseBraceLitBody(name *ast.Ident) (*ast.BraceLit, *Diag) {
	p.advance() // consume '{'
	saved := p.noBrace
	p.noBrace = false
	defer func() { p.noBrace = saved }()
	p.skipStmtSeps()
	lit := &ast.BraceLit{
		TypeName: &ast.NamedType{Span: name.Span, QName: []string{name.Name}},
		Kind:     ast.BraceUnknown,
	}
	for !p.at(lexer.KindPunct, "}") && !p.at(lexer.KindEOF) {
		entryStart := p.peek()
		// RecordEntry: `Ident :` where the key is a bare identifier.
		if p.at(lexer.KindIdent) && p.peekAhead(1).Kind == lexer.KindPunct && p.peekAhead(1).Lexeme == ":" {
			key := p.advance()
			p.advance() // consume ':'
			val, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			if lit.Kind == ast.BraceUnknown {
				lit.Kind = ast.BraceRecord
			} else if lit.Kind != ast.BraceRecord {
				return nil, p.diag("E0112", "mixed brace-literal entry kinds", entryStart.Line, entryStart.Col)
			}
			lit.Entries = append(lit.Entries, &ast.RecordEntry{
				Span:  ast.Span{StartLine: key.Line, StartCol: key.Col, EndLine: val.NodeSpan().EndLine, EndCol: val.NodeSpan().EndCol},
				Name:  key.Lexeme,
				Value: val,
			})
		} else {
			// MapEntry (`key : value`) or SetEntry (bare value).
			keyExpr, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			if p.at(lexer.KindPunct, ":") {
				p.advance() // consume ':'
				val, err := p.parseExpr()
				if err != nil {
					return nil, err
				}
				if lit.Kind == ast.BraceUnknown {
					lit.Kind = ast.BraceMap
				} else if lit.Kind != ast.BraceMap {
					return nil, p.diag("E0112", "mixed brace-literal entry kinds", entryStart.Line, entryStart.Col)
				}
				lit.Entries = append(lit.Entries, &ast.MapEntry{
					Span:  ast.Span{StartLine: entryStart.Line, StartCol: entryStart.Col, EndLine: val.NodeSpan().EndLine, EndCol: val.NodeSpan().EndCol},
					Key:   keyExpr,
					Value: val,
				})
			} else {
				if lit.Kind == ast.BraceUnknown {
					lit.Kind = ast.BraceSet
				} else if lit.Kind != ast.BraceSet {
					return nil, p.diag("E0112", "mixed brace-literal entry kinds", entryStart.Line, entryStart.Col)
				}
				lit.Entries = append(lit.Entries, &ast.SetEntry{
					Span:  keyExpr.NodeSpan(),
					Value: keyExpr,
				})
			}
		}
		if p.at(lexer.KindPunct, ",") {
			p.advance()
		}
		p.skipStmtSeps()
	}
	closeTok, err := p.expect(lexer.KindPunct, "}")
	if err != nil {
		return nil, err
	}
	lit.Span = ast.Span{
		StartLine: name.Span.StartLine, StartCol: name.Span.StartCol,
		EndLine: closeTok.Line, EndCol: closeTok.Col + 1,
	}
	return lit, nil
}

// parseIndexOrSlice parses the postfix `[i]` or `[lo:hi]` /
// `[lo:]` / `[:hi]` form. Cursor at `[`.
func (p *parser) parseIndexOrSlice(recv ast.Expr) (ast.Expr, *Diag) {
	savedNB := p.noBrace
	p.noBrace = false
	defer func() { p.noBrace = savedNB }()
	p.advance() // consume '['
	// `[:hi]` — leading colon.
	if p.at(lexer.KindPunct, ":") {
		p.advance() // consume ':'
		// `[:]` is a copy slice; `[:hi]` has High.
		var high ast.Expr
		if !p.at(lexer.KindPunct, "]") {
			h, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			high = h
		}
		closeTok, err := p.expect(lexer.KindPunct, "]")
		if err != nil {
			return nil, err
		}
		return &ast.Slice{
			Span: ast.Span{
				StartLine: recv.NodeSpan().StartLine, StartCol: recv.NodeSpan().StartCol,
				EndLine: closeTok.Line, EndCol: closeTok.Col + 1,
			},
			Receiver: recv,
			Low:      nil,
			High:     high,
		}, nil
	}
	first, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if p.at(lexer.KindPunct, ":") {
		p.advance() // consume ':'
		var high ast.Expr
		if !p.at(lexer.KindPunct, "]") {
			h, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			high = h
		}
		closeTok, err := p.expect(lexer.KindPunct, "]")
		if err != nil {
			return nil, err
		}
		return &ast.Slice{
			Span: ast.Span{
				StartLine: recv.NodeSpan().StartLine, StartCol: recv.NodeSpan().StartCol,
				EndLine: closeTok.Line, EndCol: closeTok.Col + 1,
			},
			Receiver: recv,
			Low:      first,
			High:     high,
		}, nil
	}
	closeTok, err := p.expect(lexer.KindPunct, "]")
	if err != nil {
		return nil, err
	}
	return &ast.Index{
		Span: ast.Span{
			StartLine: recv.NodeSpan().StartLine, StartCol: recv.NodeSpan().StartCol,
			EndLine: closeTok.Line, EndCol: closeTok.Col + 1,
		},
		Receiver: recv,
		Idx:      first,
	}, nil
}

// couldBeGenericCallSite peeks at the token stream starting at
// the current `<` and returns true iff the `<` opens a
// generic-argument list. Per `lang-spec/grammar.ebnf`
// §Generic-argument disambiguation: commit when the matching
// `>` is followed by one of `(`, `{`, or `.` — function call,
// generic literal, or generic static-method call respectively.
// Otherwise the `<` is the comparison operator.
//
// Implementation is token-only depth-counting (no rewind /
// speculative parse). It is equivalent to the speculative-parse
// shape for the v1 type-argument grammar (Ident, `.`, `[`, `]`,
// `,`, nested `<>`). Adding richer type forms to TypeArgs
// (TupleType, FuncType, ParenType, ...) must extend the
// allowed-token set below or the disambig will silently
// false-negative on those shapes.
func (p *parser) couldBeGenericCallSite() bool {
	pos := p.pos
	if pos >= len(p.toks) || p.toks[pos].Kind != lexer.KindOp || p.toks[pos].Lexeme != "<" {
		return false
	}
	depth := 0
	for i := pos; i < len(p.toks); i++ {
		t := p.toks[i]
		switch {
		case t.Kind == lexer.KindOp && t.Lexeme == "<":
			depth++
		case t.Kind == lexer.KindOp && t.Lexeme == ">":
			depth--
			if depth == 0 {
				// Next non-newline token decides per
				// `grammar.ebnf` §Generic-argument disambiguation:
				// commit on `(`, `{`, or `.`.
				for j := i + 1; j < len(p.toks); j++ {
					n := p.toks[j]
					if n.Kind == lexer.KindNewline {
						continue
					}
					if n.Kind != lexer.KindPunct {
						return false
					}
					return n.Lexeme == "(" || n.Lexeme == "{" || n.Lexeme == "."
				}
				return false
			}
		case t.Kind == lexer.KindIdent,
			t.Kind == lexer.KindPunct && (t.Lexeme == "," || t.Lexeme == "." || t.Lexeme == "[" || t.Lexeme == "]"):
			// allowed inside a type-arg list
		default:
			return false
		}
	}
	return false
}

// couldBeShortClosure peeks from the cursor at `(` to its matching
// `)` and reports whether `=>` immediately follows — i.e. this is a
// short closure `(params) => expr`, not a parenthesised expr / tuple.
func (p *parser) couldBeShortClosure() bool {
	if !p.at(lexer.KindPunct, "(") {
		return false
	}
	depth := 0
	for i := p.pos; i < len(p.toks); i++ {
		t := p.toks[i]
		if t.Kind == lexer.KindPunct && t.Lexeme == "(" {
			depth++
		} else if t.Kind == lexer.KindPunct && t.Lexeme == ")" {
			depth--
			if depth == 0 {
				for j := i + 1; j < len(p.toks); j++ {
					n := p.toks[j]
					if n.Kind == lexer.KindNewline {
						continue
					}
					return n.Kind == lexer.KindOp && n.Lexeme == "=>"
				}
				return false
			}
		}
	}
	return false
}

// parseShortClosure parses `(p1, p2, ...) => expr` (grammar.ebnf
// ShortParamList). Params may carry an optional `: TypeExpr`. The
// `=> expr` body is wrapped in a Block whose trailing value is expr.
func (p *parser) parseShortClosure() (*ast.ClosureLit, *Diag) {
	open := p.advance() // consume '('
	p.skipNewlines()
	var params []*ast.Param
	for !p.at(lexer.KindPunct, ")") && !p.at(lexer.KindEOF) {
		if !p.at(lexer.KindIdent) {
			t := p.peek()
			return nil, p.diag("E0112", "expected closure parameter name", t.Line, t.Col)
		}
		nameTok := p.advance()
		param := &ast.Param{
			Span: ast.Span{StartLine: nameTok.Line, StartCol: nameTok.Col,
				EndLine: nameTok.Line, EndCol: nameTok.Col + len(nameTok.Lexeme)},
			Name: nameTok.Lexeme,
		}
		if p.at(lexer.KindPunct, ":") {
			p.advance()
			ty, err := p.parseTypeExpr()
			if err != nil {
				return nil, err
			}
			param.DeclType = ty
			param.Span.EndLine, param.Span.EndCol = ty.NodeSpan().EndLine, ty.NodeSpan().EndCol
		}
		params = append(params, param)
		p.skipNewlines()
		if !p.at(lexer.KindPunct, ",") {
			break
		}
		p.advance()
		p.skipNewlines()
	}
	if _, err := p.expect(lexer.KindPunct, ")"); err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.KindOp, "=>"); err != nil {
		return nil, err
	}
	body, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	bodyBlock := &ast.Block{Span: body.NodeSpan(), Trailing: body}
	return &ast.ClosureLit{
		Span: ast.Span{
			StartLine: open.Line, StartCol: open.Col,
			EndLine: body.NodeSpan().EndLine, EndCol: body.NodeSpan().EndCol,
		},
		Params: params,
		Body:   bodyBlock,
		Short:  true,
	}, nil
}

// parseFuncClosure parses the full closure form `func(ParamList)
// ReturnAnnot? Block` in expression position. Cursor at `func`.
func (p *parser) parseFuncClosure() (*ast.ClosureLit, *Diag) {
	kw := p.advance() // consume 'func'
	if _, err := p.expect(lexer.KindPunct, "("); err != nil {
		return nil, err
	}
	params, err := p.parseParamList()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.KindPunct, ")"); err != nil {
		return nil, err
	}
	var ret ast.TypeExpr
	if p.at(lexer.KindPunct, ":") {
		p.advance()
		ret, err = p.parseTypeExpr()
		if err != nil {
			return nil, err
		}
	}
	body, err := p.parseBlock()
	if err != nil {
		return nil, err
	}
	return &ast.ClosureLit{
		Span: ast.Span{
			StartLine: kw.Line, StartCol: kw.Col,
			EndLine: body.Span.EndLine, EndCol: body.Span.EndCol,
		},
		Params:     params,
		ReturnType: ret,
		Body:       body,
	}, nil
}

func (p *parser) parseCallTypeArgs() ([]ast.TypeExpr, *Diag) {
	if _, err := p.expect(lexer.KindOp, "<"); err != nil {
		return nil, err
	}
	var args []ast.TypeExpr
	for {
		p.skipNewlines()
		t, err := p.parseTypeExpr()
		if err != nil {
			return nil, err
		}
		args = append(args, t)
		if p.at(lexer.KindPunct, ",") {
			p.advance()
			continue
		}
		break
	}
	if _, err := p.expect(lexer.KindOp, ">"); err != nil {
		return nil, err
	}
	return args, nil
}

func (p *parser) parseCallSuffix(callee ast.Expr, typeArgs []ast.TypeExpr) (*ast.Call, *Diag) {
	// Inside `( … )` arguments a `{` is unambiguously a brace
	// literal — lift any header brace-suppression.
	savedNB := p.noBrace
	p.noBrace = false
	defer func() { p.noBrace = savedNB }()
	if _, err := p.expect(lexer.KindPunct, "("); err != nil {
		return nil, err
	}
	c := &ast.Call{Callee: callee, TypeArgs: typeArgs}
	p.skipNewlines()
	if !p.at(lexer.KindPunct, ")") {
		for {
			arg, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			c.Args = append(c.Args, arg)
			p.skipNewlines()
			if !p.at(lexer.KindPunct, ",") {
				break
			}
			p.advance() // consume ','
			p.skipNewlines()
		}
	}
	closeTok, err := p.expect(lexer.KindPunct, ")")
	if err != nil {
		return nil, err
	}
	c.Span = ast.Span{
		StartLine: callee.NodeSpan().StartLine, StartCol: callee.NodeSpan().StartCol,
		EndLine: closeTok.Line, EndCol: closeTok.Col + 1,
	}
	return c, nil
}

func (p *parser) parsePrimary() (ast.Expr, *Diag) {
	t := p.peek()
	switch t.Kind {
	case lexer.KindIntLit:
		p.advance()
		v, err := parseIntLit(t.Lexeme)
		if err != nil {
			return nil, p.diag("E0109", "Malformed numeric literal", t.Line, t.Col)
		}
		return &ast.IntLitExpr{
			Span:    spanFromToken(t),
			RawText: t.Lexeme,
			Value:   v,
		}, nil
	case lexer.KindFloatLit:
		p.advance()
		fv, err := strconv.ParseFloat(t.Lexeme, 64)
		if err != nil {
			return nil, p.diag("E0109", "Malformed numeric literal", t.Line, t.Col)
		}
		return &ast.FloatLitExpr{
			Span:    spanFromToken(t),
			RawText: t.Lexeme,
			Value:   fv,
		}, nil
	case lexer.KindStringLit:
		p.advance()
		val, err := decodeStringLit(t.Lexeme)
		if err != nil {
			return nil, p.diag("E0110", "Malformed escape sequence", t.Line, t.Col)
		}
		return &ast.StringLitExpr{
			Span:    spanFromToken(t),
			RawText: t.Lexeme,
			Value:   val,
		}, nil
	case lexer.KindRuneLit:
		p.advance()
		val, err := decodeRuneLit(t.Lexeme)
		if err != nil {
			return nil, p.diag("E0110", "Malformed rune literal", t.Line, t.Col)
		}
		return &ast.RuneLitExpr{
			Span:    spanFromToken(t),
			RawText: t.Lexeme,
			Value:   val,
		}, nil
	case lexer.KindKeyword:
		switch t.Lexeme {
		case "true":
			p.advance()
			return &ast.BoolLitExpr{Span: spanFromToken(t), Value: true}, nil
		case "false":
			p.advance()
			return &ast.BoolLitExpr{Span: spanFromToken(t), Value: false}, nil
		case "match":
			return p.parseMatchExpr()
		case "if":
			return p.parseIfExpr()
		case "func":
			return p.parseFuncClosure()
		case "break":
			bt := p.advance()
			return &ast.BreakExpr{Span: spanFromToken(bt)}, nil
		case "continue":
			ct := p.advance()
			return &ast.ContinueExpr{Span: spanFromToken(ct)}, nil
		case "this":
			p.advance()
			return &ast.ThisExpr{Span: spanFromToken(t)}, nil
		case "try":
			tk := p.advance()
			inner, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			return &ast.TryExpr{
				Span: ast.Span{
					StartLine: tk.Line, StartCol: tk.Col,
					EndLine: inner.NodeSpan().EndLine, EndCol: inner.NodeSpan().EndCol,
				},
				Inner: inner,
			}, nil
		}
		return nil, p.diag("E0112", fmt.Sprintf("unexpected keyword %q in expression", t.Lexeme), t.Line, t.Col)
	case lexer.KindIdent:
		p.advance()
		return &ast.Ident{Span: spanFromToken(t), Name: t.Lexeme}, nil
	case lexer.KindPunct:
		if t.Lexeme == "(" {
			// `(params) => expr` short closure — disambiguated from a
			// parenthesised expr / tuple by a `=>` after the `)`.
			if p.couldBeShortClosure() {
				return p.parseShortClosure()
			}
			// Inside parens a `{` is unambiguous — lift header
			// brace-suppression for the whole `( … )`.
			savedNB := p.noBrace
			p.noBrace = false
			defer func() { p.noBrace = savedNB }()
			open := p.advance()
			p.skipNewlines()
			e, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			// `(e, e, ...)` is a tuple literal; `(e)` is grouping.
			if p.at(lexer.KindPunct, ",") {
				comps := []ast.Expr{e}
				for p.at(lexer.KindPunct, ",") {
					p.advance() // consume ','
					p.skipNewlines()
					if p.at(lexer.KindPunct, ")") {
						break // trailing comma
					}
					c, err := p.parseExpr()
					if err != nil {
						return nil, err
					}
					comps = append(comps, c)
					p.skipNewlines()
				}
				closeTok, err := p.expect(lexer.KindPunct, ")")
				if err != nil {
					return nil, err
				}
				return &ast.TupleLit{
					Span: ast.Span{
						StartLine: open.Line, StartCol: open.Col,
						EndLine: closeTok.Line, EndCol: closeTok.Col + 1,
					},
					Components: comps,
				}, nil
			}
			closeTok, err := p.expect(lexer.KindPunct, ")")
			if err != nil {
				return nil, err
			}
			// Keep the grouping as a node — flattening to `e` would
			// drop the author's precedence intent (`a * (b + c)`).
			return &ast.ParenExpr{
				Span: ast.Span{
					StartLine: open.Line, StartCol: open.Col,
					EndLine: closeTok.Line, EndCol: closeTok.Col + 1,
				},
				Inner: e,
			}, nil
		}
		if t.Lexeme == "[" {
			return p.parseSliceLit()
		}
		if t.Lexeme == "{" {
			// Block-as-expression. Only reached in true expression
			// position — control-flow headers consume their `{`
			// through parseBlock, never through parsePrimary.
			return p.parseValueBlock()
		}
	}
	return nil, p.diag("E0112",
		fmt.Sprintf("expected expression, got %s %q", t.Kind, t.Lexeme),
		t.Line, t.Col)
}

// ---- helpers ----

func spanFromToken(t lexer.Token) ast.Span {
	return ast.Span{
		StartLine: t.Line, StartCol: t.Col,
		EndLine: t.Line, EndCol: t.Col + tokenWidth(t),
	}
}

func tokenWidth(t lexer.Token) int {
	// Char-counted (not byte-counted) token width — matches the
	// lexer's column-counting convention (utf8-aware).
	return utf8.RuneCountInString(t.Lexeme)
}

func parseIntLit(s string) (int64, error) {
	// Strip "_" separators (grammar.ebnf admits them anywhere in
	// the literal body); pick base from prefix.
	clean := strings.ReplaceAll(s, "_", "")
	if strings.HasPrefix(clean, "0x") || strings.HasPrefix(clean, "0X") {
		return strconv.ParseInt(clean[2:], 16, 64)
	}
	if strings.HasPrefix(clean, "0o") || strings.HasPrefix(clean, "0O") {
		return strconv.ParseInt(clean[2:], 8, 64)
	}
	if strings.HasPrefix(clean, "0b") || strings.HasPrefix(clean, "0B") {
		return strconv.ParseInt(clean[2:], 2, 64)
	}
	return strconv.ParseInt(clean, 10, 64)
}

// decodeRuneLit converts a single-quoted rune literal lexeme
// (`'a'`, `'\n'`, `'\\'`) to its decoded code point. Delegates
// to Go's strconv.UnquoteChar so all standard Go rune escapes
// (`\n`, `\t`, `\xNN`, `\uNNNN`, `\UNNNNNNNN`) are accepted.
func decodeRuneLit(s string) (int32, error) {
	if len(s) < 3 || s[0] != '\'' || s[len(s)-1] != '\'' {
		return 0, fmt.Errorf("not a rune literal")
	}
	r, _, _, err := strconv.UnquoteChar(s[1:len(s)-1], '\'')
	if err != nil {
		return 0, err
	}
	return int32(r), nil
}

// decodeStringLit converts a lexer-token lexeme `"hello\n"` to the
// decoded value `hello<LF>`.
func decodeStringLit(s string) (string, error) {
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return "", fmt.Errorf("not a string literal")
	}
	inner := s[1 : len(s)-1]
	var b strings.Builder
	for i := 0; i < len(inner); {
		c := inner[i]
		if c != '\\' {
			b.WriteByte(c)
			i++
			continue
		}
		if i+1 >= len(inner) {
			return "", fmt.Errorf("trailing backslash")
		}
		esc := inner[i+1]
		switch esc {
		case 'n':
			b.WriteByte('\n')
			i += 2
		case 't':
			b.WriteByte('\t')
			i += 2
		case 'r':
			b.WriteByte('\r')
			i += 2
		case '\\':
			b.WriteByte('\\')
			i += 2
		case '"':
			b.WriteByte('"')
			i += 2
		case '\'':
			b.WriteByte('\'')
			i += 2
		case '0':
			b.WriteByte(0)
			i += 2
		case 'x':
			if i+3 >= len(inner) {
				return "", fmt.Errorf("short \\x escape")
			}
			n, err := strconv.ParseInt(inner[i+2:i+4], 16, 32)
			if err != nil {
				return "", err
			}
			b.WriteByte(byte(n))
			i += 4
		case 'u':
			if i+5 >= len(inner) {
				return "", fmt.Errorf("short \\u escape")
			}
			n, err := strconv.ParseInt(inner[i+2:i+6], 16, 32)
			if err != nil {
				return "", err
			}
			b.WriteRune(rune(n))
			i += 6
		default:
			return "", fmt.Errorf("unknown escape \\%c", esc)
		}
	}
	return b.String(), nil
}

// ---- match expression + patterns ----

// parseMatchExpr expects the cursor at the `match` keyword.
// Form: `match Subject { Pat => Body (,|nl) ... }`.
func (p *parser) parseMatchExpr() (*ast.MatchExpr, *Diag) {
	kw := p.advance() // consume 'match'
	subject, err := p.withNoBrace(p.parseExpr)
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(lexer.KindPunct, "{"); err != nil {
		return nil, err
	}
	p.skipNewlines()
	var arms []*ast.MatchArm
	for !p.at(lexer.KindPunct, "}") && !p.at(lexer.KindEOF) {
		armStart := p.peek()
		pat, err := p.parsePattern()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(lexer.KindOp, "=>"); err != nil {
			return nil, err
		}
		body, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		arms = append(arms, &ast.MatchArm{
			Span: ast.Span{
				StartLine: armStart.Line, StartCol: armStart.Col,
				EndLine: body.NodeSpan().EndLine, EndCol: body.NodeSpan().EndCol,
			},
			Pattern: pat,
			Body:    body,
		})
		p.skipNewlines()
		// Per grammar.ebnf:512 — comma separates arms; an
		// optional trailing comma is admitted after the last
		// arm. So: if next is `}` we're done; otherwise a
		// comma must follow.
		if p.at(lexer.KindPunct, "}") {
			break
		}
		if !p.at(lexer.KindPunct, ",") {
			t := p.peek()
			return nil, p.diag("E0112",
				"expected `,` between match arms (or `}` to close)",
				t.Line, t.Col)
		}
		p.advance() // consume ','
		p.skipNewlines()
	}
	closeTok, err := p.expect(lexer.KindPunct, "}")
	if err != nil {
		return nil, err
	}
	if len(arms) == 0 {
		return nil, p.diag("E0112", "match needs at least one arm", kw.Line, kw.Col)
	}
	return &ast.MatchExpr{
		Span: ast.Span{
			StartLine: kw.Line, StartCol: kw.Col,
			EndLine: closeTok.Line, EndCol: closeTok.Col + 1,
		},
		Subject: subject,
		Arms:    arms,
	}, nil
}

// parsePattern admits IdentPat, WildcardPat (`_`),
// IntLitPat, StringLitPat, BoolLitPat, TuplePat, and VariantPat
// (with optional payload subpatterns). Record, AltPat land later.
func (p *parser) parsePattern() (ast.Pattern, *Diag) {
	t := p.peek()
	// TuplePat: `(p1, p2, ...)` — arity ≥ 2.
	if t.Kind == lexer.KindPunct && t.Lexeme == "(" {
		open := p.advance() // consume '('
		p.skipNewlines()
		var sub []ast.Pattern
		for !p.at(lexer.KindPunct, ")") && !p.at(lexer.KindEOF) {
			sp, err := p.parsePattern()
			if err != nil {
				return nil, err
			}
			sub = append(sub, sp)
			p.skipNewlines()
			if !p.at(lexer.KindPunct, ",") {
				break
			}
			p.advance() // consume ','
			p.skipNewlines()
		}
		closeTok, err := p.expect(lexer.KindPunct, ")")
		if err != nil {
			return nil, err
		}
		if len(sub) < 2 {
			return nil, p.diag("E0112", "tuple pattern needs at least two components", open.Line, open.Col)
		}
		return &ast.TuplePat{
			Span: ast.Span{
				StartLine: open.Line, StartCol: open.Col,
				EndLine: closeTok.Line, EndCol: closeTok.Col + 1,
			},
			Sub: sub,
		}, nil
	}
	switch t.Kind {
	case lexer.KindIdent:
		// Could be IdentPat or VariantPat. PR-F2 uses
		// capitalisation as a parser-time proxy for the
		// resolution-time check documented in
		// name-resolution.md §Variant constructors (which
		// notes that the canonical algorithm is resolution-
		// time, not parser-commit). A VariantPat may also be
		// qualified: `Type.V`. The resolver may later reclass
		// IdentPat as VariantPat for in-scope variants —
		// parser only commits the shape based on syntax.
		nameTok := p.advance()
		qname := []string{nameTok.Lexeme}
		endLine, endCol := nameTok.Line, nameTok.Col+utf8.RuneCountInString(nameTok.Lexeme)
		for p.at(lexer.KindPunct, ".") {
			p.advance() // consume '.'
			if !p.at(lexer.KindIdent) {
				t := p.peek()
				return nil, p.diag("E0112", "expected identifier after `.` in pattern", t.Line, t.Col)
			}
			next := p.advance()
			qname = append(qname, next.Lexeme)
			endLine, endCol = next.Line, next.Col+utf8.RuneCountInString(next.Lexeme)
		}
		if isCapitalised(nameTok.Lexeme) || len(qname) > 1 || p.at(lexer.KindPunct, "(") {
			vp := &ast.VariantPat{
				Span: ast.Span{
					StartLine: nameTok.Line, StartCol: nameTok.Col,
					EndLine: endLine, EndCol: endCol,
				},
				QName: qname,
			}
			if p.at(lexer.KindPunct, "(") {
				p.advance() // consume '('
				p.skipNewlines()
				for !p.at(lexer.KindPunct, ")") {
					sp, err := p.parsePattern()
					if err != nil {
						return nil, err
					}
					vp.Sub = append(vp.Sub, sp)
					p.skipNewlines()
					if !p.at(lexer.KindPunct, ",") {
						break
					}
					p.advance() // consume ','
					p.skipNewlines()
				}
				closeTok, err := p.expect(lexer.KindPunct, ")")
				if err != nil {
					return nil, err
				}
				vp.Span.EndLine = closeTok.Line
				vp.Span.EndCol = closeTok.Col + 1
			}
			return vp, nil
		}
		// Lower-case → IdentPat. `_` is handled as WildcardPat
		// via the Ident path; intercept here.
		if nameTok.Lexeme == "_" {
			return &ast.WildcardPat{
				Span: ast.Span{
					StartLine: nameTok.Line, StartCol: nameTok.Col,
					EndLine: nameTok.Line, EndCol: nameTok.Col + 1,
				},
			}, nil
		}
		return &ast.IdentPat{
			Span: ast.Span{
				StartLine: nameTok.Line, StartCol: nameTok.Col,
				EndLine: nameTok.Line, EndCol: nameTok.Col + utf8.RuneCountInString(nameTok.Lexeme),
			},
			Name: nameTok.Lexeme,
		}, nil
	case lexer.KindIntLit:
		p.advance()
		v, err := parseIntLit(t.Lexeme)
		if err != nil {
			return nil, p.diag("E0109", "Malformed numeric literal", t.Line, t.Col)
		}
		return &ast.IntLitPat{
			Span:    spanFromToken(t),
			RawText: t.Lexeme,
			Value:   v,
		}, nil
	case lexer.KindFloatLit:
		p.advance()
		fv, err := strconv.ParseFloat(t.Lexeme, 64)
		if err != nil {
			return nil, p.diag("E0109", "Malformed numeric literal", t.Line, t.Col)
		}
		// Grammar-legal but sema rejects with E0305.
		return &ast.FloatLitPat{
			Span:    spanFromToken(t),
			RawText: t.Lexeme,
			Value:   fv,
		}, nil
	case lexer.KindStringLit:
		p.advance()
		val, err := decodeStringLit(t.Lexeme)
		if err != nil {
			return nil, p.diag("E0110", "Malformed escape sequence", t.Line, t.Col)
		}
		return &ast.StringLitPat{
			Span:  spanFromToken(t),
			Value: val,
		}, nil
	case lexer.KindKeyword:
		switch t.Lexeme {
		case "true":
			p.advance()
			return &ast.BoolLitPat{Span: spanFromToken(t), Value: true}, nil
		case "false":
			p.advance()
			return &ast.BoolLitPat{Span: spanFromToken(t), Value: false}, nil
		}
	}
	return nil, p.diag("E0112",
		fmt.Sprintf("expected pattern, got %s %q", t.Kind, t.Lexeme),
		t.Line, t.Col)
}

func isCapitalised(s string) bool {
	if s == "" {
		return false
	}
	r := s[0]
	return r >= 'A' && r <= 'Z'
}

// parseSliceLit parses either of:
//   - `[expr, expr, ...]`         — inferred-element-type form
//   - `[]T{}` or `[]T{e1, ...}`   — annotated-type form
//
// The cursor is at the leading `[`.
func (p *parser) parseSliceLit() (*ast.SliceLit, *Diag) {
	savedNB := p.noBrace
	p.noBrace = false
	defer func() { p.noBrace = savedNB }()
	openTok := p.advance() // consume '['
	// Annotated form: `[` immediately followed by `]` is the
	// SliceType prefix; the following `{...}` carries the items.
	if p.at(lexer.KindPunct, "]") {
		p.advance() // consume ']'
		// Element type follows.
		elem, err := p.parseTypeExpr()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(lexer.KindPunct, "{"); err != nil {
			return nil, err
		}
		p.skipNewlines()
		var items []ast.Expr
		for !p.at(lexer.KindPunct, "}") {
			it, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			items = append(items, it)
			p.skipNewlines()
			if !p.at(lexer.KindPunct, ",") {
				break
			}
			p.advance() // consume ','
			p.skipNewlines()
		}
		closeTok, err := p.expect(lexer.KindPunct, "}")
		if err != nil {
			return nil, err
		}
		return &ast.SliceLit{
			Span: ast.Span{
				StartLine: openTok.Line, StartCol: openTok.Col,
				EndLine: closeTok.Line, EndCol: closeTok.Col + 1,
			},
			ElemType: elem,
			Items:    items,
		}, nil
	}
	// Inferred form: `[e1, e2, ...]`. At least one element.
	var items []ast.Expr
	for {
		it, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		items = append(items, it)
		p.skipNewlines()
		if !p.at(lexer.KindPunct, ",") {
			break
		}
		p.advance() // consume ','
		p.skipNewlines()
	}
	closeTok, err := p.expect(lexer.KindPunct, "]")
	if err != nil {
		return nil, err
	}
	return &ast.SliceLit{
		Span: ast.Span{
			StartLine: openTok.Line, StartCol: openTok.Col,
			EndLine: closeTok.Line, EndCol: closeTok.Col + 1,
		},
		Items: items,
	}, nil
}
