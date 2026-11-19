// Why: inline assembly is a statement with its own small operand language;
// its nodes are kept apart from the ordinary statements.
//
// What: AsmStmt, AsmOperand and AsmOperandKind, the grammar's Asm,
// AsmOperand and AsmDir productions (doc/grammar.md).
//
// How: the template is kept as the string literal written, and check
// resolves its holes later; an operand's Kind says which of its fields are
// set (a register or constraint and an expression, a path, or names).

package ast

import "strconv"

// AsmStmt is "asm" "(" String { "," AsmOperand } [ "," ] ")". It is a
// statement, never an expression; check allows it only inside an
// UnsafeStmt's block (E1101). Template is the string literal as written (a
// raw or multi-line string allowed); resolving its holes is check's.
type AsmStmt struct {
	Base
	Template *BasicLit
	Operands []*AsmOperand
}

// AsmOperandKind tags an AsmOperand. The five directions are the grammar's
// AsmDir.
type AsmOperandKind uint8

const (
	// AsmIn .. AsmInLateOut are [ Name ":" ] AsmDir "(" ( Name | String ) ")"
	// ( Expr | "_" ).
	AsmIn AsmOperandKind = iota + 1
	AsmOut
	AsmLateOut
	AsmInOut
	AsmInLateOut
	// AsmConst is [ Name ":" ] "const" Expr.
	AsmConst
	// AsmSym is [ Name ":" ] "sym" Path.
	AsmSym
	// AsmClobberABI is "clobber_abi" "(" String ")".
	AsmClobberABI
	// AsmOptions is "options" "(" Name { "," Name } ")".
	AsmOptions
)

var asmOperandWords = [...]string{"", "in", "out", "lateout", "inout", "inlateout", "const", "sym", "clobber_abi", "options"}

// String is the operand's leading word: in, out, ..., clobber_abi, options.
func (k AsmOperandKind) String() string {
	if k != 0 && int(k) < len(asmOperandWords) {
		return asmOperandWords[k]
	}
	return "asmoperand(" + strconv.Itoa(int(k)) + ")"
}

// IsDirection reports whether k is one of AsmIn .. AsmInLateOut.
func (k AsmOperandKind) IsDirection() bool { return k >= AsmIn && k <= AsmInLateOut }

// AsmOperand is one operand of an AsmStmt. Which fields are set depends on
// Kind:
//
//   - a direction (AsmIn .. AsmInLateOut): Reg is an *Ident register class
//     or a *BasicLit string naming a register; Value is the in value or the
//     out place, nil with Discard set for "_";
//   - AsmConst: Value is the constant expression;
//   - AsmSym: Sym is the path, and Value is nil (a Path is no Expr);
//   - AsmClobberABI: ABI is the string;
//   - AsmOptions: Options are the names, in source order.
//
// Label is the "name:" of the first three forms, nil when absent. Every
// operand word but in and const is read by its position.
type AsmOperand struct {
	Base
	Kind    AsmOperandKind
	Label   *Ident
	Reg     Node
	Value   Expr
	Discard bool
	Sym     *Path
	ABI     *BasicLit
	Options []*Ident
}
