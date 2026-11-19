// Why: a command line must split the way a shell would, without a shell.
//
// What: Words and Expand.
//
// How: as a POSIX shell splits a simple command: blanks separate words,
// single quotes keep everything, double quotes keep blanks and expand `$`,
// and a backslash outside single quotes takes the next byte as it is. No
// globbing, pipes or redirection: procman runs the program itself.

// `cmd` starts at column `col0` of line `n`; columns in errors count from 1.
Words(a: &Arena, cmd: str, col0: int, n: int, const lookup: fn(str, mut Text): OutOfMemory!bool): Error![]str from a {
    var out := List[str].New(a)
    var word := Text.New(a)
    var in_word := false
    var open_quote: u8 = 0
    var quote_at := 0
    var i := 0
    for i < cmd.Len() {
        c := cmd.Byte(i)
        if open_quote == b'\'' {
            if c == b'\'' {
                open_quote = 0
            } else {
                try word.Push(cmd[i..i + 1])
            }
            i += 1
            continue
        }
        if c == b'\\' && i + 1 < cmd.Len() {
            try word.Push(cmd[i + 1..i + 2])
            in_word = true
            i += 2
            continue
        }
        if c == b'$' {
            i = try Expand(mut word, cmd, i, col0, n, lookup)
            in_word = true
            continue
        }
        if open_quote == b'"' {
            if c == b'"' {
                open_quote = 0
            } else {
                try word.Push(cmd[i..i + 1])
            }
            i += 1
            continue
        }
        if c == b'\'' || c == b'"' {
            open_quote = c
            quote_at = i
            in_word = true
            i += 1
            continue
        }
        if IsSpace(c) {
            if in_word {
                try out.Push(try a.Dup(word.Str()))
                word.Clear()
                in_word = false
            }
            i += 1
            continue
        }
        try word.Push(cmd[i..i + 1])
        in_word = true
        i += 1
    }
    if open_quote != 0 {
        fail UnclosedQuote(line: n, col: col0 + quote_at + 1)
    }
    if in_word {
        try out.Push(try a.Dup(word.Str()))
    }
    return out.Done()[..]
}

// Expands the `$` at `cmd[i]` into `word`: `$$` is a dollar, `$NAME` and
// `${NAME}` the variable's value. Returns the index after what it read.
Expand(word: mut Text, cmd: str, i: int, col0: int, n: int, const lookup: fn(str, mut Text): OutOfMemory!bool): Error!int {
    if i + 1 < cmd.Len() && cmd.Byte(i + 1) == b'$' {
        try word.Push("$")
        return i + 2
    }
    braced := i + 1 < cmd.Len() && cmd.Byte(i + 1) == b'{'
    start := if braced { i + 2 } else { i + 1 }
    var end := start
    for end < cmd.Len() && IsVarByte(cmd.Byte(end)) {
        end += 1
    }
    if end == start {
        fail Syntax(line: n, col: col0 + i + 1)
    }
    if braced && (end == cmd.Len() || cmd.Byte(end) != b'}') {
        fail Syntax(line: n, col: col0 + end + 1)
    }
    if !try lookup(cmd[start..end], word) {
        fail UnknownVariable(line: n, col: col0 + i + 1)
    }
    return if braced { end + 1 } else { end }
}

IsVarByte(c: u8): bool {
    return (c >= b'a' && c <= b'z') || (c >= b'A' && c <= b'Z') || (c >= b'0' && c <= b'9') || c == b'_'
}
