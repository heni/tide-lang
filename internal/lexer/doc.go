// Package lexer turns Tide source text into a stream of tokens.
//
// Contract: lang-spec/grammar.ebnf lexical part.
// Canonical token serialization: lang-spec/test-contract.md §TOKENS.
// Diagnostic codes: lang-spec/diagnostics.md (E0101, E0102, E0107,
// E0109, E0110, E0111).
//
// The public surface is Lex (and LexFile for callers that have a
// source path). Both return a token stream terminated by an EOF
// token, plus an optional Diag describing the first lex-time
// error.
package lexer
