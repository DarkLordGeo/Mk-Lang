// Why: Plotting evaluates one expression per pixel column, so it must be
// compiled once and evaluated cheaply.
//
// What: Plot expressions in one variable `x`: numbers, the constants and
// functions of funcs.mk, `+ - * /`, `^` for pow (right-associative, above unary
// minus, so -x^2 is -(x^2)), and parentheses.
//
// How: An expression compiles to nodes in post-order, so it evaluates by one
// pass over them with no recursion, in f64 or in f32.

import std.math

/// The expression has a syntax error at this byte offset.
export error Syntax(at: int)
/// The name at this offset is no function, constant or `x`.
export error UnknownName(at: int)
/// The call at this offset has the wrong number of arguments.
export error Arity(at: int)
/// The expression nests deeper than 64 levels or has more than 4096 nodes.
export error TooComplex(at: int)

/// What parsing can fail with.
export type Error = Syntax | UnknownName | Arity | TooComplex | OutOfMemory

const MaxDepth: int = 64
const MaxNodes: int = 4096

enum Op {
    Add
    Sub
    Mul
    Div
    Pow
}

// A node's operands are indices of earlier nodes.
enum Node {
    Num(value: f64)
    X
    Neg(a: int)
    Arith(op: Op, a: int, b: int)
    Call1(f: int, a: int)
    Call2(f: int, a: int, b: int)
    Test(f: int, a: int)
    Fma(a: int, b: int, c: int)
    LdExp(a: int, b: int)
    FrExpFrac(a: int)
    FrExpExp(a: int)
}

/// A compiled expression.
export view struct Expr {
    nodes: List[Node]
}

struct Parser {
    src: str
    pos: int
    depth: int
    nodes: List[Node]
}

/// Compiles `src`. Fails with the first error and its byte offset. O(n);
/// allocates the nodes in `a`. The result names `src` too: the parser holds
/// `src` and the nodes in one struct, whose one loan set the nodes keep.
export Parse(a: &Arena, src: str): Error!Expr from a, src {
    var p := Parser(src: src, pos: 0, depth: 0, nodes: List[Node].New(a))
    _ = try p.Sum()
    p.Space()
    if p.pos < src.Len() {
        fail Syntax(at: p.pos)
    }
    Parser(nodes, ..) := p
    return Expr(nodes: nodes)
}

Parser.Space(mut self) {
    for self.pos < self.src.Len() && (self.src.Byte(self.pos) == b' ' || self.src.Byte(self.pos) == b'\t') {
        self.pos += 1
    }
}

Parser.Peek(mut self): u8 {
    self.Space()
    return if self.pos < self.src.Len() { self.src.Byte(self.pos) } else { 0 }
}

Parser.Add(mut self, n: Node): Error!int {
    if self.nodes.Len() == MaxNodes {
        fail TooComplex(at: self.pos)
    }
    try self.nodes.Push(n)
    return self.nodes.Len() - 1
}

Parser.Sum(mut self): Error!int {
    var left := try self.Product()
    for {
        op := match self.Peek() {
            b'+' { Op.Add }
            b'-' { Op.Sub }
            _ { return left }
        }
        self.pos += 1
        right := try self.Product()
        left = try self.Add(Node.Arith(op: op, a: left, b: right))
    }
}

Parser.Product(mut self): Error!int {
    var left := try self.Unary()
    for {
        op := match self.Peek() {
            b'*' { Op.Mul }
            b'/' { Op.Div }
            _ { return left }
        }
        self.pos += 1
        right := try self.Unary()
        left = try self.Add(Node.Arith(op: op, a: left, b: right))
    }
}

Parser.Unary(mut self): Error!int {
    if self.Peek() != b'-' {
        return try self.Power()
    }
    self.pos += 1
    try self.Enter()
    a := try self.Unary()
    self.depth -= 1
    return try self.Add(Node.Neg(a: a))
}

Parser.Enter(mut self): TooComplex!void {
    self.depth += 1
    if self.depth > MaxDepth {
        fail TooComplex(at: self.pos)
    }
}

Parser.Power(mut self): Error!int {
    base := try self.Atom()
    if self.Peek() != b'^' {
        return base
    }
    self.pos += 1
    try self.Enter()
    exp := try self.Unary()
    self.depth -= 1
    return try self.Add(Node.Arith(op: Op.Pow, a: base, b: exp))
}

IsDigit(b: u8): bool {
    return b >= b'0' && b <= b'9'
}

IsLetter(b: u8): bool {
    return (b >= b'a' && b <= b'z') || (b >= b'A' && b <= b'Z') || b == b'_'
}

Parser.Number(mut self): Error!int {
    start := self.pos
    n := self.src.Len()
    for self.pos < n && (IsDigit(self.src.Byte(self.pos)) || self.src.Byte(self.pos) == b'.') {
        self.pos += 1
    }
    // An exponent is `e` or `E`, an optional sign, then a digit; anything
    // else after the digits is left for the next token.
    if self.pos < n && (self.src.Byte(self.pos) == b'e' || self.src.Byte(self.pos) == b'E') {
        var k := self.pos + 1
        if k < n && (self.src.Byte(k) == b'+' || self.src.Byte(k) == b'-') {
            k += 1
        }
        if k < n && IsDigit(self.src.Byte(k)) {
            self.pos = k
            for self.pos < n && IsDigit(self.src.Byte(self.pos)) {
                self.pos += 1
            }
        }
    }
    v := self.src[start..self.pos].ParseFloat() else { fail Syntax(at: start) }
    return try self.Add(Node.Num(value: v))
}

Parser.Atom(mut self): Error!int {
    b := self.Peek()
    if IsDigit(b) || b == b'.' {
        return try self.Number()
    }
    if b == b'(' {
        self.pos += 1
        try self.Enter()
        inner := try self.Sum()
        self.depth -= 1
        if self.Peek() != b')' {
            fail Syntax(at: self.pos)
        }
        self.pos += 1
        return inner
    }
    if !IsLetter(b) {
        fail Syntax(at: self.pos)
    }
    start := self.pos
    for self.pos < self.src.Len() && (IsLetter(self.src.Byte(self.pos)) || IsDigit(self.src.Byte(self.pos))) {
        self.pos += 1
    }
    name := self.src[start..self.pos]
    if name == "x" {
        return try self.Add(Node.X)
    }
    callee := Find(name)
    match callee {
        .None {
            fail UnknownName(at: start)
        }
        .Constant(index) {
            return try self.Add(Node.Num(value: Constants[index].value))
        }
        _ {
        }
    }
    var args: [3]int = [0; 3]
    count := try self.Arguments(mut args)
    want := match callee {
        .Binary | .LdExp { 2 }
        .Fma { 3 }
        _ { 1 }
    }
    if count != want {
        fail Arity(at: start)
    }
    node := match callee {
        .Unary(index) { Node.Call1(f: index, a: args[0]) }
        .Binary(index) { Node.Call2(f: index, a: args[0], b: args[1]) }
        .Predicate(index) { Node.Test(f: index, a: args[0]) }
        .Fma { Node.Fma(a: args[0], b: args[1], c: args[2]) }
        .LdExp { Node.LdExp(a: args[0], b: args[1]) }
        .FrExpFrac { Node.FrExpFrac(a: args[0]) }
        _ { Node.FrExpExp(a: args[0]) }
    }
    return try self.Add(node)
}

// Parses `( e, e, ... )`, at most three arguments, into `args`.
Parser.Arguments(mut self, args: mut [3]int): Error!int {
    if self.Peek() != b'(' {
        fail Syntax(at: self.pos)
    }
    self.pos += 1
    try self.Enter()
    var count := 0
    for {
        a := try self.Sum()
        if count == 3 {
            fail Arity(at: self.pos)
        }
        args[count] = a
        count += 1
        c := self.Peek()
        self.pos += 1
        if c == b')' {
            self.depth -= 1
            return count
        }
        if c != b',' {
            fail Syntax(at: self.pos - 1)
        }
    }
}

/// The number of value slots `Eval` and `EvalF32` need. O(1).
export Expr.Slots(&self): int {
    return self.nodes.Len()
}

// An exponent for LdExp from a computed value: truncated toward zero and
// clamped, so a huge or non-finite value still gives a defined scale.
Exponent(v: f64): int {
    if math.IsNan(v) {
        return 0
    }
    return Cast[int](math.RoundToZero(math.Max(-4096.0, math.Min(4096.0, v))))
}

/// Evaluates the expression at `x` in f64; `vals` holds `Slots()` values.
/// O(nodes); allocates nothing.
export Expr.Eval(&self, x: f64, vals: mut []f64): f64 {
    for i, n in &self.nodes {
        vals[i] = match *n {
            .Num(value) { value }
            .X { x }
            .Neg(a) { -vals[a] }
            .Arith(op, a, b) {
                match op {
                    .Add { vals[a] + vals[b] }
                    .Sub { vals[a] - vals[b] }
                    .Mul { vals[a] * vals[b] }
                    .Div { vals[a] / vals[b] }
                    .Pow { math.Pow(vals[a], vals[b]) }
                }
            }
            .Call1(f, a) { Unaries[f].f(vals[a]) }
            .Call2(f, a, b) { Binaries[f].f(vals[a], vals[b]) }
            .Test(f, a) {
                if Predicates[f].f(vals[a]) { 1.0 } else { 0.0 }
            }
            .Fma(a, b, c) { math.Fma(vals[a], vals[b], vals[c]) }
            .LdExp(a, b) { math.LdExp(vals[a], Exponent(vals[b])) }
            .FrExpFrac(a) { math.FrExp(vals[a]).frac }
            .FrExpExp(a) { Cast[f64](math.FrExp(vals[a]).exp) }
        }
    }
    return vals[self.nodes.Len() - 1]
}

/// `v` rounded to f32, where a finite value past f32's range becomes an
/// infinity rather than the panic of `Cast`.
export ToF32(v: f64): f32 {
    largest := Cast[f64](f32.FromBits(0x7F7FFFFF))
    if v > largest {
        return Cast[f32](math.Inf)
    }
    if v < -largest {
        return Cast[f32](-math.Inf)
    }
    return Cast[f32](v)
}

/// Evaluates the expression at `x` in f32, every operation and function in
/// f32; `vals` holds `Slots()` values. O(nodes); allocates nothing.
export Expr.EvalF32(&self, x: f32, vals: mut []f32): f32 {
    for i, n in &self.nodes {
        vals[i] = match *n {
            .Num(value) { ToF32(value) }
            .X { x }
            .Neg(a) { -vals[a] }
            .Arith(op, a, b) {
                match op {
                    .Add { vals[a] + vals[b] }
                    .Sub { vals[a] - vals[b] }
                    .Mul { vals[a] * vals[b] }
                    .Div { vals[a] / vals[b] }
                    .Pow { math.PowF32(vals[a], vals[b]) }
                }
            }
            .Call1(f, a) { Unaries[f].g(vals[a]) }
            .Call2(f, a, b) { Binaries[f].g(vals[a], vals[b]) }
            .Test(f, a) {
                if Predicates[f].g(vals[a]) { 1.0 } else { 0.0 }
            }
            .Fma(a, b, c) { math.FmaF32(vals[a], vals[b], vals[c]) }
            .LdExp(a, b) { math.LdExpF32(vals[a], Exponent(Cast[f64](vals[b]))) }
            .FrExpFrac(a) { math.FrExpF32(vals[a]).frac }
            .FrExpExp(a) { Cast[f32](math.FrExpF32(vals[a]).exp) }
        }
    }
    return vals[self.nodes.Len() - 1]
}

test "precedence: unary minus below ^, ^ right-associative" (sys: &System) {
    e := try Parse(sys.arena, "-2^3^2 + 1")
    vals := try sys.arena.Slice[f64](e.Slots())
    Assert(e.Eval(0.0, vals) == -511.0)
}

test "a name that is neither x nor a function is UnknownName at its offset" (sys: &System) {
    _ = Parse(sys.arena, "sin(x) + y") else e {
        match e {
            UnknownName(at) {
                Assert(at == 9)
                return
            }
            _ {
                Assert(false)
            }
        }
    }
    Assert(false)
}

test "fma, ldexp and frexp evaluate in both widths" (sys: &System) {
    e := try Parse(sys.arena, "fma(x, 2, 1) + ldexp(1, 3) + frexp(8) + frexpexp(8)")
    vals := try sys.arena.Slice[f64](e.Slots())
    Assert(e.Eval(1.0, vals) == 3.0 + 8.0 + 0.5 + 4.0)
    vals32 := try sys.arena.Slice[f32](e.Slots())
    Assert(e.EvalF32(1.0, vals32) == 15.5)
}
