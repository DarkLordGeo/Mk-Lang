// Why: most text is already valid UTF-8, so decoding it should cost one
// validation pass and a copy, while malformed input must still decode
// exactly as the WHATWG UTF-8 decoder does.
//
// What: Utf8Run, the UTF-8 half of text.mk's Run, with Utf8Units and the
// block sizes BlockMin and BlockMax.
//
// How: the WHATWG UTF-8 decoder (Utf8Step, maximal subparts) behind a block
// fast path, the method of Rust's Utf8Chunks and simdutf's convert_with_errors.
// A block cut at a scalar's start that simd.ValidUtf8 (Keiser and Lemire)
// accepts is counted, or copied, whole. Blocks start at 64 bytes and double
// while they are valid, up to 4,096; a longer block refused is looked at again
// in 64-byte blocks, and a 64-byte block refused is decoded a unit at a time to
// its end. A refusal restarts the doubling at the refused block's start, which
// roughly halves a byte's distance past 64 from the next block's start, so at
// most 13 refused blocks hold a byte: O(n) for any input, and malformed text
// costs at most a 64-byte block of the unit loop per malformed unit.

import std.simd

// BlockMin and BlockMax bound a block's length before it is cut back to a
// scalar's start.
const BlockMin: int = 64
const BlockMax: int = 4096

// At is where a unit loop stopped: the next byte to read, the next byte to
// write, the first malformed unit it met (-1 for none) and whether the run
// ends there (the input ends inside a unit, or strict met a malformed one).
type At = (i: int, o: int, bad: int, done: bool)

// Utf8Units decodes the units of b that start in from..lim one at a time,
// writing at out[at] (or only counting); with strict it stops before the
// first malformed unit, and the run ends.
Utf8Units(b: []u8, from: int, lim: int, last: bool, out: mut []u8, at: int, write: bool, strict: bool): At {
    var i := from
    var o := at
    var bad := -1
    for i < lim {
        x := b[i]
        if x < 0x80 {
            if write {
                out[o] = x
            }
            o += 1
            i += 1
            continue
        }
        st := Utf8Step(b, i, last)
        if st.n == 0 {
            return (i, o, bad, done: true)
        }
        if st.c == Bad {
            if bad < 0 {
                bad = i
            }
            if strict {
                return (i, o, bad, done: true)
            }
        }
        o = if write { Put(out, o, st.c) } else { o + Utf8Len(st.c) }
        i += st.n
    }
    return (i, o, bad, done: false)
}

// Utf8Run is Run for UTF-8.
Utf8Run(b: []u8, stop: int, last: bool, out: mut []u8, write: bool, strict: bool): Ran {
    var i := 0
    var o := 0
    var bad := -1
    var block := BlockMin
    for i < stop {
        var lim := stop
        if stop - i > block {
            // Cut the block at a scalar's start: well-formed UTF-8 has at
            // most three continuation bytes in a row. j < stop <= b.Len().
            var j := i + block
            var k := 0
            for k < 3 && b[j] & 0xC0 == 0x80 {
                j -= 1
                k += 1
            }
            if simd.ValidUtf8(b[i..j]) {
                if write {
                    (mut out[o..o + (j - i)]).CopyFrom(b[i..j])
                }
                o += j - i
                i = j
                if block < BlockMax {
                    block *= 2
                }
                continue
            }
            if block > BlockMin {
                // Look again from here in blocks of the least length,
                // doubling again (the header bounds the repeats).
                block = BlockMin
                continue
            }
            lim = j
        }
        // The refused block (or the tail) is decoded to its end, so a byte
        // is in at most one block validation however dense the errors.
        at := Utf8Units(b, i, lim, last, out, o, write, strict)
        i = at.i
        o = at.o
        if bad < 0 {
            bad = at.bad
        }
        if at.done {
            break
        }
    }
    return (read: i, wrote: o, bad)
}
