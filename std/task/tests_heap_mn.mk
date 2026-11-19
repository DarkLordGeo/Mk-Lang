// Why: under M:N scheduling a heap block is often allocated on one carrier
// and freed on another; the heap must stay correct and reuse such blocks.
// What: RT-Heap-OneOwner and RT-HeapRemoteFree (run by std/rt-sched_harness_test.go),
// with the helpers HmSize, HmFill, HmCheck, HmChurn and HmFreeAll.
// How: tasks fill each block with a pattern of its owner and check it before
// the free, so a block handed to two owners at once shows as a bad pattern.

import std.raw
import std.rt

// HmSize is the size of block k of seed: 1 to 8,192 bytes over the classes.
HmSize(x: u64): int {
    return 1 + Trunc[int]((x % 8192) >> ((x >> 60) % 8))
}

// HmFill writes tag into every 8-byte word of p's first n bytes (at least
// one byte: the low byte of tag).
HmFill(p: *u8, n: int, tag: u64) {
    unsafe "p holds n bytes" {
        for i in 0..n {
            raw.Write(raw.Offset(p, i), Trunc[u8](tag >> Trunc[u64]((i & 7) * 8)))
        }
    }
}

HmCheck(p: *u8, n: int, tag: u64): bool {
    unsafe "p holds n bytes" {
        for i in 0..n {
            if raw.Read(raw.Offset(p, i)) != Trunc[u8](tag >> Trunc[u64]((i & 7) * 8)) {
                return false
            }
        }
    }
    return true
}

// HmChurn keeps up to 32 live blocks of random sizes for n steps, checking
// each block's pattern before it frees it.
HmChurn(io: Io, seed: u64, n: int, bad: &raw.Atomic[u32]) {
    var live: [32]*u8 = [raw.Null[u8](); 32]
    var size: [32]int = [0; 32]
    var tag: [32]u64 = [0; 32]
    var x := seed
    for k in 0..n {
        x = Mix(x)
        i := Trunc[int](x & 31)
        if !raw.IsNull(live[i]) {
            if !HmCheck(live[i], size[i], tag[i]) {
                _ = bad.Add(1)
            }
            rt.HeapFree(live[i], size[i], 8)
            live[i] = raw.Null[u8]()
        }
        if x & 64 != 0 {
            n2 := HmSize(Mix(x))
            p := rt.HeapAlloc(n2, 8)
            if raw.IsNull(p) {
                _ = bad.Add(1)
            } else {
                HmFill(p, n2, x)
                live[i] = p
                size[i] = n2
                tag[i] = x
            }
        }
        if k % 997 == 0 {
            YieldNow()
        }
    }
    for i in 0..32 {
        if !raw.IsNull(live[i]) {
            if !HmCheck(live[i], size[i], tag[i]) {
                _ = bad.Add(1)
            }
            rt.HeapFree(live[i], size[i], 8)
        }
    }
}

test "RT-Heap-OneOwner 8 tasks churn blocks of every class across carriers" (sys: &System) {
    bad := raw.Atomic[u32].New(0)
    scope s := sys.io.Scope() {
        for k in 0..8 {
            s.go HmChurn(sys.io, Trunc[u64](k) * 0x1234_5678 + 1, 40_000, &bad)
        }
    }
    Assert(bad.Load() == 0)
}

// HmFreeAll frees the blocks of addrs[from..to] after checking them; each
// address is a block of HmSize(tag) bytes with the pattern tag = index.
HmFreeAll(io: Io, addrs: uint, from: int, to: int, bad: &raw.Atomic[u32]) {
    YieldNow()
    unsafe "addrs is the block's array of addresses, alive until the scope ends" {
        a := raw.FromAddr[uint](addrs)
        for i in from..to {
            p := raw.FromAddr[u8](raw.Read(raw.Offset(a, i)))
            n := HmSize(Mix(Trunc[u64](i) + 1))
            if !HmCheck(p, n, Trunc[u64](i) + 1) {
                _ = bad.Add(1)
            }
            rt.HeapFree(p, n, 8)
        }
    }
}

test "RT-HeapRemoteFree blocks freed by tasks on other carriers are reused, and the heap stops growing" (sys: &System) {
    bad := raw.Atomic[u32].New(0)
    var addrs: [4096]uint = [0; 4096]
    var segs: [6]int = [0; 6]
    for round in 0..6 {
        for i in 0..4096 {
            n := HmSize(Mix(Trunc[u64](i) + 1))
            p := rt.HeapAlloc(n, 8)
            Assert(!raw.IsNull(p))
            HmFill(p, n, Trunc[u64](i) + 1)
            unsafe "only p's address is stored; nothing is dereferenced" {
                addrs[i] = raw.Addr(p)
            }
        }
        unsafe "the array outlives the scope" {
            base := raw.Addr(raw.Data(addrs[..]))
            scope s := sys.io.Scope() {
                for k in 0..8 {
                    s.go HmFreeAll(sys.io, base, k * 512, k * 512 + 512, &bad)
                }
            }
        }
        segs[round] = rt.HeapSegments()
    }
    Assert(bad.Load() == 0)
    // After the first rounds the segments are reused: the last three rounds
    // add none.
    Assert(segs[5] == segs[2])
}
