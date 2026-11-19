// textprops, Mk: text.Has(c, Property.Alphabetic) (variant alphabetic) or
// text.CategoryOf(c) == Category.UppercaseLetter (variant upper) over
// 65,536 scalars drawn by splitmix64 from six ranges whose properties no
// Unicode version since 5.0 has changed (README.md), reps passes; the
// checksum counts the scalars that have the property, plus the pass's
// index times each hit, so a pass cannot be skipped.
// Usage: textprops <variant> <reps> <seed>.

import std.text

const N: int = 65536

Mix(z0: u64): u64 {
    var z := z0
    z = (z ^ (z >> 30)) *% 0xBF58476D1CE4E5B9
    z = (z ^ (z >> 27)) *% 0x94D049BB133111EB
    return z ^ (z >> 31)
}

// The ranges, first and count.
const Lo: [6]u32 = [0x20, 0xC0, 0x391, 0x410, 0x3041, 0x4E00]
const Len: [6]u32 = [95, 400, 57, 64, 86, 20902]

export Main(sys: &System): OutOfMemory|ParseError|WriteError!void {
    name := sys.args[1]
    reps := Cast[int](try sys.args[2].ParseInt())
    var st := Cast[u64](try sys.args[3].ParseInt())
    cs := try sys.arena.Slice[u32](N)
    for i in 0..N {
        st = st +% 0x9E3779B97F4A7C15
        z := Mix(st)
        k := Trunc[int](z % 6)
        cs[i] = Lo[k] + Trunc[u32]((z >> 8) % Cast[u64](Len[k]))
    }
    upper := name == "upper"
    var acc: u64 = 0
    for r in 0..reps {
        for i in 0..N {
            c := Cast[char](cs[i])
            hit := if upper { text.CategoryOf(c) == text.Category.UppercaseLetter } else { text.Has(c, text.Property.Alphabetic) }
            if hit {
                acc = acc +% 1 +% Cast[u64](r)
            }
        }
    }
    var out := sys.Stdout()
    try Print(mut out, "{acc}\n", (acc))
}
