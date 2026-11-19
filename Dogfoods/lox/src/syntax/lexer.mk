// Why: The parser reads tokens, not characters.
//
// What: Lexer, a scanner over the source text (Crafting Interpreters, chapter
// 16).
//
// How: Tokens are views of the source, so scanning allocates nothing;
// characters past ASCII are decoded with Scalars.

/// Produces tokens on demand from a source text.
export view struct Lexer {
    src: str
    start: int
    pos: int
    line: int
}

/// A lexer at the start of `src`, counting lines from `line`.
export Lexer.New(src: str, line: int): Lexer from src {
    return Lexer(src: src, start: 0, pos: 0, line: line)
}

Lexer.AtEnd(&self): bool {
    return self.pos >= self.src.Len()
}

Lexer.Peek(&self): u8 {
    if self.AtEnd() {
        return 0
    }
    return self.src.Byte(self.pos)
}

Lexer.PeekNext(&self): u8 {
    if self.pos + 1 >= self.src.Len() {
        return 0
    }
    return self.src.Byte(self.pos + 1)
}

Lexer.Match(mut self, b: u8): bool {
    if self.Peek() != b {
        return false
    }
    self.pos += 1
    return true
}

Lexer.Make(&self, kind: Kind): Token from self.src {
    return Token(kind: kind, text: self.src[self.start..self.pos], line: self.line)
}

Lexer.Fail(&self, message: str): Token from message {
    return Token(kind: Kind.Error, text: message, line: self.line)
}

IsDigit(b: u8): bool {
    return b >= b'0' && b <= b'9'
}

IsAlpha(b: u8): bool {
    return b >= b'a' && b <= b'z' || b >= b'A' && b <= b'Z' || b == b'_'
}

/// The number of bytes UTF-8 uses for `c`.
Utf8Len(c: char): int {
    n := u32(c)
    return if n < 0x80 { 1 } else if n < 0x800 { 2 } else if n < 0x10000 { 3 } else { 4 }
}

Lexer.SkipSpace(mut self) {
    for !self.AtEnd() {
        b := self.Peek()
        if b == b' ' || b == b'\r' || b == b'\t' {
            self.pos += 1
        } else if b == b'\n' {
            self.line += 1
            self.pos += 1
        } else if b == b'/' && self.PeekNext() == b'/' {
            for !self.AtEnd() && self.Peek() != b'\n' {
                self.pos += 1
            }
        } else {
            return
        }
    }
}

Lexer.String(mut self): Token from self.src {
    // A string may hold any scalar, and may span lines; it has no escapes.
    for c in self.src[self.pos..].Scalars() {
        if c == '"' {
            break
        }
        if c == '\n' {
            self.line += 1
        }
        self.pos += Utf8Len(c)
    }
    if self.AtEnd() {
        return self.Fail("Unterminated string.")
    }
    self.pos += 1
    return self.Make(Kind.String)
}

Lexer.Number(mut self): Token from self.src {
    for IsDigit(self.Peek()) {
        self.pos += 1
    }
    if self.Peek() == b'.' && IsDigit(self.PeekNext()) {
        self.pos += 1
        for IsDigit(self.Peek()) {
            self.pos += 1
        }
    }
    return self.Make(Kind.Number)
}

Lexer.Identifier(mut self): Token from self.src {
    for IsAlpha(self.Peek()) || IsDigit(self.Peek()) {
        self.pos += 1
    }
    return self.Make(KeywordKind(self.src[self.start..self.pos]))
}

/// The next token; `Eof` at the end, forever after.
export Lexer.Next(mut self): Token from self.src {
    self.SkipSpace()
    self.start = self.pos
    if self.AtEnd() {
        return self.Make(Kind.Eof)
    }
    b := self.Peek()
    if IsAlpha(b) {
        return self.Identifier()
    }
    if IsDigit(b) {
        return self.Number()
    }
    if b >= 0x80 {
        // Step over the whole scalar so the error names one character.
        var it := self.src[self.pos..].Scalars()
        c := it.Next() else { '?' }
        self.pos += Utf8Len(c)
        return self.Fail("Unexpected character.")
    }
    self.pos += 1
    kind := match b {
        b'(' { Kind.LeftParen }
        b')' { Kind.RightParen }
        b'{' { Kind.LeftBrace }
        b'}' { Kind.RightBrace }
        b'[' { Kind.LeftBracket }
        b']' { Kind.RightBracket }
        b',' { Kind.Comma }
        b'.' { Kind.Dot }
        b'-' { Kind.Minus }
        b'+' { Kind.Plus }
        b';' { Kind.Semicolon }
        b'/' { Kind.Slash }
        b'*' { Kind.Star }
        b'%' { Kind.Percent }
        b'!' { if self.Match(b'=') { Kind.BangEqual } else { Kind.Bang } }
        b'=' { if self.Match(b'=') { Kind.EqualEqual } else { Kind.Equal } }
        b'<' { if self.Match(b'=') { Kind.LessEqual } else { Kind.Less } }
        b'>' { if self.Match(b'=') { Kind.GreaterEqual } else { Kind.Greater } }
        b'"' { return self.String() }
        _ { return self.Fail("Unexpected character.") }
    }
    return self.Make(kind)
}
