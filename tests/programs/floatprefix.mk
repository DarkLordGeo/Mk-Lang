// str.ParseFloatPrefix: it parses the longest prefix that is a float by
// str.ParseFloat's grammar, and returns its value and how many bytes it used. Overflow gives an
// infinity with the text's sign, as ECMA-262's parseFloat does. A total underflow is a zero with the
// text's sign. A text with no float prefix gives NaN with used 0. No whitespace is skipped, and a
// prefix stops before a byte that cannot extend it. Each line prints the text, the bytes used, the
// value and its bits, so a signed zero and an infinity are told apart from their neighbours.
// Expected output: floatprefix.out.

Show(w: mut Writer, s: str): WriteError!void {
    r := s.ParseFloatPrefix()
    b := r.value.Bits()
    try Print(w, "\"{s}\" used {u} value {v} bits {b:x}\n", (s: s, u: r.used, v: r.value, b: b))
}

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    // Whole texts and texts with a tail.
    try Show(mut out, "3.25")
    try Show(mut out, "3.25abc")
    try Show(mut out, "-0.5,1")
    try Show(mut out, "1e3x")
    try Show(mut out, "0.1 ")
    // A prefix stops before the byte that cannot extend it.
    try Show(mut out, "1.5e")
    try Show(mut out, "1.5e+")
    try Show(mut out, "5.")
    try Show(mut out, "1_000")
    try Show(mut out, "0x10")
    // Nothing parses: NaN, used 0.
    try Show(mut out, "")
    try Show(mut out, ".")
    try Show(mut out, "+")
    try Show(mut out, " 1")
    try Show(mut out, "abc")
    // Words: inf and infinity take a sign, nan takes none.
    try Show(mut out, "inf")
    try Show(mut out, "-Infinity!")
    try Show(mut out, "infinit")
    try Show(mut out, "nan")
    try Show(mut out, "-nan")
    // Overflow is a signed infinity; total underflow a signed zero.
    try Show(mut out, "1e400")
    try Show(mut out, "-1e400")
    try Show(mut out, "1e-400")
    try Show(mut out, "-1e-400")
    // Correct rounding, as str.ParseFloat.
    try Show(mut out, "9007199254740993")
    try Show(mut out, "2.2250738585072011e-308")
    try Show(mut out, "0.30000000000000004;")
    try Show(mut out, "123456789012345678901234567890e-10z")
}
