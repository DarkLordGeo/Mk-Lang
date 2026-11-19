// Why: The parser and the compiler share one tree, and the tree must be cheap
// to build and free.
//
// What: Ast, Span, Expr, Stmt, ExprNode, StmtNode and FunDecl.
//
// How: The tree is kept in arena lists and linked by index: a child is the
// index of a node in `Ast.exprs` or `Ast.stmts`, and a run of children is a
// Span of `Ast.refs`.

/// A run of `len` entries of `Ast.refs` or `Ast.names` from `start`.
export struct Span {
    export start: int
    export len: int
}

/// An expression. Children are indices into `Ast.exprs`.
export enum Expr {
    Number(value: f64)
    Str(text: str)
    True
    False
    Nil
    Variable(name: str)
    Assign(name: str, value: int)
    Unary(op: Kind, right: int)
    Binary(op: Kind, left: int, right: int)
    Logical(op: Kind, left: int, right: int)
    Call(callee: int, args: Span)
    Array(items: Span)
    Index(target: int, index: int)
    SetIndex(target: int, index: int, value: int)
}

/// A statement. Children are indices into `Ast.stmts` and `Ast.exprs`.
export enum Stmt {
    Expression(expr: int)
    Print(expr: int)
    Var(name: str, init: ?int)
    Block(body: Span)
    If(cond: int, then: int, otherwise: ?int)
    While(cond: int, body: int)
    Function(decl: int)
    Return(value: ?int)
}

/// An expression and the line it starts on.
export struct ExprNode {
    export line: int
    export expr: Expr
}

/// A statement and the line it starts on.
export struct StmtNode {
    export line: int
    export stmt: Stmt
}

/// A function declaration: its parameters are a span of `Ast.names`, its body of `Ast.refs`.
export struct FunDecl {
    export name: str
    export line: int
    export params: Span
    export body: Span
}

/// The nodes of one parse. Every list lives in the parse's arena.
export struct Ast {
    export exprs: List[ExprNode]
    export stmts: List[StmtNode]
    export refs: List[int]
    export names: List[str]
    export funs: List[FunDecl]
}

/// An empty tree in `a`.
export Ast.New(a: &Arena): Ast {
    return Ast(exprs: List[ExprNode].New(a), stmts: List[StmtNode].New(a), refs: List[int].New(a), names: List[str].New(a), funs: List[FunDecl].New(a))
}

/// The index of the `i`th child of `span`.
export Ast.Ref(&self, span: Span, i: int): int {
    return self.refs[span.start + i]
}

/// The `i`th name of `span`.
export Ast.Name(&self, span: Span, i: int): str from self.names {
    return self.names[span.start + i]
}
