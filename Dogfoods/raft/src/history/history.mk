// Why: the checker and the replay compare what clients asked and were told.
// What: History, Op, Kind, Parse and the errors of a history file.
// How: one operation a line (client, call and return times, op, key,
// arguments, result) under a versioned header; Parse reads what Write writes.

/// What a client asked of one register.
export enum Kind: u8 {
    Get = 1
    Put = 2
    Cas = 3
}

/// One client operation, from its invocation to its response, with times
/// in nanoseconds. ret is -1 when no response came, so the operation may
/// or may not have taken effect, and its result is unknown.
export struct Op {
    export client: int
    export call: i64
    export ret: i64
    export kind: Kind
    /// An index into the history's keys.
    export key: int
    /// Put: the value written. Cas: the expected value.
    export arg: i64
    /// Cas: the value written when the register held arg.
    export arg2: i64
    /// Get: the value read.
    export value: i64
    /// Cas: whether it swapped.
    export ok: bool
}

/// A history: every operation of every client, and the register names its
/// operations index.
export view struct History {
    export ops: List[Op]
    export keys: List[str]
}

/// A line that is not an operation; column counts words from 1.
export error BadLine(line: int, column: int)
/// A response before its invocation.
export error BadTime(line: int)
/// More registers than a history may name.
export error TooManyKeys(line: int)
/// Everything Parse can refuse.
export type Error = BadLine | BadTime | TooManyKeys | OutOfMemory

/// The registers a history may name.
export const MaxKeys: int = 64

/// An empty history in a. O(1); allocates nothing until it grows.
export History.New(a: &Arena): History {
    return History(ops: List[Op].New(a), keys: List[str].New(a))
}

/// An empty history over the registers k0 to k(n - 1). Fails with
/// OutOfMemory when a cannot hold the names. O(n); allocates in a.
export History.Numbered(a: &Arena, n: int): OutOfMemory!History from a
    requires n >= 0 && n <= MaxKeys
{
    var h := History.New(a)
    for k in 0..n {
        try h.keys.Push(try a.Fmt("k{k}", (k)))
    }
    return h
}

/// The index of key, adding it when new. Fails with TooManyKeys past
/// MaxKeys and OutOfMemory when the list cannot grow. O(keys).
export History.Key(mut self, key: str into self, line: int): TooManyKeys|OutOfMemory!int {
    for i, k in self.keys {
        if k == key {
            return i
        }
    }
    if self.keys.Len() == MaxKeys {
        fail TooManyKeys(line: line)
    }
    try self.keys.Push(key)
    return self.keys.Len() - 1
}

/// The register's name, or "?" for an index the history does not have.
export History.Name(&self, key: int): str from self {
    k := self.keys.Get(key) else {
        return "?"
    }
    return *k
}

/// Writes one operation as a line of the text format:
///
///     <client> <call> <return or -> get <key> -> <value or ?>
///     <client> <call> <return or -> put <key> <value>
///     <client> <call> <return or -> cas <key> <old> <new> -> <ok, fail or ?>
///
/// Fails as w does. O(1); allocates nothing.
export History.WriteOp(&self, w: mut Writer, op: &Op): WriteError!void {
    name := self.Name(op.key)
    try Print(w, "{op.client} {op.call} ", (op))
    if op.ret < 0 {
        try w.Str("- ")
    } else {
        try Print(w, "{op.ret} ", (op))
    }
    match op.kind {
        .Get {
            try Print(w, "get {name} -> ", (name))
            if op.ret < 0 {
                try w.Str("?")
            } else {
                try Print(w, "{op.value}", (op))
            }
        }
        .Put {
            try Print(w, "put {name} {op.arg}", (name, op))
        }
        .Cas {
            try Print(w, "cas {name} {op.arg} {op.arg2} -> ", (name, op))
            result := if op.ret < 0 { "?" } else if op.ok { "ok" } else { "fail" }
            try w.Str(result)
        }
    }
    try w.Str("\n")
}

/// Writes the whole history, one line per operation, in the order the
/// operations were invoked. Fails as w does. O(ops).
export History.Write(&self, w: mut Writer): WriteError!void {
    try w.Str("# raft history v1: client call return op key args -> result\n")
    for op in self.ops {
        try self.WriteOp(w, &op)
    }
}

// The words of one line, at most nine of them.
struct Words9 {
    w: [9]str
    n: int
}

Split9(line: str): ?Words9 {
    var out := Words9(w: [""; 9], n: 0)
    for word in line.Words() {
        if out.n == 9 {
            return none
        }
        out.w[out.n] = word
        out.n += 1
    }
    return some(out)
}

Number(s: str, line: int, column: int): BadLine!i64 {
    return s.ParseInt() else {
        fail BadLine(line: line, column: column)
    }
}

/// Reads the text format History.Write writes. Blank lines and lines that
/// start with # are skipped. Fails with BadLine at the first word that does
/// not fit, BadTime for a response before its invocation, TooManyKeys past
/// MaxKeys registers and OutOfMemory when a cannot hold the result.
/// O(length of text); allocates the operations and copies of the key names
/// in a, so the history holds nothing of text.
export Parse(a: &Arena, text: str): Error!History from a {
    var h := History.New(a)
    var line := 0
    for raw in text.Lines() {
        line += 1
        s := raw.Trim()
        if s.Len() == 0 || s.StartsWith("#") {
            continue
        }
        ws := Split9(s) else {
            fail BadLine(line: line, column: 10)
        }
        if ws.n < 5 {
            fail BadLine(line: line, column: ws.n + 1)
        }
        client := try Number(ws.w[0], line, 1)
        call := try Number(ws.w[1], line, 2)
        ret := if ws.w[2] == "-" { -1 } else { try Number(ws.w[2], line, 3) }
        if ret >= 0 && ret < call {
            fail BadTime(line: line)
        }
        key := try h.Key(try a.Dup(ws.w[4]), line)
        var op := Op(client: Trunc[int](client), call: call, ret: ret, kind: Kind.Get, key: key, arg: 0, arg2: 0, value: 0, ok: false)
        match ws.w[3] {
            "get" {
                if ws.n != 7 || ws.w[5] != "->" {
                    fail BadLine(line: line, column: 6)
                }
                if ret >= 0 {
                    op.value = try Number(ws.w[6], line, 7)
                } else if ws.w[6] != "?" {
                    fail BadLine(line: line, column: 7)
                }
            }
            "put" {
                if ws.n != 6 {
                    fail BadLine(line: line, column: 6)
                }
                op.kind = Kind.Put
                op.arg = try Number(ws.w[5], line, 6)
            }
            "cas" {
                if ws.n != 9 || ws.w[7] != "->" {
                    fail BadLine(line: line, column: 8)
                }
                op.kind = Kind.Cas
                op.arg = try Number(ws.w[5], line, 6)
                op.arg2 = try Number(ws.w[6], line, 7)
                result := ws.w[8]
                if ret < 0 && result != "?" || ret >= 0 && result != "ok" && result != "fail" {
                    fail BadLine(line: line, column: 9)
                }
                op.ok = result == "ok"
            }
            _ {
                fail BadLine(line: line, column: 4)
            }
        }
        try h.ops.Push(op)
    }
    return h
}
