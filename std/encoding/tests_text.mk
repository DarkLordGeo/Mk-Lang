// Why: Lookup, DecodeStrict and Encode each declare the members of
// encoding.Error they can fail with, and callers rely on that list.
// What: test blocks over those error sets, with helpers TxLookupWhy,
// TxDecodeWhy and TxEncodeWhy.
// How: each match names the members of one function's set and needs no
// arm for the rest of encoding.Error.

// TxLookupWhy is the member Lookup of label failed with, "" on success.
TxLookupWhy(label: str): str {
    _ = Lookup(label) else e {
        return match e {
            UnknownLabel { "UnknownLabel" }
            Unsupported { "Unsupported" }
        }
    }
    return ""
}

// TxEncodeWhy is the member e.Encode of s failed with, "" on success.
TxEncodeWhy(a: &Arena, e: Encoding, s: str): str from a {
    _ = e.Encode(a, s) else err {
        return match err {
            BadInput(offset) { if offset == 0 { "BadInput 0" } else { "BadInput" } }
            Unsupported { "Unsupported" }
            OutOfMemory { "OutOfMemory" }
        }
    }
    return ""
}

// TxDecodeWhy is the member e.DecodeStrict of b failed with, "" on
// success.
TxDecodeWhy(a: &Arena, e: Encoding, b: []u8): str from a {
    _ = e.DecodeStrict(a, b) else err {
        return match err {
            BadInput(offset) { if offset == 1 { "BadInput 1" } else { "BadInput" } }
            OutOfMemory { "OutOfMemory" }
        }
    }
    return ""
}

const TxBad: [2]u8 = [0x61, 0xFF]

test "Lookup, DecodeStrict and Encode fail with precise sets, so a match names only the members each can fail with" (sys: &System) {
    Assert(TxLookupWhy(" UTF-8 ") == "")
    Assert(TxLookupWhy("no-such-label") == "UnknownLabel")
    Assert(TxLookupWhy("shift_jis") == "Unsupported")
    Assert(TxEncodeWhy(sys.arena, Windows1252, "é") == "")
    Assert(TxEncodeWhy(sys.arena, Windows1252, "あ") == "BadInput 0")
    Assert(TxEncodeWhy(sys.arena, Replacement, "x") == "Unsupported")
    Assert(TxDecodeWhy(sys.arena, Utf8, "ab".AsBytes()) == "")
    Assert(TxDecodeWhy(sys.arena, Utf8, TxBad[..]) == "BadInput 1")
}

const TxBomBad: [7]u8 = [0xEF, 0xBB, 0xBF, 0xEF, 0xBB, 0xBF, 0xFF]
const TxBomLe: [4]u8 = [0xFF, 0xFE, 0x41, 0x00]

// DecodeKeepBom keeps a leading mark of the encoding's own as
// U+FEFF, where Decode removes one.
test "DecodeKeepBom keeps a leading byte order mark, Decode removes one" (sys: &System) {
    a := sys.arena
    Assert(Utf8.Decode(a, "\u{FEFF}x".AsBytes()).Must() == "x")
    Assert(Utf8.DecodeKeepBom(a, "\u{FEFF}x".AsBytes()).Must() == "\u{FEFF}x")
    Assert(Utf8.Decode(a, TxBomBad[..]).Must() == "\u{FEFF}\u{FFFD}")
    Assert(Utf8.DecodeKeepBom(a, TxBomBad[..]).Must() == "\u{FEFF}\u{FEFF}\u{FFFD}")
    Assert(Utf16Le.Decode(a, TxBomLe[..]).Must() == "A")
    Assert(Utf16Le.DecodeKeepBom(a, TxBomLe[..]).Must() == "\u{FEFF}A")
    Assert(Utf8.DecodeKeepBom(a, "".AsBytes()).Must() == "")
}

test "PercentSet.Has is PercentEncode's choice of byte" (sys: &System) {
    a := sys.arena
    sets: [8]PercentSet = [PercentSet.C0Control, PercentSet.Fragment, PercentSet.Query, PercentSet.SpecialQuery, PercentSet.Path, PercentSet.Userinfo, PercentSet.Component, PercentSet.FormUrlencoded]
    for set in sets {
        for i in 0..256 {
            var one: [1]u8 = [Trunc[u8](i)]
            out := PercentEncode(a, one[..], set).Must()
            // A byte not in the set is written as it is; FormUrlencoded
            // writes the space, which it holds, as `+`.
            Assert(set.Has(one[0]) == (out.Len() != 1 || i == 0x20 && out == "+"))
        }
    }
    Assert(PercentSet.Query.Has(b'#') && !PercentSet.Fragment.Has(b'#'))
    Assert(PercentSet.SpecialQuery.Has(b'\'') && !PercentSet.Query.Has(b'\''))
    Assert(PercentSet.C0Control.Has(0x7F) && PercentSet.C0Control.Has(0x80) && !PercentSet.C0Control.Has(b' '))
}
