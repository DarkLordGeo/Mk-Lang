// Why: std.url keeps its own percent-encode table (chars.mk), which also
// holds the forbidden host and domain flags, so it can drift from
// std.encoding's.
// What: one test that each of its six percent-encode sets agrees with
// encoding.PercentSet.Has.
// How: compares the two on every byte.

import std.encoding

test "chars.mk's six percent-encode sets agree with encoding.PercentSet.Has" (sys: &System) {
    for i in 0..256 {
        c := Trunc[u8](i)
        Assert(Encodes(c, InC0) == encoding.PercentSet.C0Control.Has(c))
        Assert(Encodes(c, InFragment) == encoding.PercentSet.Fragment.Has(c))
        Assert(Encodes(c, InQuery) == encoding.PercentSet.Query.Has(c))
        Assert(Encodes(c, InSpecialQuery) == encoding.PercentSet.SpecialQuery.Has(c))
        Assert(Encodes(c, InPath) == encoding.PercentSet.Path.Has(c))
        Assert(Encodes(c, InUserinfo) == encoding.PercentSet.Userinfo.Has(c))
    }
}
