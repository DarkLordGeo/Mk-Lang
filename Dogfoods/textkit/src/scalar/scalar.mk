// Why: several commands take or print Unicode scalars as hex.
// What: ParseHex, Decode, Write and the BadScalar error.
// How: each token is hex digits naming a scalar value; surrogates and values
// past 10FFFF are refused.

/// A token of a hex line that is not hex digits or names no Unicode scalar
/// value (a surrogate, or past 10FFFF), counted from 1.
export error BadScalar(token: int)

/// What decoding hex notation can fail with.
export type Error = BadScalar | OutOfMemory

/// The highest Unicode scalar value.
const MaxScalar: u32 = 0x10FFFF

/// Decodes a line of code points in hex separated by spaces, as the UCD's
/// test files write them (`0041 0308`, each with an optional `U+`), into a
/// string in `a`. Fails with `BadScalar` at the first token that is not one.
/// O(n); allocates the string in `a`.
export Decode(a: &Arena, line: str): Error!str from a {
    var t := Text.New(a)
    var token := 0
    for word in line.Words() {
        token += 1
        digits := if word.StartsWith("U+") || word.StartsWith("u+") { word[2..] } else { word }
        n := ParseHex(digits) else {
            fail BadScalar(token: token)
        }
        c := ToChar(n) else {
            fail BadScalar(token: token)
        }
        try t.PushChar(c)
    }
    return t.Done()
}

/// `n` as a `char`, or `none` for a surrogate or a value past 10FFFF.
export ToChar(n: u32): ?char {
    if n > MaxScalar || (n >= 0xD800 && n <= 0xDFFF) {
        return none
    }
    return some(Cast[char](n))
}

/// The value of 1 to 8 hex digits, or `none`.
export ParseHex(s: str): ?u32 {
    if s.Len() == 0 || s.Len() > 8 {
        return none
    }
    var n: u32 = 0
    for i in 0..s.Len() {
        d := Digit(s.Byte(i)) else {
            return none
        }
        n = n * 16 + d
    }
    return some(n)
}

Digit(b: u8): ?u32 {
    if b >= b'0' && b <= b'9' {
        return some(u32(b - b'0'))
    }
    if b >= b'a' && b <= b'f' {
        return some(u32(b - b'a') + 10)
    }
    if b >= b'A' && b <= b'F' {
        return some(u32(b - b'A') + 10)
    }
    return none
}

/// Writes the scalars of `s` in hex, four digits at least, upper case,
/// separated by `sep`: `0041 0308`.
export Write(w: mut Writer, s: str, sep: str): WriteError!void {
    var first := true
    for c in s.Scalars() {
        if !first {
            try w.Str(sep)
        }
        first = false
        try Print(w, "{n:04X}", (n: u32(c)))
    }
}
