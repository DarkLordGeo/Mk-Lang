// regexrare, Mk: Regex.Find of patterns
// with a literal prefix that is rare in the text, where the DFA scan spends
// its time in the prefilter (FindPre: simd.IndexByte for one byte, a
// vector pair filter for more). Usage: regexrare <mib> <reps> <seed>
// [gen|eng|enggen]. The text is mib MiB of log-like lines from
// splitmix64(seed) (lowercase words, digits, spaces, newlines); each rep
// finds, from the start, the one-byte-prefix pattern "#[0-9]+" and the
// six-byte-prefix pattern "needle[0-9]+", each planted once near the end;
// the checksum sums the match offsets (README.md). With a fourth argument
// gen, the same two patterns run as gen regex.Matcher code (the emitted
// RxPre prefilter). With eng (enggen as gen code) the text's letters follow
// English letter frequencies (Eng) and the patterns are "theseus[0-9]+" and
// "needle[0-9]+", whose end bytes are common in it.

import std.regex

struct One {}

gen regex.Matcher(One, "#[0-9]+") fuel 200000000

struct Six {}

gen regex.Matcher(Six, "needle[0-9]+") fuel 200000000

struct Three {}

gen regex.Matcher(Three, "theseus[0-9]+") fuel 200000000

struct Mix {
    x: u64
}

Next(m: mut Mix): u64 {
    m.x = m.x +% 0x9E3779B97F4A7C15
    var z := m.x
    z = (z ^ (z >> 30)) *% 0xBF58476D1CE4E5B9
    z = (z ^ (z >> 27)) *% 0x94D049BB133111EB
    return z ^ (z >> 31)
}

// Eng is the eng text's bytes, one drawn per byte: 104 letters in English
// frequency proportions (e 12, a 9, t 8, ...), 20 spaces, 3 digits, a
// newline.
const Eng: [128]u8 = [0x65, 0x65, 0x65, 0x65, 0x65, 0x65, 0x65, 0x65, 0x65, 0x65, 0x65, 0x65, 0x74, 0x74, 0x74, 0x74, 0x74, 0x74, 0x74, 0x74, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x61, 0x6F, 0x6F, 0x6F, 0x6F, 0x6F, 0x6F, 0x6F, 0x6F, 0x69, 0x69, 0x69, 0x69, 0x69, 0x69, 0x69, 0x6E, 0x6E, 0x6E, 0x6E, 0x6E, 0x6E, 0x6E, 0x73, 0x73, 0x73, 0x73, 0x73, 0x73, 0x73, 0x68, 0x68, 0x68, 0x68, 0x68, 0x68, 0x72, 0x72, 0x72, 0x72, 0x72, 0x72, 0x64, 0x64, 0x64, 0x64, 0x6C, 0x6C, 0x6C, 0x6C, 0x63, 0x63, 0x63, 0x75, 0x75, 0x75, 0x6D, 0x6D, 0x77, 0x77, 0x66, 0x66, 0x67, 0x67, 0x79, 0x79, 0x70, 0x70, 0x62, 0x62, 0x76, 0x6B, 0x6A, 0x78, 0x71, 0x7A, 0x20, 0x20, 0x20, 0x20, 0x20, 0x20, 0x20, 0x20, 0x20, 0x20, 0x20, 0x20, 0x20, 0x20, 0x20, 0x20, 0x20, 0x20, 0x20, 0x20, 0x30, 0x35, 0x39, 0x0A]

export Main(sys: &System): OutOfMemory|ParseError|WriteError|Utf8Error|regex.Error!void {
    mib := Cast[int](try sys.args[1].ParseInt())
    reps := Cast[int](try sys.args[2].ParseInt())
    seed := Cast[u64](try sys.args[3].ParseInt())
    var mode := ""
    if sys.args.Len() > 4 {
        mode = sys.args[4]
    }
    english := mode == "eng" || mode == "enggen"
    emitted := mode == "gen" || mode == "enggen"
    n := mib * 1048576
    var b := try sys.arena.Slice[u8](n)
    var m := Mix(x: seed)
    for i in 0..n {
        if english {
            b[i] = Eng[Cast[int](Next(mut m) % 128)]
            continue
        }
        r := Next(mut m) % 32
        if r < 26 {
            b[i] = Trunc[u8](0x61 + r)
        } else if r < 29 {
            b[i] = 0x20
        } else if r < 31 {
            b[i] = Trunc[u8](0x30 + (Next(mut m) % 10))
        } else {
            b[i] = 0x0A
        }
    }
    var plant := "needle42 #7 ".AsBytes()
    if english {
        plant = "needle42 theseus7 ".AsBytes()
    }
    for k in 0..plant.Len() {
        b[n - 100 + k] = plant[k]
    }
    text := try str.From(b[..])
    var p1 := "#[0-9]+"
    if english {
        p1 = "theseus[0-9]+"
    }
    one := try regex.Compile(sys.arena, p1)
    six := try regex.Compile(sys.arena, "needle[0-9]+")
    var total: u64 = 0
    for _ in 0..reps {
        if emitted {
            if english {
                if x := Three.Find(text) {
                    total = total +% Cast[u64](x.lo)
                }
            } else if x := One.Find(text) {
                total = total +% Cast[u64](x.lo)
            }
            if y := Six.Find(text) {
                total = total +% Cast[u64](y.lo)
            }
            continue
        }
        if x := one.Find(text) {
            total = total +% Cast[u64](x.lo)
        }
        if y := six.Find(text) {
            total = total +% Cast[u64](y.lo)
        }
    }
    var out := sys.Stdout()
    try Print(mut out, "{total}\n", (total: total))
}
