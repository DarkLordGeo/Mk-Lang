// Why: crypto.Wipe must really overwrite secrets, but a plain store loop over
// a buffer that dies right after is dead code to LLVM, and so are relaxed
// atomic stores to a local that does not escape (measured: both vanish from
// a release build).
//
// What: the Wipe kernel, crypto.Wipe's loop.
//
// How: the bytes are written with std.mmio's volatile stores, which LLVM
// never removes, merges or reorders against each other. The evaluator
// refuses the alignment test (E0901, refused: pointer-bytes) and volatile
// access, so a compile-time evaluation that reaches Wipe fails rather than
// leaving the bytes in place.

import std.mmio
import std.raw

/// Wipe sets every byte of buf to 0 with stores the optimiser keeps, even
/// when buf is never read again. O(n): byte stores up to an 8-byte
/// boundary, then one 8-byte store per word, then byte stores; allocates
/// nothing. Refused at compile time (E0901).
export Wipe(buf: mut []u8) {
    n := buf.Len()
    if n == 0 {
        return
    }
    unsafe "every store is inside buf's n bytes, which the caller lends mutably, and each 8-byte store is at an address aligned to 8" {
        p := raw.DataMut(buf)
        var i := Cast[int]((8 - (raw.Addr(p) & 7)) & 7)
        if i > n {
            i = n
        }
        for j in 0..i {
            mmio.Store[u8](raw.Offset(p, j), 0)
        }
        for i + 8 <= n {
            mmio.Store[u64](raw.PtrCast[u8, u64](raw.Offset(p, i)), 0)
            i += 8
        }
        for i < n {
            mmio.Store[u8](raw.Offset(p, i), 0)
            i += 1
        }
    }
}
