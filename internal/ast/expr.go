// Why: expressions are the largest part of the syntax; their node types
// are kept in one file, one per production that makes a node.
//
// What: the expression productions of doc/grammar.md: Expr, Fallback, Or,
// And, Compare, Add, Mul, Unary, Postfix, Suffix, Args, Arg, Primary,
// RecordLit, RecField, ArrayLit, FnLit and Quote.
//
// How: a parenthesized lone name is a record literal, not a ParenExpr; a
// quote holds raw tokens and splices, not a parsed tree.

package ast

// ElseExpr is Or "else" [ Name ] Block: handle the error of a fallible call.
type ElseExpr struct {
	Base
	X       Expr
	Name    *Ident // nil when absent
	Handler *Block
}

// BinaryExpr is X Op Y over the ladder || < && < compare < add < mul. Op is
// the operator's TokenKind. In bracket arguments Op may also be TokPipe or TokBang
// spelling an error set or E!T; the resolver reinterprets those as types.
type BinaryExpr struct {
	Base
	X  Expr
	Op TokenKind
	Y  Expr
}

// UnaryExpr is UnaryOp Unary: Op is TokMinus, TokBang, TokTilde, TokAmp,
// TokMut, TokStar or TokTry.
type UnaryExpr struct {
	Base
	Op TokenKind
	X  Expr
}

// FieldExpr is X "." Name.
type FieldExpr struct {
	Base
	X    Expr
	Name *Ident
}

// GoExpr is X "." "go" Postfix: s.go F(a).
type GoExpr struct {
	Base
	Scope Expr
	Call  Expr
}

// IndexExpr is X TypeArgs: an index, or generic arguments; the resolver
// decides from what X is, never from its spelling.
type IndexExpr struct {
	Base
	X    Expr
	Args []TypeOrExpr
}

// SliceExpr is X "[" [ Expr ] ".." [ Expr ] "]".
type SliceExpr struct {
	Base
	X  Expr
	Lo Expr // nil when absent
	Hi Expr // nil when absent
}

// CallExpr is Fun "(" [ Args ] ")".
type CallExpr struct {
	Base
	Fun  Expr
	Args []*Arg
}

// Arg is [ Name ":" ] Expr. A named argument is struct construction.
type Arg struct {
	Base
	Name  *Ident // nil when positional
	Value Expr
}

// ParenExpr is "(" Expr ")", for anything but a lone name, which is a
// RecordLit.
type ParenExpr struct {
	Base
	X Expr
}

// RecordLit is "(" [ RecField { "," RecField } [ "," ] ] ")".
type RecordLit struct {
	Base
	Fields []*RecField
}

// RecField is Name ":" Expr | Name. A bare Name has Value nil and means
// Name: Name.
type RecField struct {
	Base
	Name  *Ident
	Value Expr
}

// ArrayLit is "[" [ Expr { "," Expr } [ "," ] ] "]".
type ArrayLit struct {
	Base
	Elems []Expr
}

// RepeatLit is "[" Expr ";" Expr "]", the only place ";" appears.
type RepeatLit struct {
	Base
	Value Expr
	Count Expr
}

// FnLit is "fn" "(" [ Params ] ")" [ ":" Type ] Block.
type FnLit struct {
	Base
	Self   *SelfParam // a receiver is E0101 here, kept for the message
	Params []*Param
	Result Type
	Body   *Block
}

// QuoteExpr is "quote" "{" Tokens "}". Toks are the raw, bracket-balanced
// tokens between the braces; Splices are the $( Expr ) and $Name inside them,
// in order, each parsed as an expression.
type QuoteExpr struct {
	Base
	Toks    TokRange
	Splices []*Splice
}

// Splice is "$" "(" Expr ")" or "$" Name inside a quote. For $Name, X is the
// *Ident.
type Splice struct {
	Base
	X Expr
}
