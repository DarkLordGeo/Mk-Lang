// Why: some commands need a whole input at once.
// What: ReadWhole, ReadText and WholeLimit.
// How: reads up to a byte limit and fails past it.

/// The default limit of a whole-input command, in bytes.
export const WholeLimit: int = 8388608

/// Reads the rest of `r` into `a` with `Reader.ReadAll`, at most `limit`
/// bytes. Fails with `TooLarge` past `limit`. O(n); allocates `limit` + 1
/// bytes in `a`.
export ReadWhole(a: &Arena, r: mut Reader, limit: int): Error![]u8 from a {
    buf := try a.Slice[u8](limit + 1)
    n := try r.ReadAll(buf)
    if n > limit {
        fail TooLarge(limit: limit)
    }
    return buf[..n]
}

/// Reads the rest of `r` as UTF-8 text, as `ReadWhole` does. Fails with
/// `BadUtf8` at the first line that is not UTF-8.
export ReadText(a: &Arena, r: mut Reader, limit: int): Error!str from a {
    bytes := try ReadWhole(a, r, limit)
    return str.From(bytes) else {
        fail BadUtf8(line: BadLine(bytes).lines + 1)
    }
}
