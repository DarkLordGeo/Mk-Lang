// Why: passes that visit a whole tree should not each repeat the knowledge
// of which fields of which node are children.
//
// What: Visitor, Walk, Inspect and EachChild for every node type of
// doc/grammar.md.
//
// How: EachChild is one type switch that calls f on each child in source
// order, skipping nil optional children, and ICEs on a node type it does not
// know; Walk and Inspect are built on it.

package ast

import "mk/internal/source"

// Visitor is go/ast's visitor: Visit is called for each node; when it
// returns a non-nil w, Walk visits the node's children with w, then calls
// w.Visit(nil).
type Visitor interface {
	Visit(n Node) (w Visitor)
}

// Walk traverses the tree rooted at n depth-first, children in source order.
func Walk(v Visitor, n Node) {
	if v = v.Visit(n); v == nil {
		return
	}
	EachChild(n, func(c Node) { Walk(v, c) })
	v.Visit(nil)
}

type inspector func(Node) bool

func (f inspector) Visit(n Node) Visitor {
	if f(n) {
		return f
	}
	return nil
}

// Inspect calls f(n) and, if it returns true, inspects each child in source
// order, then calls f(nil), as go/ast.Inspect does.
func Inspect(n Node, f func(Node) bool) { Walk(inspector(f), n) }

// EachChild calls f for every non-nil direct child of n, in source order.
// Tokens are not children; File's Items are. Any node type this function does
// not know is an ICE, so a new node type cannot be silently skipped.
func EachChild(n Node, f func(Node)) {
	switch n := n.(type) {
	case *File:
		for _, x := range n.Items {
			f(x)
		}
	case *Ident, *BasicLit, *BadItem, *BadType, *BadExpr, *BadStmt, *BadPattern,
		*WildcardPattern, *NonePattern:
	case *SelfParam:
		opt(f, n.Into)
	case *Path:
		for _, x := range n.Names {
			f(x)
		}
	case *Import:
		opt(f, n.Alias)
		f(n.Path)
	case *GenItem:
		f(n.Generator)
		for _, x := range n.Args {
			f(x)
		}
		optNode(f, n.Fuel)
	case *TestItem:
		f(n.Name)
		opt(f, n.Param)
		f(n.Body)
	case *Packed:
		opt(f, n.Align)
	case *Aligned:
		f(n.Align)
	case *StructDecl:
		// Packed and Aligned may come in either order (the grammar's Struct).
		if n.Align != nil && n.Packed != nil && n.Align.Sp.Start < n.Packed.Sp.Start {
			f(n.Align)
			f(n.Packed)
		} else {
			opt(f, n.Packed)
			opt(f, n.Align)
		}
		f(n.Name)
		opt(f, n.TypeParams)
		for _, x := range n.Members {
			f(x)
		}
	case *FieldMember:
		f(n.Name)
		f(n.Type)
	case *InvariantMember:
		f(n.X)
	case *EnumDecl:
		f(n.Name)
		opt(f, n.TypeParams)
		optNode(f, n.Tag)
		for _, x := range n.Variants {
			f(x)
		}
	case *Variant:
		f(n.Name)
		for _, x := range n.Fields {
			f(x)
		}
		optNode(f, n.Value)
	case *Field:
		f(n.Name)
		f(n.Type)
	case *ErrorDecl:
		f(n.Name)
		for _, x := range n.Fields {
			f(x)
		}
	case *InterfaceDecl:
		f(n.Name)
		opt(f, n.TypeParams)
		for _, x := range n.Methods {
			f(x)
		}
	case *ImplDecl:
		opt(f, n.TypeParams)
		f(n.Interface)
		f(n.For)
		for _, x := range n.Methods {
			f(x)
		}
	case *AliasDecl:
		f(n.Name)
		opt(f, n.TypeParams)
		f(n.Type)
	case *ConstDecl:
		f(n.Name)
		optNode(f, n.Type)
		f(n.Value)
		optNode(f, n.Fuel)
		opt(f, n.Section)
	case *GlobalDecl:
		f(n.Name)
		f(n.Type)
		optNode(f, n.Value)
		opt(f, n.Section)
	case *ExternDecl:
		f(n.Sig)
		opt(f, n.Body)
	case *FuncDecl:
		f(n.Sig)
		opt(f, n.Body)
	case *Signature:
		f(n.Name)
		opt(f, n.TypeParams)
		opt(f, n.Self)
		for _, x := range n.Params {
			f(x)
		}
		optNode(f, n.Result)
		for _, x := range n.Flows {
			f(x)
		}
		for _, x := range n.Contracts {
			f(x)
		}
		opt(f, n.Cpu)
		opt(f, n.Section)
	case *FuncName:
		opt(f, n.Owner)
		opt(f, n.OwnerArgs)
		f(n.Name)
	case *Param:
		f(n.Name)
		f(n.Type)
		opt(f, n.Into)
	case *FromFlow:
		for _, x := range n.Sources {
			f(x)
		}
		for _, x := range n.Fields {
			f(x)
		}
	case *FieldSrc:
		f(n.Name)
		for _, x := range n.Sources {
			f(x)
		}
	case *EndsFlow:
		f(n.Name)
	case *StoreFlow:
		f(n.From)
		f(n.Into)
	case *Source:
		for _, x := range n.Names {
			f(x)
		}
	case *Contract:
		f(n.X)
	case *TypeParams:
		for _, x := range n.Params {
			f(x)
		}
	case *TypeParam:
		f(n.Name)
		for _, x := range n.Bounds {
			f(x)
		}
		optNode(f, n.Type)
	case *TypeArgs:
		for _, x := range n.Args {
			f(x)
		}
	case *ErrorType:
		f(n.Errors)
		f(n.Ok)
	case *UnionType:
		for _, x := range n.Types {
			f(x)
		}
	case *RefType:
		f(n.Elem)
	case *MutType:
		f(n.Elem)
	case *MutSliceType:
		f(n.Elem)
	case *OptionalType:
		f(n.Elem)
	case *RawPtrType:
		f(n.Elem)
	case *SliceType:
		f(n.Elem)
	case *ArrayType:
		f(n.Len)
		f(n.Elem)
	case *FnType:
		for _, x := range n.Params {
			f(x)
		}
		optNode(f, n.Result)
		for _, x := range n.Flows {
			f(x)
		}
	case *FnParam:
		opt(f, n.Name)
		f(n.Type)
		opt(f, n.Into)
	case *RecordType:
		for _, x := range n.Fields {
			f(x)
		}
	case *ParenType:
		f(n.Type)
	case *PathType:
		f(n.Path)
		opt(f, n.Args)
	case *Block:
		for _, x := range n.Stmts {
			f(x)
		}
	case *BindStmt:
		f(n.Name)
		optNode(f, n.Type)
		optNode(f, n.Value)
	case *DestructureStmt:
		f(n.Pat)
		f(n.Value)
	case *DiscardStmt:
		f(n.Value)
	case *AssignStmt:
		f(n.Target)
		f(n.Value)
	case *IfStmt:
		f(n.Cond)
		f(n.Then)
		optNode(f, n.Else)
	case *BindCond:
		f(n.Name)
		f(n.Value)
	case *IsCond:
		f(n.Name)
		f(n.Type)
	case *ForStmt:
		opt(f, n.Label)
		optNode(f, n.Head)
		f(n.Body)
	case *InHead:
		opt(f, n.Key)
		opt(f, n.Value)
		opt(f, n.Pat)
		f(n.Source)
	case *RangeSource:
		f(n.Lo)
		f(n.Hi)
	case *RefSource:
		f(n.X)
	case *FieldsSource:
		f(n.X)
	case *MatchStmt:
		f(n.X)
		for _, x := range n.Arms {
			f(x)
		}
	case *Arm:
		f(n.Pat)
		f(n.Body)
	case *ReturnStmt:
		optNode(f, n.Value)
	case *FailStmt:
		f(n.Value)
	case *BecomeStmt:
		f(n.Call)
	case *BreakStmt:
		opt(f, n.Label)
	case *ContinueStmt:
		opt(f, n.Label)
	case *DeferStmt:
		opt(f, n.Body)
		optNode(f, n.X)
	case *ScopeStmt:
		f(n.Name)
		f(n.Value)
		f(n.Body)
	case *UnsafeStmt:
		f(n.Reason)
		f(n.Body)
	case *AsmStmt:
		f(n.Template)
		for _, x := range n.Operands {
			f(x)
		}
	case *AsmOperand:
		opt(f, n.Label)
		optNode(f, n.Reg)
		optNode(f, n.Value)
		opt(f, n.Sym)
		opt(f, n.ABI)
		for _, x := range n.Options {
			f(x)
		}
	case *ExprStmt:
		f(n.X)
	case *RecordPat:
		opt(f, n.Path)
		for _, x := range n.Fields {
			f(x)
		}
	case *PatField:
		f(n.Name)
		optNode(f, n.Pat)
	case *AltPattern:
		for _, x := range n.Alts {
			f(x)
		}
	case *VariantPattern:
		f(n.Name)
		for _, x := range n.Fields {
			f(x)
		}
	case *PathPattern:
		f(n.Path)
		for _, x := range n.Fields {
			f(x)
		}
	case *LiteralPattern:
		f(n.Lit)
	case *RangePattern:
		opt(f, n.Lo)
		opt(f, n.Hi)
	case *SomePattern:
		f(n.Pat)
	case *OkPattern:
		f(n.Pat)
	case *ErrPattern:
		f(n.Pat)
	case *NamePattern:
		f(n.Name)
	case *ElseExpr:
		f(n.X)
		opt(f, n.Name)
		f(n.Handler)
	case *BinaryExpr:
		f(n.X)
		f(n.Y)
	case *UnaryExpr:
		f(n.X)
	case *FieldExpr:
		f(n.X)
		f(n.Name)
	case *GoExpr:
		f(n.Scope)
		f(n.Call)
	case *IndexExpr:
		f(n.X)
		for _, x := range n.Args {
			f(x)
		}
	case *SliceExpr:
		f(n.X)
		optNode(f, n.Lo)
		optNode(f, n.Hi)
	case *CallExpr:
		f(n.Fun)
		for _, x := range n.Args {
			f(x)
		}
	case *Arg:
		opt(f, n.Name)
		f(n.Value)
	case *ParenExpr:
		f(n.X)
	case *RecordLit:
		for _, x := range n.Fields {
			f(x)
		}
	case *RecField:
		f(n.Name)
		optNode(f, n.Value)
	case *ArrayLit:
		for _, x := range n.Elems {
			f(x)
		}
	case *RepeatLit:
		f(n.Value)
		f(n.Count)
	case *FnLit:
		opt(f, n.Self)
		for _, x := range n.Params {
			f(x)
		}
		optNode(f, n.Result)
		f(n.Body)
	case *QuoteExpr:
		for _, x := range n.Splices {
			f(x)
		}
	case *Splice:
		f(n.X)
	default:
		var sp source.Span
		if n != nil {
			sp = n.Span()
		}
		source.ICEf(sp, "ast.EachChild: unknown node %T", n)
	}
}

// opt calls f on a pointer child when it is not nil.
func opt[T interface {
	*E
	Node
}, E any](f func(Node), c T) {
	if c != nil {
		f(c)
	}
}

// optNode calls f on an interface child when it is not nil.
func optNode(f func(Node), n Node) {
	if n != nil {
		f(n)
	}
}
