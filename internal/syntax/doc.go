// Why: the parser is the front of the compiler pipeline; every later stage
// reads its token list and tree.
//
// What: the package documentation of syntax; the code is in the other files.
//
// How: the lexer (lexer.go) produces a lossless token list, the parser
// (parser.go and the production files) builds an internal/ast tree, and
// parse-phase diagnostics are collected per file.

// Package syntax turns one normalized source file into its lossless token
// list and its typed tree (internal/ast), and reports every parse-phase
// diagnostic of that file: lexical and grammatical E0101, E0102 at
// declarations, the parse-phase habits (E1203 to E1222),
// closure syntax (E0609) and the macro forms of E1221. It also decodes literal
// tokens, hashes items for the cache and parses generated fragments.
//
// Every exported function is safe to call from many goroutines on different
// files; the only shared state is the *source.Interner.
package syntax
