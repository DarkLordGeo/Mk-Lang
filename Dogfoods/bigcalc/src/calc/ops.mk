// Why: the operations must stay within the run's limits even when an input
// asks for a result of gigabytes.
//
// What: Apply, which dispatches every operation word, with Pow, ModPow,
// Factorial and Fibonacci.
//
// How: every result is allocated in the line arena, and each one whose size
// grows with its inputs is checked against the bit limit before it is
// computed.

import std.big

// The largest factorial and Fibonacci index fact and fib take.
const MaxIndex: int = 1_000_000

/// Apply runs the arithmetic or stack word tok. Fails with UnknownWord when
/// tok is none of them, leaving the stack unchanged.
Apply(a: &Arena into stack, stack: mut List[big.Int], tok: Token, max_bits: int): Error!void {
    c := tok.column
    match tok.text {
        "+" | "-" | "*" | "/" | "%" | "mod" | "divmod" | "gcd" | "^" | "<<" | ">>" | "cmp" | "eq" | "lt" | "min" | "max" | "inv" | "bit" {
            try Need(stack.Len(), c, 2)
            // Need checked the depth, so Pop is never none here
            y := stack.Pop().Must()
            x := stack.Pop().Must()
            try Binary(a, stack, tok, &x, &y, max_bits)
        }
        "modpow" {
            try Need(stack.Len(), c, 3)
            // Need checked the depth, so Pop is never none here
            m := stack.Pop().Must()
            e := stack.Pop().Must()
            x := stack.Pop().Must()
            try stack.Push(try ModPow(a, c, &x, &e, &m))
        }
        "neg" | "abs" | "sqrt" | "sign" | "bitlen" | "fact" | "fib" {
            try Need(stack.Len(), c, 1)
            // Need checked the depth, so Pop is never none here
            x := stack.Pop().Must()
            try Unary(a, stack, tok, &x, max_bits)
        }
        "dup" {
            try Need(stack.Len(), c, 1)
            try stack.Push(try stack[stack.Len() - 1].Clone(a))
        }
        "drop" {
            try Need(stack.Len(), c, 1)
            _ = stack.Pop()
        }
        "swap" {
            try Need(stack.Len(), c, 2)
            stack.Swap(stack.Len() - 1, stack.Len() - 2)
        }
        "over" {
            try Need(stack.Len(), c, 2)
            try stack.Push(try stack[stack.Len() - 2].Clone(a))
        }
        "rot" {
            // a b c -> b c a
            try Need(stack.Len(), c, 3)
            x := try stack[stack.Len() - 3].Clone(a)
            _ = stack.Remove(stack.Len() - 3)
            try stack.Push(x)
        }
        "clear" {
            for stack.Len() > 0 {
                _ = stack.Pop()
            }
        }
        "depth" {
            try stack.Push(try big.FromI64(a, Cast[i64](stack.Len())))
        }
        "sort" {
            stack.Sort(fn(x: &big.Int, y: &big.Int): bool { return x.Less(y) })
        }
        "uniq" {
            var seen := Set[big.Int].New(a)
            var n: i64 = 0
            for i in 0..stack.Len() {
                if try seen.Add(try stack[i].Clone(a)) {
                    n += 1
                }
            }
            try stack.Push(try big.FromI64(a, n))
        }
        _ {
            fail UnknownWord(column: c)
        }
    }
}

// The two-operand words, x below y on the stack.
Binary(a: &Arena into stack, stack: mut List[big.Int], tok: Token, x: &big.Int, y: &big.Int, max_bits: int): Error!void {
    c := tok.column
    match tok.text {
        "+" {
            try stack.Push(try x.Add(a, y))
        }
        "-" {
            try stack.Push(try x.Sub(a, y))
        }
        "*" {
            try Fits(c, x.BitLen() + y.BitLen(), max_bits)
            try stack.Push(try x.Mul(a, y))
        }
        "/" | "%" | "divmod" {
            try NonZero(c, y)
            (q, r) := try x.DivMod(a, y)
            if tok.text != "%" {
                try stack.Push(q)
            }
            if tok.text != "/" {
                try stack.Push(r)
            }
        }
        "mod" {
            try NonZero(c, y)
            try stack.Push(try x.Mod(a, y))
        }
        "gcd" {
            try stack.Push(try x.Gcd(a, y))
        }
        "inv" {
            try NonZero(c, y)
            r := try x.ModInverse(a, y) else {
                fail NoInverse(column: c)
            }
            try stack.Push(r)
        }
        "^" {
            try stack.Push(try Pow(a, c, x, y, max_bits))
        }
        "<<" {
            n := try Count(y, c, max_bits)
            try Fits(c, x.BitLen() + n, max_bits)
            try stack.Push(try x.Shl(a, n))
        }
        ">>" {
            n := try Count(y, c, max_bits)
            try stack.Push(try x.Shr(a, n))
        }
        "bit" {
            n := try Count(y, c, max_bits)
            try stack.Push(try big.FromI64(a, if x.Bit(n) { 1 } else { 0 }))
        }
        "cmp" {
            try stack.Push(try big.FromI64(a, Cast[i64](x.Cmp(y))))
        }
        "eq" {
            try stack.Push(try big.FromI64(a, if x.Eq(y) { 1 } else { 0 }))
        }
        "lt" {
            try stack.Push(try big.FromI64(a, if x.Less(y) { 1 } else { 0 }))
        }
        "min" {
            low := if y.Less(x) { y } else { x }
            try stack.Push(try low.Clone(a))
        }
        _ {
            // max
            high := if x.Less(y) { y } else { x }
            try stack.Push(try high.Clone(a))
        }
    }
}

// The one-operand words.
Unary(a: &Arena into stack, stack: mut List[big.Int], tok: Token, x: &big.Int, max_bits: int): Error!void {
    c := tok.column
    match tok.text {
        "neg" {
            try stack.Push(try x.Neg(a))
        }
        "abs" {
            if x.Sign() < 0 {
                try stack.Push(try x.Neg(a))
            } else {
                try stack.Push(try x.Clone(a))
            }
        }
        "sqrt" {
            if x.Sign() < 0 {
                fail NegativeSquareRoot(column: c)
            }
            try stack.Push(try x.Sqrt(a))
        }
        "sign" {
            try stack.Push(try big.FromI64(a, Cast[i64](x.Sign())))
        }
        "bitlen" {
            try stack.Push(try big.FromI64(a, Cast[i64](x.BitLen())))
        }
        "fact" {
            n := try Count(x, c, MaxIndex)
            try stack.Push(try Factorial(a, c, n, max_bits))
        }
        _ {
            // fib
            n := try Count(x, c, MaxIndex)
            try stack.Push(try Fibonacci(a, c, n, max_bits))
        }
    }
}

/// Fits fails with ResultTooLarge when a result of bits bits passes limit.
Fits(column: int, bits: int, limit: int): ResultTooLarge!void {
    if bits > limit {
        fail ResultTooLarge(column: column, bits: bits, limit: limit)
    }
}

NonZero(column: int, y: &big.Int): DivisionByZero!void {
    if y.Sign() == 0 {
        fail DivisionByZero(column: column)
    }
}

/// Pow is x^e by squaring, e from 0 to the bit limit; the result's length is
/// bounded by BitLen(x) * e before any product is formed.
Pow(a: &Arena, column: int, x: &big.Int, e: &big.Int, max_bits: int): Error!big.Int from a {
    n := try Count(e, column, max_bits)
    if x.BitLen() > 1 {
        try Fits(column, (x.BitLen() - 1) * n + 1, max_bits)
    }
    var result := try big.FromI64(a, 1)
    var base := try x.Clone(a)
    var k := n
    for k > 0 {
        if k % 2 == 1 {
            result = try result.Mul(a, &base)
        }
        k = k / 2
        if k > 0 {
            base = try base.Mul(a, &base)
        }
    }
    return result
}

/// ModPow is x^e mod |m|; a negative e takes the inverse of x first.
ModPow(a: &Arena, column: int, x: &big.Int, e: &big.Int, m: &big.Int): Error!big.Int from a {
    try NonZero(column, m)
    if e.Sign() >= 0 {
        return try x.ModPow(a, e, m)
    }
    inv := try x.ModInverse(a, m) else {
        fail NoInverse(column: column)
    }
    pos := try e.Neg(a)
    return try inv.ModPow(a, &pos, m)
}

/// Factorial is n!, checked against the bit limit as it grows.
Factorial(a: &Arena, column: int, n: int, max_bits: int): Error!big.Int from a {
    var result := try big.FromI64(a, 1)
    for i in 2..n + 1 {
        k := try big.FromI64(a, Cast[i64](i))
        try Fits(column, result.BitLen() + k.BitLen(), max_bits)
        result = try result.Mul(a, &k)
    }
    return result
}

/// Fibonacci is F(n), F(0) = 0, by repeated addition.
Fibonacci(a: &Arena, column: int, n: int, max_bits: int): Error!big.Int from a {
    var x := try big.FromI64(a, 0)
    var y := try big.FromI64(a, 1)
    for i in 0..n {
        _ = i
        try Fits(column, y.BitLen() + 1, max_bits)
        next := try x.Add(a, &y)
        x = y
        y = next
    }
    return x
}
