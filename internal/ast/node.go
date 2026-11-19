// Why: every pass walks the same tree, so the node identity, the grammar
// categories and the error-recovery nodes are defined once, here.
//
// What: NodeID, Base, ItemInfo, the Node interface and its categories (Item,
// Decl, Type, Expr, TypeOrExpr, Stmt, Pattern, Flow, Member), File, Ident,
// Path, BasicLit, and the recovery nodes BadItem, BadType, BadExpr, BadStmt
// and BadPattern.
//
// How: a pointer-based AST like go/ast, one node type per production that
// makes a node, immutable after parsing, with side tables indexed by NodeID.
// A syntax error leaves a recovery node holding the skipped tokens, so later
// passes always see a total tree.

package ast

import "mk/internal/source"

// NodeID numbers a node within its file: 1 .. File.NodeCount, assigned by the
// parser in the order it creates nodes, so the same text always gives the same
// IDs. The zero NodeID means "none". Passes keep per-node facts in slices
// indexed by NodeID (resolution, types), never in maps on hot paths. A NodeID
// is never written to a hash or to output.
type NodeID uint32

// NoNode is the zero NodeID.
const NoNode NodeID = 0

// Base is embedded first in every node: its ID and its span. The span covers
// the node's first significant token to its last; a node the parser had to
// invent for recovery has an empty span at the insertion point.
type Base struct {
	NID NodeID
	Sp  source.Span
}

// ID is the node's file-local number.
func (b *Base) ID() NodeID { return b.NID }

// Span is the node's source span.
func (b *Base) Span() source.Span { return b.Sp }

// Node is every syntax node. Child fields hold either a node or a nil
// interface: the parser never stores a typed nil pointer in an interface
// field, so a nil check is enough everywhere.
type Node interface {
	ID() NodeID
	Span() source.Span
	aNode()
}

// Item is a top-level element of a file: Import, GenItem, TestItem or a Decl.
type Item interface {
	Node
	// Info is the item's export flag and token range.
	Info() *ItemInfo
	itemNode()
}

// Decl is an Item that declares a name (the grammar's Decl), or a BadItem.
type Decl interface {
	Item
	declNode()
}

// ItemInfo is embedded in every item node.
type ItemInfo struct {
	// Export is true when the item is written "export ...".
	Export bool
	// Toks is the item's token range, from its first doc comment or keyword to
	// its last token, trivia inside included. The per-item token hash of stage 2
	// is computed over the non-trivia tokens of this range.
	Toks TokRange
}

// Info returns the embedded ItemInfo.
func (i *ItemInfo) Info() *ItemInfo { return i }

// Type is a type node (the grammar's Type and its alternatives).
type Type interface {
	Node
	typeNode()
	typeOrExpr()
}

// Expr is an expression node (the grammar's Expr and its alternatives).
type Expr interface {
	Node
	exprNode()
	typeOrExpr()
}

// TypeOrExpr is a slot the shared bracket rule fills with a type node or an
// expression node (the grammar's TypeOrExpr, TypeArgs, Gen arguments,
// IndexExpr): the resolver decides which reading applies.
type TypeOrExpr interface {
	Node
	typeOrExpr()
}

// Stmt is a statement node (the grammar's Stmt).
type Stmt interface {
	Node
	stmtNode()
}

// Pattern is a pattern node (the grammar's Pattern, Alt).
type Pattern interface {
	Node
	patNode()
}

// Flow is a flow clause of a signature or fn type (the grammar's Flow).
type Flow interface {
	Node
	flowNode()
}

// Member is a struct member (the grammar's Member).
type Member interface {
	Node
	memberNode()
}

// File is one parsed source file: its items and its complete token list.
type File struct {
	Base
	// Path is the file's path as in its source.File.
	Path string
	// Items are the file's items in source order.
	Items []Item
	// Tokens is every token of the file, trivia included, ending with EOF.
	Tokens []Token
	// NodeCount is the largest NodeID in the file (the File node included):
	// side tables are sized NodeCount+1.
	NodeCount uint32
}

// Ident is one name: an identifier token, interned. It is the NameExpr of
// expressions and the name of every declaration.
type Ident struct {
	Base
	Name source.Symbol
}

// Path is Name { "." Name } (the grammar's Path).
type Path struct {
	Base
	Names []*Ident
}

// BasicLit is a literal token: TokInt, TokFloat, TokChar, TokByte, TokStr,
// TokRawStr, TokMultiStr or TokByteStr. Raw is its spelling exactly as written; the checker decodes it.
type BasicLit struct {
	Base
	Kind TokenKind
	Raw  string
}

// Recovery nodes. On a syntax error the parser reports it
// (E0101 or a habit code) and keeps the skipped tokens in one of these, so
// every later pass sees a total tree. Toks may be empty for a missing element.

// BadItem is an item that did not parse.
type BadItem struct {
	Base
	ItemInfo
}

// BadType is a type that did not parse.
type BadType struct {
	Base
	Toks TokRange
}

// BadExpr is an expression that did not parse.
type BadExpr struct {
	Base
	Toks TokRange
}

// BadStmt is a statement that did not parse.
type BadStmt struct {
	Base
	Toks TokRange
}

// BadPattern is a pattern that did not parse.
type BadPattern struct {
	Base
	Toks TokRange
}
