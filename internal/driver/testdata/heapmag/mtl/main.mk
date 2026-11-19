// A threads program of TestHeapMainUsesMagazine (std/rt/heap.mk, MagOf):
// no task construct, so Main runs on carrier
// 0's system stack and takes magazine 0, while a thread C starts
// (libc's pthread_create) must take the shared cache. Both run a
// tagged-ownership churn on the process heap and hand blocks to each other through two
// single-producer rings, so each frees blocks the other allocated. Every
// live block carries a tag at both ends, checked before its free; a fresh
// block found in the thread's own live set, or a tag another owner
// overwrote, counts as bad. Kept (as in heapmag_test.go) tells a free into
// a magazine from a free to the page. A MagOf that gave the C thread
// magazine 0 too prints a C thread count above 0, and its unlocked pushes
// and pops racing Main's show as bad blocks.
import std.raw


Mix(x: u64): u64 {
    var z := x
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb
    return z ^ (z >> 31)
}

Size(x: u64): int {
    if (x >> 52) & 255 == 0 {
        return 8193 + Trunc[int]((x >> 8) % 60_000)
    }
    return 1 + Trunc[int]((x % 8192) >> ((x >> 60) % 8))
}

Get(a: &Alloc, n: int): *u8 {
    unsafe "a raw block of n bytes, freed by Put with the same n" {
        return a.Bytes(n, 8)
    }
}

Put(a: &Alloc, p: *u8, n: int) {
    unsafe "p is a block Get gave for n bytes, freed once" {
        a.FreeBytes(p, n, 8)
    }
}

Mark(p: *u8, n: int, tag: u64) {
    unsafe "p holds n bytes" {
        raw.Write(p, Trunc[u8](tag))
        raw.Write(raw.Offset(p, n - 1), Trunc[u8](tag >> 8))
        if n >= 16 {
            raw.Write(raw.PtrCast[u8, u64](p), tag)
            raw.Write(raw.PtrCast[u8, u64](raw.Offset(p, n - 8)), ~tag)
        }
    }
}

Marked(p: *u8, n: int, tag: u64): bool {
    unsafe "p holds n bytes" {
        if n >= 16 {
            return raw.Read(raw.PtrCast[u8, u64](p)) == tag && raw.Read(raw.PtrCast[u8, u64](raw.Offset(p, n - 8))) == ~tag
        }
        return (n == 1 || raw.Read(p) == Trunc[u8](tag)) && raw.Read(raw.Offset(p, n - 1)) == Trunc[u8](tag >> 8)
    }
}

const RingN: int = 128

// A single-producer, single-consumer ring of blocks: the producer writes a
// slot, then stores tail (seq_cst); the consumer loads tail, reads the slot,
// then stores head.
struct Ring {
    head: raw.Atomic[u64]
    tail: raw.Atomic[u64]
    addr: [128]uint
    size: [128]int
    tag: [128]u64
}

struct Shared {
    kept: raw.Atomic[u64]
    alloc: *Alloc
    to_worker: Ring
    to_main: Ring
    steps: int
    seed: u64
    bad: raw.Atomic[u64]
    stop: raw.Atomic[u64]
}

NewRing(): Ring {
    return Ring(head: raw.Atomic[u64].New(0), tail: raw.Atomic[u64].New(0), addr: [0; 128], size: [0; 128], tag: [0; 128])
}

// Send gives block p to the ring's consumer; false when the ring is full.
Hand(r: *Ring, p: *u8, n: int, tag: u64): bool {
    unsafe "r is a ring of Shared, whose producer is this thread" {
        t := (*r).tail.Load()
        if t - (*r).head.Load() == Trunc[u64](RingN) {
            return false
        }
        s := Trunc[int](t % Trunc[u64](RingN))
        (*r).addr[s] = raw.Addr(p)
        (*r).size[s] = n
        (*r).tag[s] = tag
        (*r).tail.Store(t + 1)
        return true
    }
}

// Drain frees every block waiting in the ring, after checking its tag; it
// returns the bad count.
Drain(r: *Ring, a: &Alloc): int {
    var bad := 0
    unsafe "r is a ring of Shared, whose consumer is this thread" {
        for {
            h := (*r).head.Load()
            if h == (*r).tail.Load() {
                return bad
            }
            s := Trunc[int](h % Trunc[u64](RingN))
            p := raw.FromAddr[u8]((*r).addr[s])
            n := (*r).size[s]
            if !Marked(p, n, (*r).tag[s]) {
                bad += 1
            }
            (*r).head.Store(h + 1)
            Put(a, p, n)
        }
    }
    return bad
}

// Churn keeps up to 64 live blocks, frees or sends one at random each step,
// and frees what the other thread sent.
Churn(a: &Alloc, out: *Ring, inbox: *Ring, steps: int, seed: u64): int {
    var live: [64]*u8 = [raw.Null[u8](); 64]
    var size: [64]int = [0; 64]
    var tag: [64]u64 = [0; 64]
    var x: u64 = seed
    var bad := 0
    for step in 0..steps {
        x = Mix(x)
        i := Trunc[int](x & 63)
        if !raw.IsNull(live[i]) {
            if !Marked(live[i], size[i], tag[i]) {
                bad += 1
            }
            if x & 128 == 0 || !Hand(out, live[i], size[i], tag[i]) {
                Put(a, live[i], size[i])
            }
            live[i] = raw.Null[u8]()
        }
        if x & 64 != 0 {
            s := Size(Mix(x))
            p := Get(a, s)
            if raw.IsNull(p) {
                bad += 1
            } else {
                for j in 0..64 {
                    if raw.Same(live[j], p) {
                        bad += 1
                    }
                }
                Mark(p, s, x)
                live[i] = p
                size[i] = s
                tag[i] = x
            }
        }
        if step & 15 == 0 {
            bad += Drain(inbox, a)
        }
    }
    for i in 0..64 {
        if !raw.IsNull(live[i]) {
            if !Marked(live[i], size[i], tag[i]) {
                bad += 1
            }
            Put(a, live[i], size[i])
        }
    }
    return bad
}

// libc's pthreads (lib/libc.def): a thread C starts, not std.
extern pthread_create(t: *u64, attr: *u8, start: fn(*u8): *u8, arg: *u8): i32
extern pthread_join(t: u64, ret: *u8): i32

Worker(p: *u8): *u8 {
    unsafe "p is Main's Shared, alive until Main has joined this thread" {
        sh := raw.PtrCast[u8, Shared](p)
        a := &*(*sh).alloc
        if (*sh).seed == 1 {
            _ = (*sh).kept.Add(Trunc[u64](KeptCount(a)))
        }
        b := Churn(a, raw.OfMut(mut (*sh).to_main), raw.OfMut(mut (*sh).to_worker), (*sh).steps, (*sh).seed ^ 0x77)
        _ = (*sh).bad.Add(Trunc[u64](b))
        (*sh).stop.Store(1)
    }
    return raw.Null[u8]()
}

// Kept allocates size bytes, writes a pattern in the first word, frees the
// block and reads the word again: a free into a magazine leaves it, a free
// to the block's page writes the free-list link there (heapmag_test.go's
// test). Each probe's allocation comes after a pop or a refill, so its free
// is a push, never a drain.
Kept(a: &Alloc, size: int): bool {
    p := Get(a, size)
    if raw.IsNull(p) {
        return false
    }
    unsafe "p is a fresh block of size >= 8 bytes" {
        raw.Write(raw.PtrCast[u8, u64](p), 0xA5A5_A5A5_A5A5_A5A5)
    }
    Put(a, p, size)
    unsafe "p's block was freed and is still mapped; the read is the probe" {
        return raw.Read(raw.PtrCast[u8, u64](p)) == 0xA5A5_A5A5_A5A5_A5A5
    }
}

KeptCount(a: &Alloc): int {
    var k := 0
    for s in [16, 64, 1024, 8192] {
        if Kept(a, s) {
            k += 1
        }
    }
    return k
}

export Main(env: &System): WriteError!void {
    a := env.heap.Alloc()
    mk := KeptCount(a)
    var wk: u64 = 0
    var rounds := 20
    var steps := 300_000
    var total: u64 = 0
    for r in 0..rounds {
        var sh := Shared(alloc: raw.Null[Alloc](), to_worker: NewRing(), to_main: NewRing(), steps: steps,
            seed: Trunc[u64](r) * 0x9E37_79B9 + 1, bad: raw.Atomic[u64].New(0), stop: raw.Atomic[u64].New(0), kept: raw.Atomic[u64].New(0))
        var h: u64 = 0
        var rc: i32 = 0
        unsafe "sh outlives the thread: it is joined below before sh goes" {
            sh.alloc = raw.Of(a)
            rc = pthread_create(raw.OfMut(mut h), raw.Null[u8](), Worker, raw.PtrCast[Shared, u8](raw.OfMut(mut sh)))
        }
        if rc != 0 {
            try env.Print("thread start failed\n", ())
            return
        }
        var mb := 0
        unsafe "the rings live in sh, on this frame" {
            mb = Churn(a, raw.OfMut(mut sh.to_worker), raw.OfMut(mut sh.to_main), steps, sh.seed)
            for sh.stop.Load() == 0 {
                mb += Drain(raw.OfMut(mut sh.to_main), a)
            }
        }
        unsafe "the worker has ended; both rings are this thread's now" {
            _ = pthread_join(h, raw.Null[u8]())
            mb += Drain(raw.OfMut(mut sh.to_main), a)
            mb += Drain(raw.OfMut(mut sh.to_worker), a)
        }
        total += sh.bad.Load() + Trunc[u64](mb)
        wk += sh.kept.Load()
    }
    try env.Print("bad {b} of {r} rounds; Main kept {m} of 4, the pthread {w} of 4\n", (b: total, r: rounds, m: mk, w: wk))
}
