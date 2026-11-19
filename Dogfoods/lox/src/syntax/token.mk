// Why: The lexer and the parser share one token vocabulary.
//
// What: Kind, Token and IsKeyword: the tokens of the Lox grammar (Crafting
// Interpreters, chapter 4).
//
// How: A token is its kind, a view of its text and its line.

/// The kind of a token.
export enum Kind {
    LeftParen
    RightParen
    LeftBrace
    RightBrace
    LeftBracket
    RightBracket
    Comma
    Dot
    Minus
    Plus
    Semicolon
    Slash
    Star
    Percent
    Bang
    BangEqual
    Equal
    EqualEqual
    Greater
    GreaterEqual
    Less
    LessEqual
    Identifier
    String
    Number
    And
    Else
    False
    For
    Fun
    If
    Nil
    Or
    Print
    Return
    True
    Var
    While
    Error
    Eof
}

/// A token: its kind, its text as a view of the source, and its line.
/// An `Error` token's text is the message instead.
export struct Token {
    export kind: Kind
    export text: str
    export line: int
}

const Keywords: []str = ["and", "else", "false", "for", "fun", "if", "nil", "or", "print", "return", "true", "var", "while"]

/// The keyword kind of an identifier's text, or `Identifier`. O(1) per keyword.
KeywordKind(text: str): Kind {
    return match text {
        "and" { Kind.And }
        "else" { Kind.Else }
        "false" { Kind.False }
        "for" { Kind.For }
        "fun" { Kind.Fun }
        "if" { Kind.If }
        "nil" { Kind.Nil }
        "or" { Kind.Or }
        "print" { Kind.Print }
        "return" { Kind.Return }
        "true" { Kind.True }
        "var" { Kind.Var }
        "while" { Kind.While }
        _ { Kind.Identifier }
    }
}

/// Reports whether `text` is a reserved word, by binary search of the sorted keyword table.
export IsKeyword(text: str): bool {
    i := Keywords.Search(&text, fn(k: &str, t: &str): bool { return *k < *t })
    return i < Keywords.Len() && Keywords[i] == text
}
