// Why: the server writes replies and every client parses them; both sides
// share one definition of the reply side of the kvd line protocol.
// What: the writers Status, Problem, Int, Bulk, Nil and Array, and
// ParseReply with the Reply enum.
// How: every reply line starts with a byte that says what it holds:
//   +text   a status        -text   an error        :n      an integer
//   $text   a value         _       no value        *n      n "$" lines follow

export error BadReply(offset: int)

/// One reply line, parsed. Text is a view of the line.
export enum Reply {
    Status(text: str)
    Problem(text: str)
    Int(n: i64)
    Bulk(value: str)
    Nil
    Array(n: int)
}

/// Writes a status reply such as `+OK`.
export Status(w: mut Writer, text: str): WriteError!void {
    try Print(w, "+{text}\n", (text))
}

/// Writes an error reply; `e` prints itself, `-ERR KeyTooLong(len: 300, limit: 256)`.
export Problem[E: Format + Plain](w: mut Writer, e: E): WriteError!void {
    try Print(w, "-ERR {e}\n", (e))
}

/// Writes an integer reply.
export Int(w: mut Writer, n: i64): WriteError!void {
    try Print(w, ":{n}\n", (n))
}

/// Writes a value reply. The value holds no line break (request.CheckValue).
export Bulk(w: mut Writer, value: str): WriteError!void {
    try w.Str("$")
    try w.Str(value)
    try w.Str("\n")
}

/// Writes the reply for a missing value.
export Nil(w: mut Writer): WriteError!void {
    try w.Str("_\n")
}

/// Writes the header of an array of `n` value lines, which the caller writes next.
export Array(w: mut Writer, n: int): WriteError!void {
    try Print(w, "*{n}\n", (n))
}

/// Parses one reply line, without its line break. Fails with `BadReply` and
/// the offset of the first byte that does not fit. O(n); allocates nothing.
export ParseReply(line: str): BadReply!Reply from line {
    if line.Len() == 0 {
        fail BadReply(offset: 0)
    }
    rest := line[1..]
    return match line.Byte(0) {
        b'+' { Reply.Status(text: rest) }
        b'-' { Reply.Problem(text: rest) }
        b':' {
            n := rest.ParseInt() else {
                fail BadReply(offset: 1)
            }
            Reply.Int(n: n)
        }
        b'$' { Reply.Bulk(value: rest) }
        b'_' {
            if rest.Len() > 0 {
                fail BadReply(offset: 1)
            }
            Reply.Nil
        }
        b'*' {
            n := rest.ParseInt() else {
                fail BadReply(offset: 1)
            }
            if n < 0 {
                fail BadReply(offset: 1)
            }
            Reply.Array(n: Cast[int](n))
        }
        _ {
            fail BadReply(offset: 0)
        }
    }
}
