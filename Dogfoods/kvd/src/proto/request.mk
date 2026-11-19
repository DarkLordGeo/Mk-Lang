// Why: requests are parsed in one place, with limits the server enforces
// before it does any work.
// What: Request, Parse, Bounds and the parse errors (Empty, UnknownCommand,
// KeyTooLong, ValueTooLong, BadByte and others).
// How: a request is one line of ASCII words ended by "\n": a command, then a
// key, then for writes a value, which is the rest of the line and may hold
// spaces. Commands are case-insensitive.

/// The longest key a server accepts unless configured otherwise, in bytes.
export const DefaultMaxKey: int = 256

/// The longest value a server accepts unless configured otherwise, in bytes.
export const DefaultMaxValue: int = 65536

export error Empty
export error UnknownCommand
export error MissingArgument
export error ExtraArgument
export error KeyTooLong(len: int, limit: int)
export error ValueTooLong(len: int, limit: int)
export error BadByte(offset: int)

/// What can be wrong with one request line.
export type Error = Empty | UnknownCommand | MissingArgument | ExtraArgument | KeyTooLong | ValueTooLong | BadByte

/// The size limits a request is checked against.
export struct Bounds {
    export max_key: int
    export max_value: int
}

/// One parsed request. Keys and values are views of the line it came from.
export enum Request {
    Ping
    Get(key: str)
    Set(key: str, value: str)
    Del(key: str)
    Append(key: str, value: str)
    Incr(key: str)
    GetSet(key: str, value: str)
    Keys(prefix: str)
    Size
    Hash(key: str)
    Wait(key: str)
    Stats
    Name(name: str)
    Clients
    Compact
    Quit
    Shutdown
}

/// Reports whether `r` changes the store, and so goes to the write-ahead log.
export Request.Writes(&self): bool {
    return match *self {
        .Set | .Del | .Append | .Incr | .GetSet { true }
        _ { false }
    }
}

/// Reports whether `r` compacts the log.
export Request.Compacts(&self): bool {
    return match *self {
        .Compact { true }
        _ { false }
    }
}

/// Splits `line` at its first space: the word before it and the rest after it.
/// A line with no space is all word. O(n); allocates nothing.
SplitWord(line: str): (word: str, rest: str) {
    i := line.Find(" ") else {
        return (word: line, rest: "")
    }
    return (word: line[..i], rest: line[i + 1..])
}

/// Checks that a key is non-empty, within `b.max_key` and free of control
/// bytes and spaces, which the line format cannot carry.
CheckKey(key: str, b: &Bounds): Error!void {
    if key.Len() == 0 {
        fail MissingArgument
    }
    if key.Len() > b.max_key {
        fail KeyTooLong(len: key.Len(), limit: b.max_key)
    }
    for i, c in key.AsBytes() {
        if c <= b' ' || c == 0x7F {
            fail BadByte(offset: i)
        }
    }
}

/// Checks that a value is within `b.max_value` and holds no line break.
CheckValue(value: str, b: &Bounds): Error!void {
    if value.Len() > b.max_value {
        fail ValueTooLong(len: value.Len(), limit: b.max_value)
    }
    for i, c in value.AsBytes() {
        if c == b'\n' || c == b'\r' {
            fail BadByte(offset: i)
        }
    }
}

/// A command that takes exactly one key.
KeyOnly(rest: str, b: &Bounds): Error!str from rest {
    (word: key, rest: extra) := SplitWord(rest)
    if extra.Len() > 0 {
        fail ExtraArgument
    }
    try CheckKey(key, b)
    return key
}

/// A command that takes a key and a value, the value being the rest of the line.
KeyValue(rest: str, b: &Bounds): Error!(key: str, value: str) from rest {
    (word: key, rest: value) := SplitWord(rest)
    try CheckKey(key, b)
    try CheckValue(value, b)
    return (key: key, value: value)
}

/// A command that takes no argument.
NoArgument(rest: str): Error!void {
    if rest.Len() > 0 {
        fail ExtraArgument
    }
}

/// Parses one request line, without its line break. Fails with `Empty` on a
/// blank line and `UnknownCommand` on an unknown word. O(n); allocates nothing.
export Parse(line: str, b: &Bounds): Error!Request from line {
    (word, rest) := SplitWord(line.Trim())
    match word {
        "" {
            fail Empty
        }
        "PING" | "ping" {
            try NoArgument(rest)
            return Request.Ping
        }
        "GET" | "get" {
            return Request.Get(key: try KeyOnly(rest, b))
        }
        "SET" | "set" {
            (key, value) := try KeyValue(rest, b)
            return Request.Set(key: key, value: value)
        }
        "DEL" | "del" {
            return Request.Del(key: try KeyOnly(rest, b))
        }
        "APPEND" | "append" {
            (key, value) := try KeyValue(rest, b)
            return Request.Append(key: key, value: value)
        }
        "INCR" | "incr" {
            return Request.Incr(key: try KeyOnly(rest, b))
        }
        "GETSET" | "getset" {
            (key, value) := try KeyValue(rest, b)
            return Request.GetSet(key: key, value: value)
        }
        "KEYS" | "keys" {
            return Request.Keys(prefix: rest)
        }
        "DBSIZE" | "dbsize" {
            try NoArgument(rest)
            return Request.Size
        }
        "HASH" | "hash" {
            return Request.Hash(key: try KeyOnly(rest, b))
        }
        "WAIT" | "wait" {
            return Request.Wait(key: try KeyOnly(rest, b))
        }
        "STATS" | "stats" {
            try NoArgument(rest)
            return Request.Stats
        }
        "NAME" | "name" {
            return Request.Name(name: try KeyOnly(rest, b))
        }
        "CLIENTS" | "clients" {
            try NoArgument(rest)
            return Request.Clients
        }
        "COMPACT" | "compact" {
            try NoArgument(rest)
            return Request.Compact
        }
        "QUIT" | "quit" {
            try NoArgument(rest)
            return Request.Quit
        }
        "SHUTDOWN" | "shutdown" {
            try NoArgument(rest)
            return Request.Shutdown
        }
        _ {
            fail UnknownCommand
        }
    }
}
