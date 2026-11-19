// Why: a tail call (`become`) and a range pattern are each one node with its
// own invariants; the two sit in a small file of their own.
//
// What: BecomeStmt (the grammar's Become production) and RangePattern (the
// range alternative of Alt).
//
// How: plain node structs; the parser in internal/syntax makes them and
// guarantees the invariants their doc comments state.

package ast

// BecomeStmt is "become" Postfix, whose Postfix ends in a call: a tail call
// that replaces the caller's frame. The parser gives any other operand E0101,
// so Call is never nil.
type BecomeStmt struct {
	Base
	Call *CallExpr
}

// RangePattern is a half-open range of integer or char literals, lo <= x < hi:
// Lo ".." Hi, either end absent but not both. Lo and Hi are LiteralPatterns,
// so a negative integer end is one with Neg set; nil marks an open end, which
// reaches the scrutinee type's limit.
type RangePattern struct {
	Base
	Lo *LiteralPattern
	Hi *LiteralPattern
}
