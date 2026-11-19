// Why: a parsed or built Value must be written back as JSON, and Print
// should show one as {v}.
//
// What: the impl of Format for Value (as Path's and net.Addr's are): compact
// JSON (RFC 8259), no whitespace, members in their order; ValidNumber, and
// the Out buffer.
//
// How: strings are escaped as RFC 8259 section 7 requires and no more, as
// serde_json's compact formatter does: '"' and '\' with a backslash, the
// control characters below 0x20 as \b \f \n \r \t or \u00XX, everything else
// (all of UTF-8, '/', U+2028 and U+2029 included) as is. The unescaped runs are
// found 16 bytes per step (Special16, parse.mk), then 8 (Special), and copied
// whole into the output buffer (Out, below). A Number's text is checked against
// RFC 8259 section 6 (ScanNumber, parse.mk: one pass over its bytes) and
// written as it is; a Number made by hand whose text is not a JSON number ("",
// "nan", "01") fails with WriteError and nothing of it is written, as a Text's
// writer refuses bytes that are not UTF-8, and as Go's json.Marshal refuses
// such a json.Number. The check is what keeps the output JSON: Value's variants
// are public, so a Number is not only Parse's.

import std.core
import std.raw

// Out is the output buffer of one Format call on an array or an object.
// Small pieces ("," ":" quotes, short strings) are copied into it, and it
// goes to the writer in one Bytes call when full and at the end, as Zig's
// buffered std.Io.Writer and Go's encodeState buffer do: a Text's writer has
// no buffer of its own and pays an indirect call and a UTF-8 check per
// write. Pieces are never split, so every flush is whole scalars (a Text's
// writer refuses a split one); a piece longer than the buffer goes to the
// writer directly, uncopied. 512 bytes on the stack; no allocation.
struct Out {
    b: [512]u8
    n: int
}

impl Format for Value {
    Format(&self, w: mut Writer): WriteError!void {
        match self {
            .Null {
                try w.Str("null")
            }
            .Bool(b) {
                try w.Str(if *b { "true" } else { "false" })
            }
            .Number(text) {
                if !ValidNumber(*text) {
                    fail WriteError
                }
                try w.Str(*text)
            }
            .Text(s) {
                try WriteStr(w, *s)
            }
            _ {
                var o := Out(b: [0; 512], n: 0)
                try Emit(self, mut o, w)
                if o.n > 0 {
                    try w.Bytes(o.b[0..o.n])
                }
            }
        }
    }
}

// Emit writes v into o, recursively; nesting is as deep as v's, which
// Parse bounds at 512 levels (a Value built by hand is not bounded).
Emit(v: &Value, o: mut Out, w: mut Writer): WriteError!void {
    match v {
        .Null {
            try Put(o, w, "null")
        }
        .Bool(b) {
            try Put(o, w, if *b { "true" } else { "false" })
        }
        .Number(text) {
            if !ValidNumber(*text) {
                fail WriteError
            }
            try Put(o, w, *text)
        }
        .Text(s) {
            try PutStr(o, w, *s)
        }
        .Array(items) {
            try PutByte(o, w, 0x5B)
            vs := items.Items()
            for k in 0..vs.Len() {
                if k > 0 {
                    try PutByte(o, w, 0x2C)
                }
                try Emit(&vs[k], o, w)
            }
            try PutByte(o, w, 0x5D)
        }
        .Object(members) {
            try PutByte(o, w, 0x7B)
            ms := members.Items()
            for k in 0..ms.Len() {
                if k > 0 {
                    try PutByte(o, w, 0x2C)
                }
                try PutStr(o, w, ms[k].key)
                try PutByte(o, w, 0x3A)
                try Emit(&ms[k].value, o, w)
            }
            try PutByte(o, w, 0x7D)
        }
    }
}

// ValidNumber reports whether s is one JSON number, RFC 8259 section 6.
ValidNumber(s: str): bool {
    b := s.AsBytes()
    if b.Len() == 0 {
        return false
    }
    end := ScanNumber(b, 0) else { return false }
    return end == b.Len()
}

// Put appends s whole: into o when it fits, else after a flush, or straight
// to w when it is longer than the buffer.
Put(o: mut Out, w: mut Writer, s: str): WriteError!void {
    k := s.Len()
    if o.n + k > 512 {
        if o.n > 0 {
            try w.Bytes(o.b[0..o.n])
            o.n = 0
        }
        if k > 512 {
            try w.Str(s)
            return
        }
    }
    (mut o.b[o.n..o.n + k]).CopyFrom(s.AsBytes())
    o.n += k
}

// PutByte appends one ASCII byte.
PutByte(o: mut Out, w: mut Writer, c: u8): WriteError!void {
    if o.n == 512 {
        try w.Bytes(o.b[0..o.n])
        o.n = 0
    }
    o.b[o.n] = c
    o.n += 1
}

// WideStr is the length from which PutStr and WriteStr take the 16-byte
// vector step: below it the 8-byte loop alone is faster on short strings,
// and from it the vector is (the json-write and jsonlong-write bench
// rows).
const WideStr = 32

// PutStr appends s as a JSON string, quotes included: the unescaped runs
// whole, found 16 bytes per step (Special16), then 8, each escape by
// PutEscape.
PutStr(o: mut Out, w: mut Writer, s: str): WriteError!void {
    b := s.AsBytes()
    n := b.Len()
    try PutByte(o, w, 0x22)
    if n >= WideStr {
        var start := 0
        var i := 0
        for i < n {
            if i + 8 <= n {
                // The last 7 bytes pay one compare each.
                if i + 16 <= n {
                    m := Special16(b, i)
                    if m == 0 {
                        i += 16
                        continue
                    }
                    i += raw.Ctz64(m)
                } else {
                    m := Special(core.LoadU64(s, i))
                    if m == 0 {
                        i += 8
                        continue
                    }
                    i += raw.Ctz64(m) / 8
                }
            }
            c := b[i]
            if c != 0x22 && c != 0x5C && c >= 0x20 {
                i += 1
                continue
            }
            if start < i {
                try Put(o, w, s[start..i])
            }
            try PutEscape(o, w, c)
            i += 1
            start = i
        }
        if start < n {
            try Put(o, w, s[start..n])
        }
    } else {
        var start := 0
        var i := 0
        for i < n {
            if i + 8 <= n {
                m := Special(core.LoadU64(s, i))
                if m == 0 {
                    i += 8
                    continue
                }
                i += raw.Ctz64(m) / 8
            }
            c := b[i]
            if c != 0x22 && c != 0x5C && c >= 0x20 {
                i += 1
                continue
            }
            if start < i {
                try Put(o, w, s[start..i])
            }
            try PutEscape(o, w, c)
            i += 1
            start = i
        }
        if start < n {
            try Put(o, w, s[start..n])
        }
    }
    try PutByte(o, w, 0x22)
}

// PutEscape appends the escape of c, which is '"', backslash or below 0x20.
PutEscape(o: mut Out, w: mut Writer, c: u8): WriteError!void {
    try PutByte(o, w, 0x5C)
    if c == 0x22 || c == 0x5C {
        try PutByte(o, w, c)
    } else if c == 0x08 {
        try PutByte(o, w, 0x62)
    } else if c == 0x0C {
        try PutByte(o, w, 0x66)
    } else if c == 0x0A {
        try PutByte(o, w, 0x6E)
    } else if c == 0x0D {
        try PutByte(o, w, 0x72)
    } else if c == 0x09 {
        try PutByte(o, w, 0x74)
    } else {
        try Put(o, w, "u00")
        try PutByte(o, w, HexDigit(c >> 4))
        try PutByte(o, w, HexDigit(c & 0xF))
    }
}

// WriteStr writes s as a JSON string, quotes included, straight to w: the
// path of a Text value formatted alone (the codec's str fields), which
// needs no buffer.
WriteStr(w: mut Writer, s: str): WriteError!void {
    b := s.AsBytes()
    n := b.Len()
    try w.Str("\"")
    if n >= WideStr {
        var start := 0
        var i := 0
        for i < n {
            if i + 8 <= n {
                // The last 7 bytes pay one compare each.
                if i + 16 <= n {
                    m := Special16(b, i)
                    if m == 0 {
                        i += 16
                        continue
                    }
                    i += raw.Ctz64(m)
                } else {
                    m := Special(core.LoadU64(s, i))
                    if m == 0 {
                        i += 8
                        continue
                    }
                    i += raw.Ctz64(m) / 8
                }
            }
            c := b[i]
            if c != 0x22 && c != 0x5C && c >= 0x20 {
                i += 1
                continue
            }
            if start < i {
                try w.Str(s[start..i])
            }
            try WriteEscape(w, c)
            i += 1
            start = i
        }
        if start < n {
            try w.Str(s[start..n])
        }
    } else {
        var start := 0
        var i := 0
        for i < n {
            if i + 8 <= n {
                m := Special(core.LoadU64(s, i))
                if m == 0 {
                    i += 8
                    continue
                }
                i += raw.Ctz64(m) / 8
            }
            c := b[i]
            if c != 0x22 && c != 0x5C && c >= 0x20 {
                i += 1
                continue
            }
            if start < i {
                try w.Str(s[start..i])
            }
            try WriteEscape(w, c)
            i += 1
            start = i
        }
        if start < n {
            try w.Str(s[start..n])
        }
    }
    try w.Str("\"")
}

// WriteEscape writes the escape of c, which is '"', '\' or below 0x20.
WriteEscape(w: mut Writer, c: u8): WriteError!void {
    if c == 0x22 {
        try w.Str("\\\"")
    } else if c == 0x5C {
        try w.Str("\\\\")
    } else if c == 0x08 {
        try w.Str("\\b")
    } else if c == 0x0C {
        try w.Str("\\f")
    } else if c == 0x0A {
        try w.Str("\\n")
    } else if c == 0x0D {
        try w.Str("\\r")
    } else if c == 0x09 {
        try w.Str("\\t")
    } else {
        var u: [6]u8 = [0x5C, 0x75, 0x30, 0x30, HexDigit(c >> 4), HexDigit(c & 0xF)]
        try w.Bytes(u[..])
    }
}

// HexDigit is the lower-case hex digit of d, 0 to 15.
HexDigit(d: u8): u8 {
    return if d < 10 { 0x30 + d } else { 0x57 + d }
}
