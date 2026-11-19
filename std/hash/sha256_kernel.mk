// Why: the SHA extensions (x86_64 sha, aarch64 sha2) hash several times
// faster than portable code; SHA-256 should use them where the CPU has them.
//
// What: Sha256Kernel, the instruction path Sha256Compress (sha256.mk) offers
// its whole blocks to first.
//
// How: it calls std.simd.kernel's Sha256Blocks, which compresses the blocks
// when the running CPU has the extensions and else reports false having
// written nothing; the portable loop then runs, and it is the reference.
// Both give the same bits. std.hash holds no unsafe block: the kernel's own
// guard carries the obligation. The offer sits at the top of
// Sha256Compress, whose unrolled rounds keep it far above LLVM's inlining
// threshold, so the binary holds one copy of the kernel's guard, and
// Sha256State's Bytes and Sum keep only their own length tests.
// std-hash_ct_test.go reads every jump of the guard there, on the kernel
// and the masked builds, with and without a profile.

import std.simd.kernel

// Sha256Kernel compresses every whole 64-byte block of data into h on the
// SHA extensions and reports true, or reports false, writing nothing, when
// the CPU lacks them (always in the evaluator and on freestanding). O(n);
// one cached feature test per call.
Sha256Kernel(h: mut [8]u32, data: []u8): bool {
    return kernel.Sha256Blocks(h, data)
}
