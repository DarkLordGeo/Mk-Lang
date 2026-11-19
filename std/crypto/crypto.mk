// Why: std.crypto's primitives share their errors, hash kinds, the
// constant-time comparison and the OS random source, and the rules that
// keep every file constant time belong in one place.
//
// What: the module's errors, HashKind and HashLen, Equal, Random and Wipe.
//
// How: cryptographic primitives checked against the RFC test vectors
// (RFC 2104 and 4231, 8439, 7748, 8032, 7693 and 9106). No TLS: a
// protocol built on these needs an outside review of the constant-time
// rules below and of the disassembly the constant-time test
// classifies.
//
// Constant time. A function here takes time and touches memory
// independently of secret data: keys, plaintexts, scalars, nonces' secrecy
// aside, and the intermediate values derived from them. Public data are the
// lengths of every argument, the associated data, the ciphertext, the tag
// of a sealed message, messages to sign, public keys and signatures. The
// rules every file follows:
// - no `if`, `for` bound, `&&`, `||`, `match` or index depends on a secret;
//   a choice between two secret-derived values is a mask select,
//   b ^ ((a ^ b) & m) with m all ones or zero, and a mask comes from
//   arithmetic (0 -% bit), never from a comparison;
// - all secret arithmetic is +%, -%, *%, shifts by constants,
//   raw.Mul128 (x86-64 mulq, whose time does not depend on its operands),
//   and raw.AddCarry64 and raw.SubBorrow64 (add and adc, sub and sbb);
//   no division and no variable shift touches a secret;
// - a carry or borrow that decides a branch, as a range check whose answer
//   is public does, comes from math/bits' formula (fiat.mk's
//   FiatSubBorrow), never from raw.SubBorrow64 or raw.AddCarry64: LLVM
//   reads a chain of their flags as a comparison of the two numbers and
//   splits the branch on it into one jump per limb, the first on the
//   secret's top limb. A select on such a flag (the final conditional
//   subtraction of a reduction, P256Reduce's, the fiat files' and
//   PolySum's) is a cmov in every build read so far, where the formula gave
//   masks: that too is LLVM's choice, which the constant-time tests read;
// - a table lookup with a secret index reads every entry and keeps one by
//   mask (the Ed25519 comb, edwards.mk);
// - the only secret-dependent result that leaves a function is the output
//   itself; Equal and the tag check reduce the difference to one word and
//   compare it with zero once, at the end.
// One exception: Argon2id (argon2.mk) reads its memory at indices derived
// from the password in its second half, as RFC 9106 designs it; only its
// tag comparison is constant time.
// Mk has no optimisation barrier, so LLVM could in principle turn a mask
// select back into a branch: TestSTDCRYPTO_ConstantTimeDisassembly
// classifies every conditional jump of the release build's secret paths
// and fails on one that is neither a check nor a loop.

import std.simd.kernel

/// A sealed message does not authenticate: its tag does not match the key,
/// nonce, associated data and ciphertext.
export error BadTag

/// A buffer has the wrong length for the operation: a key, digest, output,
/// tag or input, or a message past the mode's limit.
export error BadLength

/// A key of the right length is refused: an EC secret that is zero or not
/// below the group order, a public point not on the curve, an RSA modulus
/// outside 2048 to 8192 bits or an exponent that is even or outside 3 to
/// 2^32 - 1, a key of a curve or kind the operation does not take, or a key
/// object used after its Wipe.
export error BadKey

/// A PKCS#8 private key is encrypted. std.crypto reads only unencrypted
/// keys.
export error Encrypted

/// Bytes that should hold a key or a signature are not its encoding.
export error BadEncoding

/// An Argon2 cost is outside RFC 9106's limits: memory below 8 KiB per lane
/// or above 2^32 - 1 KiB, passes outside 1 to 2^32 - 1, or lanes outside 1
/// to 2^24 - 1.
export error BadCost

/// Error is every failure of std.crypto. It is open, so a match on it
/// outside std keeps a _ arm.
export open type Error = BadTag | BadLength | BadKey | Encrypted | BadEncoding | BadCost

/// HashKind names a SHA-2 hash of std.hash: the hash under HKDF, or the
/// hash that made the digest an RSA signature covers.
export open enum HashKind {
    Sha256
    Sha384
    Sha512
}

// HashLen is the digest length of h in bytes.
HashLen(h: HashKind): int {
    return match h {
        .Sha256 { 32 }
        .Sha384 { 48 }
        .Sha512 { 64 }
    }
}

/// Reports whether a and b hold the same bytes, in time that depends only
/// on their lengths. Unequal lengths are false at once; otherwise every
/// byte pair is compared and the answer is decided once, at the end.
/// Compare tags, MACs and other secret-derived bytes with Equal. Cannot
/// fail. O(n); allocates nothing.
export Equal(a: []u8, b: []u8): bool {
    n := a.Len()
    if b.Len() != n {
        return false
    }
    var d: u8 = 0
    for i in 0..n {
        d = d | (a[i] ^ b[i])
    }
    return d == 0
}

/// Fills buf with bytes from the operating system's cryptographically
/// secure generator (ProcessPrng on Windows, getrandom on Linux), through
/// Io.Random. No generator of std's own runs in between, so std holds no
/// state to leak or to repeat after a fork. Fails with Canceled exactly when
/// Io.Random does; buf may then hold anything and must not be used. O(n)
/// plus the system call; allocates nothing.
///
/// Under a simulated Io from std.sim the bytes come from the simulation's
/// seeded stream: they repeat from run to run and are not secret.
export Random(io: Io, buf: mut []u8): Canceled!void {
    try io.Random(mut buf[..])
}

/// Sets every byte of buf to 0 with stores the compiler keeps even when buf
/// is never read again, so a key does not stay in memory after its use.
/// Every key object's Wipe calls it. Cannot fail. O(n); allocates nothing.
/// It cannot run in compile-time evaluation (E0901).
export Wipe(buf: mut []u8) {
    kernel.Wipe(mut buf[..])
}
