// Why: std's random bytes come from the operating system's cryptographic
// generator, which must be reached without adding a DLL import to every
// executable.
//
// What: Random, which fills a buffer from the system generator.
//
// How: ProcessPrng, loaded on first use from bcryptprimitives.dll, so the
// import table names only kernel32 (as Go's runtime does).

import std.raw

struct RandomState {
    // prng: the address of ProcessPrng, 0 before the first use, 1 when it
    // cannot be loaded.
    prng: raw.Atomic[u64]
}

var Rng: RandomState = RandomState(prng: raw.Atomic[u64].New(0))

// LoadLibrarySearchSystem32 is LOAD_LIBRARY_SEARCH_SYSTEM32.
const LoadLibrarySearchSystem32: u32 = 0x800

// Prng finds ProcessPrng, or null.
Prng(): *u8 {
    unsafe "kernel32; the names are NUL-terminated constants" {
        // Edge: the loader's Store below, made after it mapped the DLL.
        a := Rng.prng.Load()
        if a > 1 {
            return raw.FromAddr[u8](Trunc[uint](a))
        }
        if a == 1 {
            return raw.Null[u8]()
        }
        var name: [32]u16 = [0; 32]
        _ = Wide("bcryptprimitives.dll", mut name[..])
        m := LoadLibraryExW(raw.Data(name[..]), 0, LoadLibrarySearchSystem32)
        p := if m != 0 { GetProcAddress(m, raw.StrData("ProcessPrng\u{0}")) } else { raw.Null[u8]() }
        // A cache: every racing loader stores the same address (or 1), and
        // the DLL stays loaded (each racing LoadLibraryExW only adds a
        // reference). Edge: this seq_cst Store to the Load above in another
        // thread that reads the address; the thread that calls through it
        // then sees everything the loader wrote while it mapped and
        // initialized bcryptprimitives.dll before this Store.
        if raw.IsNull(p) {
            Rng.prng.Store(1)
            return p
        }
        Rng.prng.Store(u64(raw.Addr(p)))
        return p
    }
}

/// Random fills all of buf from ProcessPrng, which is documented to always
/// succeed; false only when it cannot be loaded.
/// O(len(buf)); allocates nothing (the first call loads
/// bcryptprimitives.dll, kept until exit).
export Random(buf: mut []u8): bool {
    p := Prng()
    if raw.IsNull(p) {
        return false
    }
    unsafe "p is ProcessPrng(PBYTE, SIZE_T): BOOL" {
        var word := p
        f := raw.Read(raw.PtrCast[*u8, fn(*u8, uint): i32](raw.OfMut(mut word)))
        _ = f(raw.DataMut(buf), Trunc[uint](buf.Len()))
    }
    return true
}
