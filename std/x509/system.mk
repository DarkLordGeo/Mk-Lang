// Why: a program that verifies many chains (std.tls with no roots of its
// own) should read the system's certificate store once, not per handshake.
//
// What: SystemPool and its process cell SystemCell (a Mutex[PoolCell]),
// CellPool, LoadPool, and the block helpers CopyLayout, MakeCopy, KeepCopy,
// FreeCopy, KeptInBlock and Moved.
//
// How: as Go's crypto/x509 loads systemRoots once (sync.Once): SystemPool
// keeps the first load that finds a certificate (on Windows, the pool that
// stands for the platform) in one heap block that is never freed, where
// SystemRoots reads the store at each call. Why that is sound follows.

// Where the pool lives: the load runs in an arena of h, freed after. The
// pool it made is copied (MakeCopy) into one block of the process heap's
// allocator: the Pool, then its certificates, then their DER. Each
// certificate's data is rebased onto its DER copy inside the block, and the
// Pool's certificates onto the block's certificates, so every reference
// stored in the block points into the block. A refused block keeps nothing.
// Keeping (KeepCopy) then never frees the block, and casts its Pool to a
// reference with no loan. That is sound because (a) the process heap is
// never released, (b) the block holds no reference to anything outside
// itself, (c) Pool, Certificate, Span and time.Time have no
// interior-mutable field (no Mutex, Cell or Atomic), so nothing can be
// stored through the loan-free &Pool, and (d) a PoolCopy's p is a block of
// its owner that MakeCopy filled (only MakeCopy makes one). Premise (c)
// matters because the checker does not see a store through a loan-free &
// result reach a global. The checker cannot see this proof, which is why
// std.x509 is in std's unsafe_modules. std/x509 has three unsafe blocks,
// all in this file: MakeCopy's writes, KeepCopy's cast and FreeCopy, which
// only the tests call. The grant admits five forms (an unsafe block, an
// unsafe impl, an extern var, a section clause and an ends primitive).
// TestX509_UnsafeBlocksPinned holds the blocks at three and the other four
// at none, with one PoolCopy literal, MakeCopy's (premise (d)), and
// TestX509_KeptTypesPinned pins the fields of the kept types, so a new
// block or a new field fails a test until this proof, Moved and KeptInBlock
// are looked at again. KeepCopy's reason string uses the same four labels.
// TestX509_SystemPoolBlocks checks premise (b): a kept pool's references
// lie inside its block, and survive the load's arena being freed and
// overwritten. A test build's leak count does not count the kept block
// (rt.KeptForever). Loads are serialized by a lock that parks its waiters
// (std's Mutex), so two first calls on two carriers make one load, and a
// load that failed keeps nothing: the next call loads again.

import std.core
import std.raw
import std.rt

// PoolCell is one process-wide pool, under its lock: the pool kept (none
// before), the empty pool handed out when a load found no certificate
// (made once), and the loads begun (for tests).
struct PoolCell {
    pool: ?&Pool
    empty: ?&Pool
    loads: int
}

// SystemCell is the system's pool for the process.
var SystemCell: Mutex[PoolCell] = Mutex[PoolCell].New(PoolCell(pool: none, empty: none, loads: 0))

/// The system's trusted roots, loaded once for the process: the pool
/// SystemRoots gives, read by the first call that finds a certificate and
/// kept, so every later call returns the same pool without reading the
/// store again. O(1) once kept (one uncontended lock); else SystemRoots'
/// cost and one copy of the certificates.
///
/// On Linux a later change to the bundle, or to SSL_CERT_FILE and
/// SSL_CERT_DIR, is not seen. On Windows the pool stands for the platform,
/// which reads its store at each Verify.
///
/// Calls on several tasks make one load; a call that finds another loading
/// waits for it. A load that fails (Canceled, OutOfMemory) keeps nothing
/// and leaves nothing allocated. A load that finds no certificate is not
/// kept either: it returns an empty pool, and the next call loads again. A
/// file or directory that is missing or cannot be opened, read or listed
/// (any std.fs error but Canceled) is skipped, as SystemRoots skips it, and
/// a load that found a certificate is kept without it.
///
/// Memory: the kept pool lives in one block of h, the process heap, never
/// freed: the Pool, its certificates and their DER, at most once per
/// process (and one empty pool). A test build's leak count does not count
/// it. The load's own memory is in an arena of h, freed before it returns.
///
/// Fails with Unsupported on a target with no system store, and under a
/// simulated or taped Io (a Sim's, Recorder's or Replayer's), whose files
/// are not the process's to keep: call SystemRoots there. Fails with
/// Canceled in a cancelled scope, since taking the lock can be cancelled,
/// or in the load (nothing is consumed; call again), and with OutOfMemory.
export SystemPool(h: &Heap, io: Io): Error!&Pool {
    if Target.os != core.Os.Windows && Target.os != core.Os.Linux {
        fail Unsupported
    }
    if (!core.IsNative(io) && (core.IoFlags(io) & SimBits) != 0) {
        fail Unsupported
    }
    return try CellPool(&SystemCell, h, h.Alloc(), io, "")
}

// CellPool is cell's pool: the one kept, else one loaded from src (""
// the system's store, else a PEM file, for tests on any target) in an
// arena of h under cell's lock, copied into one block of keep (MakeCopy)
// and kept (KeepCopy) when it holds a certificate or stands for the
// platform. keep is h.Alloc() but in tests of the refused copy, which
// never reach KeepCopy (it panics on any other allocator).
CellPool(cell: &Mutex[PoolCell], h: &Heap, keep: &Alloc, io: Io, src: str): Error!&Pool from cell {
    var g := try cell.Lock(io)
    if p := g.Ref().pool {
        g.Unlock()
        return p
    }
    g.Mut().loads += 1
    var arena := Arena.New(h) else {
        g.Unlock()
        fail OutOfMemory
    }
    pool := LoadPool(&arena, io, src) else e {
        arena.Free()
        g.Unlock()
        fail e
    }
    if pool.certs.Len() == 0 && !pool.system {
        // No certificate: nothing is kept, so the next call loads again
        // (a store that appears later, or files that could not be opened
        // this time, are read then); the empty pool handed out is made
        // once.
        if p := g.Ref().empty {
            arena.Free()
            g.Unlock()
            return p
        }
        copy := MakeCopy(keep, &pool) else {
            arena.Free()
            g.Unlock()
            fail OutOfMemory
        }
        arena.Free()
        empty := KeepCopy(copy)
        g.Mut().empty = some(empty)
        g.Unlock()
        return empty
    }
    copy := MakeCopy(keep, &pool) else {
        arena.Free()
        g.Unlock()
        fail OutOfMemory
    }
    // The copy references nothing of the arena, so the arena goes first.
    arena.Free()
    made := KeepCopy(copy)
    g.Mut().pool = some(made)
    g.Unlock()
    return made
}

// PoolCopy is one block of an allocator holding a copy of a pool: the
// Pool at p, its certificates after it, their DER after those (CopyLayout),
// with every reference inside the block. Only MakeCopy makes one, so p is
// always a block of owner that MakeCopy filled (KeepCopy's premise).
// Linear: CellPool keeps it (KeepCopy); only the tests free one
// (FreeCopy).
linear struct PoolCopy {
    p: *u8
    size: int
    owner: *Alloc
}

// CopyAlign is the block's alignment, enough for a Pool and a Certificate
// (a test block asserts AlignOf of each is at most CopyAlign).
const CopyAlign: int = 16

// CopyLayout is where a copy of a pool of n certificates and bytes bytes
// of DER puts them: the certificates at certs, the DER at der, size bytes
// in all; the Pool is at 0.
CopyLayout(n: int, bytes: int): (certs: int, der: int, size: int) {
    ca := AlignOf[Certificate]()
    certs := (SizeOf[Pool]() + ca - 1) / ca * ca
    der := certs + n * SizeOf[Certificate]()
    return (certs: certs, der: der, size: der + bytes)
}

// MakeCopy is pool copied into one block of keep, none when keep refuses
// it (nothing is then allocated). In the block, certificate i's data is
// the copy of its DER at the block's DER area, and the Pool's certificates
// are the block's certificates, so no reference in the block points
// outside it; a certificate's other fields are offsets into its DER and
// plain values (Moved). O(pool's bytes); one allocation.
MakeCopy(keep: &Alloc, pool: &Pool): ?PoolCopy {
    n := pool.certs.Len()
    var bytes := 0
    for i in 0..n {
        bytes += pool.certs[i].data.Len()
    }
    l := CopyLayout(n, bytes)
    q := core.AllocBytes(keep, l.size, CopyAlign)
    unsafe "q is a fresh block of l.size bytes aligned to CopyAlign; every write is inside it, at CopyLayout's offsets; each reference written points into the block (the DER area, the certificates area)" {
        if raw.IsNull(q) {
            return none
        }
        certs := raw.PtrCast[u8, Certificate](raw.Offset(q, l.certs))
        var at := l.der
        for i in 0..n {
            d := pool.certs[i].data
            k := d.Len()
            dst := raw.Offset(q, at)
            if k > 0 {
                raw.Copy(dst, raw.Data(d), k)
            }
            raw.Write(raw.Offset(certs, i), Moved(&pool.certs[i], raw.Slice(dst, k)))
            at += k
        }
        raw.Write(raw.PtrCast[u8, Pool](q), Pool(certs: raw.Slice(certs, n), system: pool.system))
    }
    return some(PoolCopy(p: q, size: l.size, owner: raw.Of(keep)))
}

// FreeCopy returns c's block to the allocator it came from. Only the
// tests call it (SpRefused), to show that a copy that fits holds one
// block of its allocator. O(1).
FreeCopy(c: PoolCopy) {
    PoolCopy(p, size, owner) := c
    unsafe "owner is the allocator MakeCopy took p from, which outlives the copy (tests only free a copy of an allocator they hold)" {
        core.FreeBytes(&*owner, p, size, CopyAlign)
    }
}

// KeepCopy keeps c for the rest of the process and is its Pool, a
// reference with no loan. It panics (code 8) unless c's block is the
// process heap's. O(1); frees nothing.
KeepCopy(c: PoolCopy): &Pool {
    PoolCopy(p, size, owner) := c
    heap := rt.ProcessHeap().Alloc()
    if !raw.Same(owner, raw.Of(heap)) {
        raw.Fail(8, "x509.KeepCopy: a copy not on the process heap")
    }
    rt.KeptForever(heap, 1, size)
    unsafe "(a) p is a block of the process heap, which is never released, and nothing frees p from here on; (d) p is a block of owner that MakeCopy filled (only MakeCopy makes a PoolCopy); (b) MakeCopy wrote a Pool at p whose every reference (its certificates, each certificate's data) points into p's block, and nothing else is stored; (c) Pool, Certificate, Span and time.Time have no interior-mutable field (no Mutex, Cell or Atomic: TestX509_KeptTypesPinned), so nothing is ever stored through the result; so the Pool lives until exit and holds no loan" {
        return &*raw.PtrCast[u8, Pool](p)
    }
}

// KeptInBlock is whether every reference of the kept pool p lies inside
// its block (CopyLayout's areas): the premise of KeepCopy's proof, checked
// by the tests. O(certificates).
KeptInBlock(p: &Pool): bool {
    n := p.certs.Len()
    var bytes := 0
    for i in 0..n {
        bytes += p.certs[i].data.Len()
    }
    l := CopyLayout(n, bytes)
    base := raw.Addr(raw.Of(p))
    if n > 0 && raw.Addr(raw.Data(p.certs)) != base + Trunc[uint](l.certs) {
        return false
    }
    lo := base + Trunc[uint](l.der)
    hi := base + Trunc[uint](l.size)
    for i in 0..n {
        d := p.certs[i].data
        if d.Len() > 0 {
            a := raw.Addr(raw.Data(d))
            if a < lo || a + Trunc[uint](d.Len()) > hi {
                return false
            }
        }
    }
    return true
}

// LoadPool is SystemRoots for src "", else the certificates of the PEM
// file src.
LoadPool(a: &Arena, io: Io, src: str): Error!Pool from a {
    if src.Len() == 0 {
        return try SystemRoots(a, io)
    }
    var out := List[Certificate].New(a)
    _ = try AddFile(a, io, src, mut out)
    all := out.Done()
    return Pool(certs: all[..], system: false)
}

// Moved is c with data as its DER: c's other fields are offsets into its
// DER and plain values, so the copy reads data as c read its own.
Moved(c: &Certificate, data: []u8): Certificate from data {
    return Certificate(data: data, tbs_span: c.tbs_span, sig_span: c.sig_span, issuer_span: c.issuer_span,
        subject_span: c.subject_span, spki_span: c.spki_span, san_span: c.san_span, permitted_span: c.permitted_span,
        excluded_span: c.excluded_span, sig_alg: c.sig_alg, valid_from: c.valid_from, valid_to: c.valid_to, flags: c.flags,
        path_len: c.path_len, key_usage: c.key_usage)
}
