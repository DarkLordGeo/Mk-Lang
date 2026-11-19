// Why: a pattern known when the program is built can be matched by
// compiled code with no arena and no Compile at run time.
//
// What: gen regex.Matcher(T, "pattern"), which gives T.IsMatch(s: str): bool
// and T.Find(s: str): ?Match.
//
// How: the generator runs Compile in the evaluator and emits the pattern's
// two DFAs (dfa.mk) as const static-slice tables, with the scan loops over
// them: IsMatch is the forward scan that stops at the first match, Find
// the forward scan to the leftmost-first end and the reverse scan back to
// its start, the same loops as dfa.mk's Scan and ScanRev, specialised to a
// scan from offset 0. A pattern whose DFAs are over dfa.mk's budget has no
// complete table, and generated code cannot fall back to the Pike VM, so
// it fails the run (E0903) and names Compile, which can. The emitted code
// names regex.Match of this module, and in the prefilter (RxPre, emitted
// beside the tables: dfa.mk's FindPre written out, since generated code
// sits in the user's module and FindPre is private) simd.IndexByte,
// simd.Vec, simd.Equal, raw.Ctz64 and core.FindFrom, each pinned through
// this file's imports.

import std.core
import std.raw
import std.simd

/// Matcher writes, for pattern, `T.IsMatch(s: str): bool` and
/// `T.Find(s: str): ?Match` as table-driven code: the pattern compiles when
/// the program builds, and matching needs no arena and no Compile at run
/// time. Use it as `gen regex.Matcher(T, "pattern")`, usually on an empty
/// struct T. Fails with E0903 at T when the pattern is not valid or either
/// of its DFAs needs more than 512 states; use Compile for such a pattern.
/// A long pattern can run out of the gen line's fuel first, E0902: state
/// more with `fuel N` on the line. Runs at compile time; the emitted
/// functions are O(n) and allocate nothing.
export Matcher(g: mut Gen, t: Type, pattern: str) {
    a := g.Arena()
    re := CompileIn(a, pattern, MaxGenStates) else e {
        match e {
            Syntax(offset) {
                msg := a.Fmt("regex.Matcher: syntax error at byte {offset} of the pattern", (offset)) else {
                    g.FailOn(t, "regex.Matcher: syntax error in the pattern")
                    return
                }
                g.FailOn(t, msg)
            }
            TooLarge {
                g.FailOn(t, "regex.Matcher: the pattern is too large (over 4,096 instructions, or nested over 1,000 levels)")
            }
            OutOfMemory {
                g.FailOn(t, "regex.Matcher: out of memory while compiling the pattern")
            }
        }
        return
    }
    if !re.dfa.ok || !re.rdfa.ok {
        g.FailOn(t, "regex.Matcher: the pattern's DFA is over the generator's budget (512 states per DFA), so it has no complete table; use regex.Compile, which falls back to the Pike VM")
        return
    }
    fc := g.Fresh("RxFwdClass")
    ft := g.Fresh("RxFwd")
    rc := g.Fresh("RxRevClass")
    rt := g.Fresh("RxRev")
    pf := g.Fresh("RxPre")
    fcls := Rows8(g, re.dfa.cls)
    ftab := Rows32(g, re.dfa.trans)
    rcls := Rows8(g, re.rdfa.cls)
    rtab := Rows32(g, re.rdfa.trans)
    f0 := re.dfa.starts[0]
    f1 := re.dfa.starts[1]
    f2 := re.dfa.starts[2]
    f3 := re.dfa.starts[3]
    top := re.dfa.top
    skip := re.prefix.Len() > 0
    pre := g.Str(re.prefix)
    // The pair filter's two places, RarePair's choice made here so the
    // emitted code loads at constant offsets.
    var plo := 0
    var phi := 1
    if re.prefix.Len() >= 2 {
        pr := RarePair(re.prefix.AsBytes())
        plo = pr.lo
        phi = pr.hi
    }
    feot := Cast[int](re.dfa.eot)
    r0 := re.rdfa.starts[0]
    r1 := re.rdfa.starts[1]
    r2 := re.rdfa.starts[2]
    r3 := re.rdfa.starts[3]
    reot := Cast[int](re.rdfa.eot)
    g.Emit(quote {
        const $(fc): []u8 = [
            $fcls
        ]

        const $(ft): []u32 = [
            $ftab
        ]

        const $(rc): []u8 = [
            $rcls
        ]

        const $(rt): []u32 = [
            $rtab
        ]

        $(t.Name()).IsMatch(s: str): bool {
            b := s.AsBytes()
            n := b.Len()
            var st: u32 = $(f0)
            if st == 0 {
                return false
            }
            var i := 0
            for i < n {
                if $(skip) && st <= $(top) {
                    j := $(pf)(s, i) else {
                        return false
                    }
                    i = j
                    if i > 0 {
                        p := b[i - 1]
                        st = if p == 10 {
                            $(f1)
                        } else if p >= 48 && p <= 57 || p >= 65 && p <= 90 || p == 95 || p >= 97 && p <= 122 {
                            $(f2)
                        } else {
                            $(f3)
                        }
                    }
                }
                e := $(ft)[Cast[int](st) + Cast[int]($(fc)[Cast[int](b[i])])]
                if e >= 0x80000000 {
                    return true
                }
                st = e & 0x7FFFFFFF
                if st == 0 {
                    return false
                }
                i += 1
            }
            return $(ft)[Cast[int](st) + $(feot)] >= 0x80000000
        }

        $(t.Name()).Find(s: str): ?regex.Match {
            b := s.AsBytes()
            n := b.Len()
            var st: u32 = $(f0)
            var end := -1
            var i := 0
            for st != 0 && i < n {
                if $(skip) && st <= $(top) {
                    j := $(pf)(s, i) else {
                        break
                    }
                    i = j
                    if i > 0 {
                        p := b[i - 1]
                        st = if p == 10 {
                            $(f1)
                        } else if p >= 48 && p <= 57 || p >= 65 && p <= 90 || p == 95 || p >= 97 && p <= 122 {
                            $(f2)
                        } else {
                            $(f3)
                        }
                    }
                }
                e := $(ft)[Cast[int](st) + Cast[int]($(fc)[Cast[int](b[i])])]
                if e >= 0x80000000 {
                    end = i
                }
                st = e & 0x7FFFFFFF
                i += 1
            }
            if st != 0 && $(ft)[Cast[int](st) + $(feot)] >= 0x80000000 {
                end = n
            }
            if end < 0 {
                return none
            }
            c: u8 = if end < n { b[end] } else { 0 }
            var r: u32 = if end == n {
                $(r0)
            } else if c == 10 {
                $(r1)
            } else if c >= 48 && c <= 57 || c >= 65 && c <= 90 || c == 95 || c >= 97 && c <= 122 {
                $(r2)
            } else {
                $(r3)
            }
            var lo := end
            var j := end
            for r != 0 && j > 0 {
                e := $(rt)[Cast[int](r) + Cast[int]($(rc)[Cast[int](b[j - 1])])]
                if e >= 0x80000000 {
                    lo = j
                }
                r = e & 0x7FFFFFFF
                j -= 1
            }
            if r != 0 && $(rt)[Cast[int](r) + $(reot)] >= 0x80000000 {
                lo = 0
            }
            return some(regex.Match(lo: lo, hi: end))
        }

        // The first index at or after from where the prefix occurs in s:
        // one byte through simd.IndexByte; longer, 16 starts a step kept
        // where the two prefix bytes RarePair chose are in place, each
        // checked by simd.Equal, and past a verification budget the rest
        // by core.FindFrom (Two-Way), so a call is O(n + m). It is emitted
        // after the methods because, emitted first, its imported names do
        // not resolve.
        $(pf)(s: str, from: int): ?int {
            b := s.AsBytes()
            p := $pre.AsBytes()
            m := p.Len()
            n := b.Len()
            if m == 1 {
                k := simd.IndexByte(b[from..n], p[0]) else {
                    return none
                }
                return some(from + k)
            }
            if m == 0 || m > n - from {
                return core.FindFrom(s, $pre, from)
            }
            first := simd.Vec[u8, 16].Splat(p[$(plo)])
            second := simd.Vec[u8, 16].Splat(p[$(phi)])
            var i := from
            var work := 0
            for i + m - 1 + 16 <= n {
                x := simd.Vec[u8, 16].FromSlice(b, i + $(plo)) == first
                y := simd.Vec[u8, 16].FromSlice(b, i + $(phi)) == second
                var mask := (x & y).Bits()
                for mask != 0 {
                    c := i + raw.Ctz64(mask)
                    if simd.Equal(b[c..c + m], p) {
                        return some(c)
                    }
                    work += m
                    mask = mask & (mask - 1)
                }
                i += 16
                if work > i - from + 4096 {
                    break
                }
            }
            return core.FindFrom(s, $pre, i)
        }
    })
}

// Rows8 is xs as the elements of an array literal, 16 to a line.
Rows8(g: mut Gen, xs: []u8): Block {
    var b := g.Block()
    var i := 0
    for i < xs.Len() {
        x := xs[i]
        var row := quote { $(x), }
        var j := i + 1
        for j < xs.Len() && j < i + 16 {
            y := xs[j]
            row = row.Then(quote { $(y), })
            j += 1
        }
        b.Add(row)
        i = j
    }
    return b
}

// Rows32 is xs as the elements of an array literal, 8 to a line.
Rows32(g: mut Gen, xs: []u32): Block {
    var b := g.Block()
    var i := 0
    for i < xs.Len() {
        x := xs[i]
        var row := quote { $(x), }
        var j := i + 1
        for j < xs.Len() && j < i + 8 {
            y := xs[j]
            row = row.Then(quote { $(y), })
            j += 1
        }
        b.Add(row)
        i = j
    }
    return b
}
