// Why: the RPN language: words split on whitespace, numbers pushed,
// operations applied to the top of the stack.
//
// What: Tokens, Machine.Line and Machine.Run, number parsing (bases, bytes:
// and b64: literals) and the `rep` repeat word.
//
// How: a line runs against a copy of the kept stack in the line's arena and
// is kept only when the whole line succeeds, so a failed line changes
// nothing.

import std.big
import std.encoding

// A word of a line and the column it starts at, counting from 1.
struct Token {
    text: str
    column: int
}

// Splits a line into words; `#` at the start of a word comments out the rest.
struct Tokens {
    src: str
    pos: int
    // the column of src[0] in its line, less one
    offset: int
}

IsSpace(b: u8): bool {
    return b == b' ' || b == b'\t' || b == b'\r' || b == b'\n'
}

Tokens.Next(mut self): ?Token {
    for self.pos < self.src.Len() && IsSpace(self.src.Byte(self.pos)) {
        self.pos += 1
    }
    if self.pos == self.src.Len() || self.src.Byte(self.pos) == b'#' {
        self.pos = self.src.Len()
        return none
    }
    start := self.pos
    for self.pos < self.src.Len() && !IsSpace(self.src.Byte(self.pos)) {
        self.pos += 1
    }
    return some(Token(text: self.src[start..self.pos], column: self.offset + start + 1))
}

/// Line evaluates one line of RPN against the kept stack and keeps the
/// result. On an error the kept stack is as it was before the line; a
/// variable stored before the failing word stays stored. Allocates the
/// line's values in arena, which the caller may reset after.
export Machine.Line(mut self, a: &Alloc into self, arena: &Arena, io: Io, out: mut Writer, line: str): Error!void {
    var stack := try self.Load(arena)
    try self.Run(a, arena, io, out, mut stack, line, 0, 0)
    try self.Keep(a, &stack)
}

// How many `rep` words one line may nest; each nests a call of Run.
const MaxRepeatDepth: int = 16

// Runs the words of src in order. `rep` repeats the rest of src, at most
// MaxRepeatDepth deep.
Machine.Run(mut self, a: &Alloc into self, arena: &Arena into stack, io: Io, out: mut Writer, stack: mut List[big.Int], src: str, depth: int, offset: int): Error!void {
    var words := Tokens(src: src, pos: 0, offset: offset)
    for tok := words.Next() {
        if tok.text == "rep" {
            if depth == MaxRepeatDepth {
                fail CountOutOfRange(column: tok.column)
            }
            try Need(stack.Len(), tok.column, 1)
            // Need checked the depth, so Pop is never none here
            n := try Count(&stack.Pop().Must(), tok.column, 1_000_000_000)
            rest := src[words.pos..]
            for i in 0..n {
                _ = i
                // a repeat is where a long script can be stopped
                try io.Yield()
                try self.Run(a, arena, io, out, stack, rest, depth + 1, offset + words.pos)
            }
            return
        }
        if tok.text.StartsWith("=") || tok.text.StartsWith("$") {
            try self.Variable(a, arena, stack, tok)
            continue
        }
        if IsNumber(tok.text) {
            try stack.Push(try ParseNumber(arena, tok, self.settings.max_digits))
            continue
        }
        if try self.Show(arena, out, stack, tok) {
            continue
        }
        Apply(arena, stack, tok, self.settings.max_bits) else e {
            match e {
                UnknownWord {
                    try self.Setting(stack, tok)
                }
                other {
                    fail other
                }
            }
        }
    }
}

// =name pops into a variable, $name pushes a copy of one.
Machine.Variable(mut self, a: &Alloc into self, arena: &Arena into stack, stack: mut List[big.Int], tok: Token): Error!void {
    name := tok.text[1..]
    if name.Len() == 0 {
        fail MissingName(column: tok.column)
    }
    if tok.text.StartsWith("=") {
        try Need(stack.Len(), tok.column, 1)
        // Need checked the depth, so Pop is never none here
        v := stack.Pop().Must()
        try self.Store(a, name, &v)
        return
    }
    v := try self.Recall(arena, name) else {
        fail NoSuchVariable(column: tok.column)
    }
    try stack.Push(v)
}

// The words that change the output base. Anything else is unknown.
Machine.Setting(mut self, stack: mut List[big.Int], tok: Token): Error!void {
    match tok.text {
        "hex" {
            self.settings.base = 16
        }
        "dec" {
            self.settings.base = 10
        }
        "oct" {
            self.settings.base = 8
        }
        "bin" {
            self.settings.base = 2
        }
        "base" {
            try Need(stack.Len(), tok.column, 1)
            // Need checked the depth, so Pop is never none here
            b := try Count(&stack.Pop().Must(), tok.column, 36)
            if b < 2 {
                fail CountOutOfRange(column: tok.column)
            }
            self.settings.base = b
        }
        _ {
            fail UnknownWord(column: tok.column)
        }
    }
}

/// Need fails with StackUnderflow unless have, the stack's depth, is at least n.
Need(have: int, column: int, n: int): StackUnderflow!void {
    if have < n {
        fail StackUnderflow(column: column, need: n, have: have)
    }
}

/// Count is v as an int from 0 to limit, or CountOutOfRange.
Count(v: &big.Int, column: int, limit: int): CountOutOfRange!int {
    n := v.ToI64() else {
        fail CountOutOfRange(column: column)
    }
    if n < 0 || n > Cast[i64](limit) {
        fail CountOutOfRange(column: column)
    }
    return Cast[int](n)
}

// A number starts with a digit, after an optional sign.
IsNumber(text: str): bool {
    var i := 0
    if text.StartsWith("-") || text.StartsWith("+") {
        i = 1
    }
    if text.StartsWith("bytes:") || text.StartsWith("b64:") {
        return true
    }
    return i < text.Len() && text.Byte(i) >= b'0' && text.Byte(i) <= b'9'
}

/// ParseNumber reads a literal: decimal, 0x, 0o or 0b with an optional sign,
/// base#digits for a base from 2 to 36, bytes:HEX or b64:BASE64 for a
/// non-negative value from big-endian bytes. Fails with TooManyDigits past
/// max_digits digits and BadNumber for anything else. Allocates the value
/// in a.
ParseNumber(a: &Arena, tok: Token, max_digits: int): BadNumber|TooManyDigits|OutOfMemory!big.Int from a {
    if tok.text.StartsWith("bytes:") || tok.text.StartsWith("b64:") {
        return try ParseBytes(a, tok, max_digits)
    }
    var body := tok.text
    var neg := false
    if body.StartsWith("-") || body.StartsWith("+") {
        neg = body.Byte(0) == b'-'
        body = body[1..]
    }
    var base := 10
    if body.StartsWith("0x") || body.StartsWith("0X") {
        base = 16
        body = body[2..]
    } else if body.StartsWith("0o") || body.StartsWith("0O") {
        base = 8
        body = body[2..]
    } else if body.StartsWith("0b") || body.StartsWith("0B") {
        base = 2
        body = body[2..]
    } else if hash := body.Find("#") {
        b := body[..hash].ParseInt() else {
            fail BadNumber(column: tok.column)
        }
        if b < 2 || b > 36 {
            fail BadNumber(column: tok.column)
        }
        base = Cast[int](b)
        body = body[hash + 1..]
    }
    // big.Parse takes a sign of its own; a second one is not a number here
    if body.Len() == 0 || body.StartsWith("-") || body.StartsWith("+") {
        fail BadNumber(column: tok.column)
    }
    parsed: big.Error!big.Int = big.Parse(a, body, base, max_digits)
    v := parsed else e {
        match e {
            big.TooLarge {
                fail TooManyDigits(column: tok.column, limit: max_digits)
            }
            OutOfMemory {
                fail OutOfMemory
            }
            _ {
                fail BadNumber(column: tok.column)
            }
        }
    }
    if neg {
        return try v.Neg(a)
    }
    return v
}

// bytes:HEX and b64:BASE64, big-endian and non-negative.
ParseBytes(a: &Arena, tok: Token, max_digits: int): BadNumber|TooManyDigits|OutOfMemory!big.Int from a {
    hex := tok.text.StartsWith("bytes:")
    body := if hex { tok.text[6..] } else { tok.text[4..] }
    if body.Len() > max_digits {
        fail TooManyDigits(column: tok.column, limit: max_digits)
    }
    raw := if hex {
        encoding.HexDecode(a, body) else e {
            match e {
                OutOfMemory {
                    fail OutOfMemory
                }
                _ {
                    fail BadNumber(column: tok.column)
                }
            }
        }
    } else {
        encoding.Base64Decode(a, body, encoding.Base64) else e {
            match e {
                OutOfMemory {
                    fail OutOfMemory
                }
                _ {
                    fail BadNumber(column: tok.column)
                }
            }
        }
    }
    return try big.FromBytes(a, raw)
}
