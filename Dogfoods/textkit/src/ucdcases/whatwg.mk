// Why: the WHATWG label table and single-byte indexes become cases.
// What: Whatwg, Written and the BadIndex error.
// How: reads the index files and writes a decode and an encode case for each.

import std.fs
import std.json

/// What `Whatwg` wrote: how many labels, and how many single-byte encodings.
struct Written {
    labels: int
    encodings: int
}

/// A line of an index file that is not `pointer<tab>0xCODE`, counted from 1.
error BadIndex(line: int)

/// The heading of encodings.json's group of single-byte encodings, the
/// encodings with an index of 128 pointers (Encoding Standard section 9).
const SingleByte: str = "Legacy single-byte encodings"

/// The case directory's path to the label list, as the harness names it.
const LabelsPath: str = "$CASES/data/whatwg/labels.txt"

/// Writes the cases of the Encoding Standard's files in `dir`:
/// `convert-labels`, every label of encodings.json, the expected name of
/// each, or the error of each label of a multi-byte CJK encoding; and for
/// each single-byte encoding a decoding and an encoding case from its index.
Whatwg(a: &Arena into a, io: Io, dir: str, cases: str): fs.Error|json.Error|BadIndex|OutOfMemory!Written {
    fs.MakeDir(io, try a.Fmt("{cases}/data/whatwg", (cases))) else e {
        match e {
            fs.Exists {}
            _ { fail e }
        }
    }
    root := try json.Parse(a, try fs.ReadText(a, io, try a.Fmt("{dir}/encodings.json", (dir))))
    var labels := Text.New(a)
    var out := Text.New(a)
    var err := Text.New(a)
    var written := Written(labels: 0, encodings: 0)
    groups := try Items(&root)
    for g in 0..groups.Len() {
        group := &groups[g]
        heading := try TextOf(group, "heading")
        // multi-byte groups name the CJK encodings, which textkit does not convert
        cjk := heading.Has("multi-byte")
        entries := try Items(try Field(group, "encodings"))
        for e in 0..entries.Len() {
            entry := &entries[e]
            name := try TextOf(entry, "name")
            names := try Items(try Field(entry, "labels"))
            for l in 0..names.Len() {
                text := names[l].Str() else { fail json.Mismatch(offset: 0) }
                written.labels += 1
                try labels.Push(text)
                try labels.Push("\n")
                if cjk {
                    try err.Push(try a.Fmt("textkit: {p}:{n}: {text}: a multi-byte CJK encoding, which textkit does not convert\n", (p: LabelsPath, n: written.labels, text)))
                } else {
                    try out.Push(try a.Fmt("{text}\t{name}\n", (text, name)))
                }
            }
            if heading == SingleByte {
                try IndexCases(a, io, dir, cases, name)
                written.encodings += 1
            }
        }
    }
    try Write(a, io, cases, "data/whatwg/labels.txt", labels.Str().AsBytes())
    try Write(a, io, cases, "convert-labels.args", (try a.Fmt("convert\n--labels\n{p}\n", (p: LabelsPath))).AsBytes())
    try Write(a, io, cases, "convert-labels.out", out.Str().AsBytes())
    try Write(a, io, cases, "convert-labels.err", err.Str().AsBytes())
    try Write(a, io, cases, "convert-labels.code", if err.Str().Len() > 0 { b"3\n" } else { b"0\n" })
    return written
}

/// Writes the two cases of the single-byte encoding `name` from its index:
/// `whatwg-<name>-decode` decodes every byte, 00 to FF, and expects each
/// pointer's code point, U+FFFD for a pointer the index leaves out (the
/// single-byte decoder, section 9.1); `whatwg-<name>-encode` encodes every
/// code point the encoding has and expects the first pointer of each
/// (section 9.2, "index pointer").
IndexCases(a: &Arena, io: Io, dir: str, cases: str, name: str): fs.Error|BadIndex|OutOfMemory!void {
    lower := try name.ToLower(a)
    // ISO-8859-8-I decodes with ISO-8859-8's index (section 9)
    file := if lower == "iso-8859-8-i" { "iso-8859-8" } else { lower }
    index := try Index(a, io, try a.Fmt("{dir}/index-{file}.txt", (dir, file)))
    var all := try a.Slice[u8](256)
    var decoded := Text.New(a)
    var encodable := Text.New(a)
    var encoded := List[u8].New(a)
    for b in 0..256 {
        all[b] = Trunc[u8](b)
        code := if b < 0x80 { Cast[u32](b) } else { index[b - 0x80] }
        if code == 0 && b >= 0x80 {
            try decoded.PushChar('\u{FFFD}')
            continue
        }
        c := Cast[char](code)
        try decoded.PushChar(c)
        try encodable.PushChar(c)
        try encoded.Push(if b < 0x80 { Trunc[u8](b) } else { Trunc[u8](0x80 + First(index, code)) })
    }
    try Write(a, io, cases, try a.Fmt("data/whatwg/{lower}.bin", (lower)), all[..])
    try Write(a, io, cases, try a.Fmt("data/whatwg/{lower}.txt", (lower)), encodable.Str().AsBytes())
    try Write(a, io, cases, try a.Fmt("whatwg-{lower}-decode.args", (lower)), (try a.Fmt("convert\n--from\n{lower}\n$CASES/data/whatwg/{lower}.bin\n", (lower))).AsBytes())
    try Write(a, io, cases, try a.Fmt("whatwg-{lower}-decode.out", (lower)), decoded.Str().AsBytes())
    try Write(a, io, cases, try a.Fmt("whatwg-{lower}-encode.args", (lower)), (try a.Fmt("convert\n--to\n{lower}\n$CASES/data/whatwg/{lower}.txt\n", (lower))).AsBytes())
    try Write(a, io, cases, try a.Fmt("whatwg-{lower}-encode.out", (lower)), encoded.Done()[..])
}

/// The code point of each of the 128 pointers of the index file at `path`,
/// 0 for a pointer it leaves out. Each line is `pointer<tab>0xCODE<tab>...`,
/// and `#` starts a comment. Fails with `BadIndex` at a line of another form.
Index(a: &Arena, io: Io, path: str): fs.Error|BadIndex|OutOfMemory![]u32 from a {
    content := try fs.ReadText(a, io, path)
    var index := try a.Slice[u32](128)
    var n := 0
    for line in content.Lines() {
        n += 1
        if line.Trim().Len() == 0 || line.StartsWith("#") {
            continue
        }
        var columns := line.Trim().Split("\t")
        pointer := (columns.Next() else { "" }).Trim().ParseInt() else { fail BadIndex(line: n) }
        code := columns.Next() else { fail BadIndex(line: n) }
        if pointer < 0 || pointer >= 128 || !code.StartsWith("0x") {
            fail BadIndex(line: n)
        }
        index[pointer] = Hex(code[2..]) else { fail BadIndex(line: n) }
    }
    return index[..]
}

/// The first pointer of `index` whose code point is `code`; `code` is in it.
First(index: []u32, code: u32): int {
    for i, c in index {
        if c == code {
            return i
        }
    }
    return 0
}

/// The value of hex digits.
Hex(s: str): ?u32 {
    if s.Len() == 0 || s.Len() > 8 {
        return none
    }
    var n: u32 = 0
    for i in 0..s.Len() {
        b := s.Byte(i)
        d := if b >= b'0' && b <= b'9' { u32(b - b'0') } else if b >= b'a' && b <= b'f' { u32(b - b'a') + 10 } else if b >= b'A' && b <= b'F' { u32(b - b'A') + 10 } else { return none }
        n = n * 16 + d
    }
    return some(n)
}

/// The items of the array `v`. Fails with `Mismatch` when `v` is no array.
Items(v: &json.Value): json.Error![]json.Value from v {
    return match v {
        .Array(items) { items[..] }
        _ { fail json.Mismatch(offset: 0) }
    }
}

/// The member `key` of the object `v`. Fails with `Mismatch` when it has none.
Field(v: &json.Value, key: str): json.Error!&json.Value from v {
    return v.Get(key) else { fail json.Mismatch(offset: 0) }
}

/// The string member `key` of the object `v`.
TextOf(v: &json.Value, key: str): json.Error!str from v {
    return (try Field(v, key)).Str() else { fail json.Mismatch(offset: 0) }
}

/// Writes `data` to `cases/<path>`.
Write(a: &Arena, io: Io, cases: str, path: str, data: []u8): fs.Error|OutOfMemory!void {
    try fs.WriteAll(io, try a.Fmt("{cases}/{path}", (cases, path)), data)
}
