// Why: output written a piece at a time (an HTTP body, a log) cannot wait
// for the whole input, so it needs a streaming encoder beside Deflate.
//
// What: compress.Encoder (New, Write, Flush, Finish, Abandon) and Framing:
// deflate (RFC 1951), gzip (RFC 1952) or zlib (RFC 1950).
//
// How: input is copied into a history buffer; each ChunkIn bytes (or the
// bytes pending at Flush and Finish) are compressed by Deflate's matcher
// (Scan) with the WSize bytes before them as history, and sent as blocks
// that end with the chunk, so the window slides only between blocks: the
// last WSize bytes move to the buffer's start and the tables' base drops by
// the shift. This is Go's compress/flate writer shape (fastGen.addBlock,
// with its fastEncL6). Flush is zlib's Z_SYNC_FLUSH: the pending input, then
// an empty stored block, so a reader can decode everything written so far.

import std.hash

// ChunkIn is the most input compressed at once, so a chunk's output fits
// OutCap; HistCap is the history buffer, a window of history and one chunk.
// A chunk is compressed when ChunkIn bytes are pending or the buffer is
// full, and then the buffer slides to keep only the window.
const ChunkIn = 65536
const HistCap = 98304

// OutCap is the output buffer: a chunk's blocks are each at most their
// stored form (DeflateBound), plus a sync flush's empty stored block (at
// most 9 bytes with its padding) and the final padding byte.
const OutCap = 65760

/// The framing an Encoder writes around its deflate stream: none (raw
/// DEFLATE, RFC 1951), a gzip member (RFC 1952), or a zlib stream (RFC
/// 1950).
export enum Framing {
    /// Raw DEFLATE (RFC 1951), with no header or trailer.
    Raw
    /// A gzip member (RFC 1952), with its CRC-32 and size.
    Gzip
    /// A zlib stream (RFC 1950), with its Adler-32.
    Zlib
}

// EncState is an Encoder's scalar state, one element of a slice in its
// arena: the framing and level, the history's length hn of which
// hist[0..done] is compressed, the tables' base, the bit writer with its
// open block, whether the header is written, whether a write to w failed,
// and the checksum and length of the input so far (gzip's CRC-32 and
// size, zlib's Adler-32). Plain, so the Encoder changes it through the
// slice: Finish may change it and still fail with the Encoder unchanged.
struct EncState {
    framing: Framing
    level: int
    hn: int
    done: int
    tbase: int
    e: Enc
    started: bool
    failed: bool
    crc: hash.Crc32State
    adl: u32
    size: int
}

/// A streaming compressor that writes deflate, gzip or zlib to a Writer.
/// It is linear: a path that ends without Finish or Abandon does not
/// compile, so a stream is never cut short in silence. It holds about
/// 610 KiB in its arena (history, match tables, symbol and output
/// buffers); at level 0 about 162 KiB.
export linear view struct Encoder {
    s: mut []EncState
    hist: mut []u8
    out: mut []u8
    st: mut []u32
    lc: mut []u32
    lp: mut []u32
    syms: mut []u32
}

/// An Encoder that writes framing around one deflate stream. Level 0
/// writes stored blocks only; every other level compresses as Deflate
/// does. It writes nothing until the first Write, Flush or Finish.
///
/// Fails only with OutOfMemory. O(1) plus the zero-filling of its
/// buffers; allocates them in a, as Encoder says.
export Encoder.New(a: &Arena, framing: Framing, level: int): OutOfMemory!Encoder from a {
    tn := if level == 0 { 1 } else { TSize }
    sn := if level == 0 { 1 } else { BlockSyms }
    s := try a.Slice[EncState](1)
    s[0] = EncState(framing: framing, level: level, hn: 0, done: 0, tbase: 0, e: NewEnc(), started: false,
        failed: false, crc: hash.Crc32State.New(), adl: 1, size: 0)
    hist := try a.Slice[u8](HistCap)
    out := try a.Slice[u8](OutCap)
    st := try a.Slice[u32](tn)
    lc := try a.Slice[u32](tn)
    lp := try a.Slice[u32](tn)
    syms := try a.Slice[u32](sn)
    return Encoder(s: s, hist: hist, out: out, st: st, lc: lc, lp: lp, syms: syms)
}

/// Compresses data into the stream. Each 64 KiB of input is compressed and
/// written to w as it fills; the rest waits for a later call.
///
/// Fails with WriteError when w does, and then on every later Write and
/// Flush: an unknown prefix of the stream reached w, so the stream can
/// only be abandoned. O(data.Len()), with one copy of data into the
/// history; allocates nothing.
export Encoder.Write(mut self, w: mut Writer, data: []u8): WriteError!void {
    s := mut self.s[0]
    if s.failed {
        fail WriteError
    }
    Sum(s, data)
    var p := 0
    for p < data.Len() {
        // At most ChunkIn pending bytes, and no byte past the buffer.
        hn := s.hn
        room := IntMin(ChunkIn - (hn - s.done), HistCap - hn)
        k := IntMin(data.Len() - p, room)
        (mut self.hist[hn..hn + k]).CopyFrom(data[p..p + k])
        s.hn = hn + k
        p += k
        if k == room {
            EncodeChunk(s, self.hist[..], mut self.out[..], mut self.st[..], mut self.lc[..], mut self.lp[..], mut self.syms[..], false)
            try SendOut(s, self.out[..], w)
            Slide(s, mut self.hist[..])
        }
    }
}

/// A sync flush: compresses the pending input, ends the block, adds an
/// empty stored block so the stream is byte-aligned, writes it all to w
/// and flushes w. A reader of w can then decode every byte written so far.
/// Each call costs a block header and 5 bytes of output.
///
/// Fails as Write does. O(pending input); allocates nothing.
export Encoder.Flush(mut self, w: mut Writer): WriteError!void {
    s := mut self.s[0]
    if s.failed {
        fail WriteError
    }
    if s.done < s.hn {
        EncodeChunk(s, self.hist[..], mut self.out[..], mut self.st[..], mut self.lc[..], mut self.lp[..], mut self.syms[..], false)
    }
    WriteStored(mut s.e, mut self.out[..], self.hist[0..0], false)
    try SendOut(s, self.out[..], w)
    // A buffered w may report the failed write only here.
    w.Flush() else {
        s.failed = true
        fail WriteError
    }
}

/// Compresses the pending input as the final block, writes the trailer
/// (gzip's CRC-32 and length, zlib's Adler-32) and consumes the Encoder.
/// It does not flush w.
///
/// Fails with WriteError when w does, or when an earlier write failed;
/// the Encoder is then still the caller's, to Abandon. O(pending input);
/// allocates nothing.
export Encoder.Finish(self, w: mut Writer): WriteError!void {
    s := mut self.s[0]
    if s.failed {
        fail WriteError
    }
    EncodeChunk(s, self.hist[..], mut self.out[..], mut self.st[..], mut self.lc[..], mut self.lp[..], mut self.syms[..], true)
    ByteAlign(mut s.e, mut self.out[..])
    q := s.e.q
    match s.framing {
        .Raw {}
        .Gzip {
            PutLe32(mut self.out[..], q, s.crc.Sum())
            PutLe32(mut self.out[..], q + 4, Trunc[u32](s.size))
            s.e.q = q + 8
        }
        .Zlib {
            PutBe32(mut self.out[..], q, s.adl)
            s.e.q = q + 4
        }
    }
    try SendOut(s, self.out[..], w)
    Encoder(s: _, hist: _, out: _, st: _, lc: _, lp: _, syms: _) := self
}

/// Ends the Encoder without finishing the stream, and writes nothing. Use
/// it on an error path and for an Encoder whose Write, Flush or Finish
/// failed. O(1); frees nothing, since the memory is its arena's.
export Encoder.Abandon(self) {
    Encoder(s: _, hist: _, out: _, st: _, lc: _, lp: _, syms: _) := self
}

// IntMin is the smaller of x and y.
IntMin(x: int, y: int): int {
    return if x < y { x } else { y }
}

// NewEnc is a bit writer with no bits held and an empty first block.
NewEnc(): Enc {
    return Enc(acc: 0, cnt: 0, q: 0, litf: [0; 286], distf: [0; 30], nsym: 0, bstart: 0)
}

// Sum adds data to the trailer's checksum and length.
Sum(s: mut EncState, data: []u8) {
    match s.framing {
        .Raw {}
        .Gzip {
            s.crc.Bytes(data)
            s.size += data.Len()
        }
        .Zlib {
            s.adl = AdlerUpdate(s.adl, data)
        }
    }
}

// EncodeChunk compresses hist[done..hn] into out as blocks that end at hn,
// the last one final when final is set (an empty final block when nothing
// is pending), with hist[0..done] as history.
EncodeChunk(s: mut EncState, hist: []u8, out: mut []u8, st: mut []u32, lc: mut []u32, lp: mut []u32, syms: mut []u32, final: bool) {
    start := s.done
    n := s.hn
    data := hist[0..n]
    if s.level == 0 {
        if start < n || final {
            WriteStored(mut s.e, out, data[start..n], final)
        }
    } else {
        r := if n - start >= 13 { Scan(mut s.e, out, data, start, mut s.tbase, st, lc, lp, syms) } else { start }
        Lits(mut s.e, out, data, syms, r, n)
        if s.e.nsym > 0 || final {
            FlushBlock(mut s.e, out, data, syms[..], n, final)
        }
    }
    s.done = n
}

// SendOut writes the stream's header if it is not yet written, then the
// whole bytes of out, and empties out; the bit writer keeps its partial
// byte. A failed write marks the stream failed.
SendOut(s: mut EncState, out: []u8, w: mut Writer): WriteError!void {
    onerr {
        s.failed = true
    }
    if !s.started {
        try WriteFraming(&*s, w)
        s.started = true
    }
    try w.Bytes(out[0..s.e.q])
    s.e.q = 0
}

// WriteFraming writes the framing's header: gzip's as Gzip writes it, zlib's
// 0x78 with the level's FLG.
WriteFraming(s: &EncState, w: mut Writer): WriteError!void {
    match s.framing {
        .Raw {}
        .Gzip {
            var h: [10]u8 = [0x1F, 0x8B, 8, 0, 0, 0, 0, 0, 0, 0xFF]
            try w.Bytes(h[..])
        }
        .Zlib {
            var h: [2]u8 = [0x78, ZlibFlg(s.level)]
            try w.Bytes(h[..])
        }
    }
}

// Slide keeps the last WSize bytes of the history at the buffer's start and
// lowers the tables' base by the shift, so every entry still names its
// byte; entries for bytes slid out name positions before 0. Called only
// between chunks, when no block is open and nothing is pending (done ==
// hn), and only when hn is past WSize.
Slide(s: mut EncState, hist: mut []u8) {
    shift := s.hn - WSize
    MoveDown(hist, shift, WSize)
    s.tbase -= shift
    s.hn = WSize
    s.done = WSize
    s.e.bstart = WSize
}
