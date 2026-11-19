// Why: the parser and every later pass need one shared, stable description
// of syntax; this package is that contract and holds no logic beyond
// traversal, printing and checks of the tree.
//
// What: the package doc; the types live in token.go, node.go, decl.go,
// expr.go, stmt.go and typenode.go.
//
// How: a typed tree plus a token list that keeps trivia, so the source can
// be printed back byte for byte.

// Package ast holds the syntax contract between the parser and every later
// pass:
//
//   - TokenKind, Token and TokRange: every token of a file, trivia included,
//     covering the text exactly, with the keyword table of doc/reference.md.
//   - Node and its categories (Item, Decl, Type, Expr, TypeOrExpr, Stmt,
//     Pattern, Flow, Member): one pointer-based node type per production of
//     doc/grammar.md that makes a node, like go/ast. Every node embeds
//     Base (its file-local NodeID and span); items embed ItemInfo (export flag
//     and token range for the per-item token hash).
//   - EachChild, Walk and Inspect: depth-first traversal in source order.
//   - Fprint, Sprint and Describe: the deterministic debug printer.
//   - Validate and PrintBack: the lossless-token and well-formed-tree checks.
//
// The tree is immutable after parsing. Resolution and types live in side
// tables indexed by NodeID, outside it. Names are source.Symbols.
package ast
